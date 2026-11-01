// Package dms holds the tests of the DMS bar widget (packaging/dms/archmaid)
// and of the pending-updates user timer. The widget's parsing is JavaScript
// (state.js), so these tests make the REAL Go writers (internal/logstore,
// internal/audit) and the real archmaid-updates-check script produce the state
// files, hand them to the JS under qmltestrunner, and so keep the widget's
// expectations from drifting away from the writers.
package dms

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/report"
)

// restartNames: the package names the restart rule is checked against in Go
// and in state.js: the kernel (found differently, so never a restart
// package), the driver and its userspace, the tools next to them, and near
// misses of every part of the pattern.
var restartNames = []string{
	"linux", "linux-lts", "linux-cachyos", "linux-cachyos-headers", "linux-cachyos-nvidia", "linux-firmware", "linux-api-headers",
	"systemd", "systemd-libs", "systemd-sysvcompat", "lib32-systemd", "lib32-lib32-systemd", "systemd-",
	"mesa", "lib32-mesa", "mesa-utils", "mesa-demos", "vulkan-mesa-layers", "lib32-vulkan-mesa-layers", "libmesa", "Mesa", "mesa ",
	"nvidia", "nvidia-utils", "lib32-nvidia-utils", "nvidia-open", "nvidia-open-dkms", "nvidia-dkms", "nvidia-580xx-utils",
	"nvidia-settings", "lib32-nvidia-settings", "nvidia-prime", "lib32-nvidia-prime", "nvidia-container-toolkit",
	"nvidia-", "nvidia-Utils", "nvidia_utils", "nvidia-utils\n", "nvidia-u.ils", "opencl-nvidia", "cuda", "",
	"glibc", "curl", "bash", "firefox", "systemd-boot", "lib32-glibc", "sd", "a-mesa", "lib32-",
}

// restartTable is what internal/report decides for each name; tst_state.qml
// makes state.js agree on every one (TestRestartRuleMatchesGo, run by
// TestStateJS).
func restartTable() map[string]bool {
	m := map[string]bool{}
	for _, n := range restartNames {
		m[n] = report.IsRestartPkg(n)
	}
	return m
}

func restartJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(restartTable())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestStateJSIsPlainJS: state.js is the only JavaScript file in the repo and
// GitHub's CodeQL parses it as plain JavaScript. A QML-only line (".pragma
// library", ".import") makes the parser reject the whole file.
func TestStateJSIsPlainJS(t *testing.T) {
	for i, line := range strings.Split(read(t, "archmaid/state.js"), "\n") {
		if strings.HasPrefix(line, ".") {
			t.Errorf("state.js line %d starts with %q: QML-only syntax, CodeQL cannot parse the file", i+1, line)
		}
	}
}

var t0 = time.Date(2026, 10, 4, 17, 48, 40, 0, time.FixedZone("CEST", 7200))

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lastRun returns last-run.json as the real writer produces it for outcome.
func lastRun(t *testing.T, outcome string, failed int) string {
	t.Helper()
	dir := t.TempDir()
	s := logstore.New(logstore.Options{Dir: dir})
	err := s.WriteLastRun(logstore.RunRecord{Action: "fullcleanup", Started: t0.Add(-time.Minute),
		Finished: t0, Outcome: outcome, StepsFailed: failed})
	if err != nil {
		t.Fatal(err)
	}
	return read(t, filepath.Join(dir, logstore.LastRunFile))
}

// lastRunSummary is last-run.json as the real writer produces it for a run
// that carries the run report's summary.
func lastRunSummary(t *testing.T, sum logstore.RunSummary) string {
	t.Helper()
	dir := t.TempDir()
	s := logstore.New(logstore.Options{Dir: dir})
	err := s.WriteLastRun(logstore.RunRecord{Action: "fullcleanup", Started: t0.Add(-time.Minute),
		Finished: t0, Outcome: logstore.OutcomeOK, Summary: &sum})
	if err != nil {
		t.Fatal(err)
	}
	return read(t, filepath.Join(dir, logstore.LastRunFile))
}

// withRefused turns a real record into the "incomplete" shape (outcome
// "incomplete" plus steps_refused) that the engine is gaining; the Go
// RunRecord does not carry those yet, so the two fields are set on the
// writer's own JSON.
func withRefused(t *testing.T, raw string, refused int, outcome ...string) string {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	m["outcome"] = "incomplete"
	if len(outcome) > 0 {
		m["outcome"] = outcome[0]
	}
	m["steps_refused"] = refused
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// withField sets (or, for nil, deletes) one key of a real record's JSON: an
// older archmaid's record without steps_refused, or a damaged value.
func withField(t *testing.T, raw, key string, val any) string {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if val == nil {
		delete(m, key)
	} else {
		m[key] = val
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

func auditState(t *testing.T, s audit.State) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), audit.StateFile)
	if err := audit.WriteState(p, s); err != nil {
		t.Fatal(err)
	}
	return read(t, p)
}

// yayShim describes a fake yay: its stdout, stderr and exit status.
type yayShim struct {
	out, err string
	rc       int
}

// The `yay -Qua` listing from the owner's machine: two updates older than
// 72 h (3d17h, 3d) and two younger (1d9h, 6h54m).
const yayListing = "bonsai 2.1.1-10 -> 2.2.1-1 [1d9h]\n" +
	"curseforge 1.321.1_39714-1 -> 1.322.0_40357-1 [3d17h]\n" +
	"archon-appimage 9.5.8-1 -> 9.6.140-22 [6h54m]\n" +
	"raiderio-client 5.0.9-1 -> 5.0.11-1 [3d]\n"

// updatesRun runs archmaid-updates-check in a scratch state dir with a fake
// checkupdates (prints out, exits rc) and, when yay is not nil, a fake yay.
// PATH holds only the shims and symlinks to the few tools the script needs,
// so the real checkupdates and yay can never be reached. old, when set, is
// the file already in the state dir. It returns the file afterwards ("" when
// none) and the script's error.
func updatesRun(t *testing.T, out string, rc int, yay *yayShim, old string) (string, error) {
	t.Helper()
	return updatesRunTools(t, out, rc, yay, old, "")
}

// updatesRunTools is updatesRun with a PATH that lacks the tool named skip
// (to make a step of the script fail).
func updatesRunTools(t *testing.T, out string, rc int, yay *yayShim, old, skip string) (string, error) {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"sh", "mktemp", "date", "grep", "chmod", "mkdir", "rm", "mv", "cat", "timeout", "sed", "awk"} {
		if tool == skip {
			continue
		}
		src, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("tool %s: %v", tool, err)
		}
		if err := os.Symlink(src, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	shim := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data := t.TempDir()
	put := func(name, body string) string {
		f := filepath.Join(data, name)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	shim("checkupdates", "cat "+put("cu.out", out)+"\nexit "+strconv.Itoa(rc)+"\n")
	if yay != nil {
		// The script must ask for exactly -Qua and give yay no stdin.
		shim("yay", "[ \"$*\" = -Qua ] || exit 64\nif read -r _; then exit 65; fi\n"+
			"cat "+put("yay.out", yay.out)+"\ncat "+put("yay.err", yay.err)+" >&2\nexit "+strconv.Itoa(yay.rc)+"\n")
	}
	// The script writes under $HOME/.local/state/archmaid, the folder
	// logstore and the widget use, whatever XDG_STATE_HOME says: that
	// variable points at a different scratch dir, which must stay empty.
	home, other := t.TempDir(), t.TempDir()
	state := filepath.Join(home, ".local", "state")
	p := filepath.Join(state, "archmaid", "updates-cache.json")
	if old != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(filepath.Join(bin, "sh"), "../systemd/archmaid-updates-check")
	cmd.Env = []string{"PATH=" + bin, "HOME=" + home, "XDG_STATE_HOME=" + other}
	cmd.Stdin = strings.NewReader("an input line\n")
	runErr := cmd.Run()
	if ents, _ := os.ReadDir(other); len(ents) != 0 {
		t.Fatalf("XDG_STATE_HOME was written to: %v", ents)
	}
	b, rerr := os.ReadFile(p)
	if rerr != nil {
		return "", runErr
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s mode = %v, want 0600", p, fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %v, want 0700", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("state dir keeps stray files: %v", ents)
	}
	return string(b), runErr
}

// updatesWith is updatesRun for a passing script with the given yay.
func updatesWith(t *testing.T, out string, y *yayShim) string {
	t.Helper()
	raw, err := updatesRun(t, out, 0, y, "")
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	return raw
}

// updatesFile is updatesRun for a passing script and no yay installed.
func updatesFile(t *testing.T, out string, rc int) string {
	t.Helper()
	raw, err := updatesRun(t, out, rc, nil, "")
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	return raw
}

func TestUpdatesScript(t *testing.T) {
	type upd struct {
		V       int
		Count   int
		Repo    int
		Aur     *int
		AurHeld *int `json:"aur_held"`
		Checked time.Time
	}
	three := "a 1-1 -> 1-2\nb 1 -> 2\nc 3 -> 4\n"
	for _, c := range []struct {
		name            string
		out             string
		rc              int
		yay             *yayShim
		repo, aur, held int // aur -1 = null (held is null too)
		wantCount       int
	}{
		{"owner's listing: 2 eligible, 2 held", three, 0, &yayShim{out: yayListing}, 3, 2, 2, 5},
		{"only held", three, 0, &yayShim{out: "bonsai 1 -> 2 [1d9h]\nx 1 -> 2 [71h59m]\ny 1 -> 2 [2d23h]\n"}, 3, 0, 3, 3},
		{"exactly 72 h is eligible", "", 2, &yayShim{out: "a 1 -> 2 [3d]\nb 1 -> 2 [72h]\nc 1 -> 2 [4320m]\nd 1 -> 2 [259200s]\ne 1 -> 2 [2d24h]\n"}, 0, 5, 0, 5},
		{"no age tag counts as held", three, 0, &yayShim{out: "old 1 -> 2\nodd 1 -> 2 [soon]\ncurseforge 1 -> 2 [3d17h]\n"}, 3, 1, 2, 4},
		{"colours in the listing", "", 2, &yayShim{out: "\x1b[1mfoo\x1b[0m 1 -> 2 \x1b[36m[5d]\x1b[0m\n"}, 0, 1, 0, 1},
		{"yay: no updates", three, 0, &yayShim{rc: 1}, 3, 0, 0, 3},
		{"none at all", "", 2, &yayShim{rc: 1}, 0, 0, 0, 0},
		{"yay missing", three, 0, nil, 3, -1, -1, 3},
		{"yay fails with a message", three, 0, &yayShim{err: "error: AUR rpc down\n", rc: 1}, 3, -1, -1, 3},
		{"yay fails other status", three, 0, &yayShim{rc: 2}, 3, -1, -1, 3},
		{"yay timed out", three, 0, &yayShim{rc: 124}, 3, -1, -1, 3},
		{"yay fails after some output", three, 0, &yayShim{out: "x 1 -> 2 [9d]\n", rc: 1}, 3, -1, -1, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := updatesRun(t, c.out, c.rc, c.yay, "")
			if err != nil {
				t.Fatalf("%s: script failed: %v", c.name, err)
			}
			var u upd
			if jerr := json.Unmarshal([]byte(raw), &u); jerr != nil || u.V != 3 || u.Count != c.wantCount || u.Repo != c.repo ||
				(c.aur < 0) != (u.Aur == nil) || (c.aur < 0) != (u.AurHeld == nil) ||
				(u.Aur != nil && (*u.Aur != c.aur || *u.AurHeld != c.held)) || time.Since(u.Checked) > time.Minute {
				t.Fatalf("%s: %q -> %+v (%v)", c.name, raw, u, jerr)
			}
			if c.aur < 0 && !strings.Contains(raw, `"aur":null,"aur_held":null`) {
				t.Fatalf("%s: aur and aur_held must be JSON nulls: %s", c.name, raw)
			}
		})
	}
	// checkupdates failing keeps the old file and fails the run, with or without yay.
	old := `{"v":2,"count":7,"repo":4,"aur":3,"aur_held":0,"checked":"2026-10-04T10:00:00+02:00"}` + "\n"
	for _, y := range []*yayShim{nil, {out: yayListing}} {
		raw, err := updatesRun(t, "", 1, y, old)
		if err == nil {
			t.Fatal("script must fail when checkupdates fails")
		}
		if raw != old {
			t.Fatalf("failed checkupdates must keep the old file, got %q", raw)
		}
	}
	if raw, _ := updatesRun(t, "", 1, nil, ""); raw != "" {
		t.Fatalf("failed checkupdates must leave no file, got %q", raw)
	}
}

func TestUpdatesTimer(t *testing.T) {
	timer := read(t, "../systemd/user/archmaid-updates-check.timer")
	for _, want := range []string{"OnStartupSec=3min\n", "OnUnitActiveSec=30min\n", "RandomizedDelaySec=5min\n"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
	// Persistent= only works with OnCalendar=; this timer is monotonic.
	if strings.Contains(timer, "Persistent") || strings.Contains(timer, "OnCalendar") {
		t.Errorf("timer must stay monotonic without Persistent=:\n%s", timer)
	}
}

// The script's cooldown is a copy of audit.Cooldown (a shell script cannot
// import it); this keeps the two equal.
func TestUpdatesScriptCooldownMatchesAudit(t *testing.T) {
	want := "COOLDOWN_HOURS=" + strconv.Itoa(int(audit.Cooldown/time.Hour)) + "\n"
	if !strings.Contains(read(t, "../systemd/archmaid-updates-check"), "\n"+want) {
		t.Fatalf("archmaid-updates-check must set %q (audit.Cooldown)", want)
	}
}

func TestManifest(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, "archmaid/plugin.json")), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "name", "description", "version", "type", "component"} {
		if s, _ := m[k].(string); s == "" {
			t.Errorf("plugin.json: %q missing", k)
		}
	}
	if m["type"] != "widget" || m["id"] != "archmaid" {
		t.Errorf("plugin.json: type/id = %v/%v", m["type"], m["id"])
	}
	if _, err := os.Stat(filepath.Join("archmaid", strings.TrimPrefix(m["component"].(string), "./"))); err != nil {
		t.Errorf("component: %v", err)
	}
	// Bar only: no control-center surface (owner decision 5).
	if q := read(t, "archmaid/ArchmaidWidget.qml"); strings.Contains(q, "ccWidget") || strings.Contains(q, "controlCenterWidget") {
		t.Error("the widget must not define a control-center tile")
	}
}

// runnableIDs: the IDs of internal/actions' menu entries that run steps or
// a discovery, comma-separated.
func runnableIDs() string {
	var ids []string
	for _, a := range actions.Menu {
		if a.Steps != nil || a.Discover != nil {
			ids = append(ids, a.ID)
		}
	}
	return strings.Join(ids, ",")
}

// TestStateJS runs tests/tst_state.qml over fixtures made by the real writers.
func TestStateJS(t *testing.T) {
	// /usr/lib/qt6/bin first: /usr/bin/qmltestrunner can be a stale build that
	// exits 1 without a word.
	runner := "/usr/lib/qt6/bin/qmltestrunner"
	if _, err := os.Stat(runner); err != nil {
		var lerr error
		if runner, lerr = exec.LookPath("qmltestrunner"); lerr != nil {
			t.Skip("qmltestrunner not installed")
		}
	}
	okRaw := lastRun(t, logstore.OutcomeOK, 0)
	fx := map[string]string{
		"t0":                t0.Format(time.RFC3339),
		"lastOK":            okRaw,
		"lastFailed":        lastRun(t, logstore.OutcomeFailed, 2),
		"lastFailed1":       lastRun(t, logstore.OutcomeFailed, 1),
		"lastAborted":       lastRun(t, logstore.OutcomeAborted, 0),
		"lastCancelled":     lastRun(t, logstore.OutcomeCancelled, 0),
		"lastNotAuthorised": withRefused(t, lastRun(t, logstore.OutcomeCancelled, 0), 1, logstore.OutcomeCancelled),
		"lastNothing":       lastRun(t, logstore.OutcomeNothingFound, 0),
		// Cancelled with 3 refused; with the field absent (an older archmaid);
		// with a damaged value; and the other outcomes carrying steps_refused.
		"lastNA3":             withRefused(t, lastRun(t, logstore.OutcomeCancelled, 0), 3, logstore.OutcomeCancelled),
		"lastCancelledAbsent": withField(t, lastRun(t, logstore.OutcomeCancelled, 0), "steps_refused", nil),
		"lastCancelledStr":    withField(t, lastRun(t, logstore.OutcomeCancelled, 0), "steps_refused", "3"),
		"lastCancelledNeg":    withField(t, lastRun(t, logstore.OutcomeCancelled, 0), "steps_refused", -1),
		"lastCancelledFrac":   withField(t, lastRun(t, logstore.OutcomeCancelled, 0), "steps_refused", 1.5),
		"lastFailedRefused":   withRefused(t, lastRun(t, logstore.OutcomeFailed, 1), 2, logstore.OutcomeFailed),
		"lastFailedRefused5":  withRefused(t, lastRun(t, logstore.OutcomeFailed, 1), 5, logstore.OutcomeFailed),
		"lastFailed2Refused1": withRefused(t, lastRun(t, logstore.OutcomeFailed, 2), 1, logstore.OutcomeFailed),
		"lastAbortedRefused":  withRefused(t, lastRun(t, logstore.OutcomeAborted, 0), 2, logstore.OutcomeAborted),
		"lastIncomplete":      withRefused(t, okRaw, 2),
		// The run report's summary object: from the real writer, with an
		// empty attention list, damaged (a string, an array, null, odd
		// members) and with too many long items.
		"lastSummary": lastRunSummary(t, logstore.RunSummary{Upgraded: 47, AUR: 6, FreedBytes: 3328599654,
			Attention: []string{"Restart recommended", "1 new .pacnew"}}),
		"lastSummaryQuiet": lastRunSummary(t, logstore.RunSummary{Upgraded: 6, AUR: 1, FreedBytes: 429916160}),
		"lastSummaryStr":   withField(t, okRaw, "summary", "47 upgraded"),
		"lastSummaryArray": withField(t, okRaw, "summary", []int{1, 2}),
		"lastSummaryNull":  strings.Replace(okRaw, `"v":1`, `"v":1,"summary":null`, 1),
		"lastSummaryOdd": withField(t, okRaw, "summary", map[string]any{"upgraded": "47", "aur": -1, "freed_bytes": 1.5,
			"attention": []any{1, nil, "", "fine", map[string]any{}}}),
		"lastSummaryMany": withField(t, okRaw, "summary", map[string]any{"upgraded": 1, "aur": 0, "freed_bytes": 0,
			"attention": []string{strings.Repeat("x", 100), "b", "c", "d", "e", "f"}}),
		"lastUnknownField": strings.Replace(okRaw, `"v":1`, `"v":1,"future":{"x":1}`, 1),
		"lastV2":           strings.Replace(okRaw, `"v":1`, `"v":2`, 1),
		"lastTorn":         okRaw[:len(okRaw)/2],
		"lastDry":          strings.Replace(okRaw, `"dry_run":false`, `"dry_run":true`, 1),
		"auditClean":       auditState(t, audit.State{V: 1, Checked: t0, Packages: 27, SnapshotPresent: true, SnapshotReadable: true}),
		"auditHigh": auditState(t, audit.State{V: 1, Checked: t0, Packages: 27, SnapshotPresent: true, SnapshotReadable: true,
			High: []audit.StateFinding{{Pkg: "foo", Reason: "maintainer alice → mallory"}, {Pkg: "bar", Reason: "adopted by x"}}}),
		"updates3":     updatesFile(t, "a\nb\nc\n", 0),
		"updatesNone":  updatesWith(t, "", &yayShim{rc: 1}),
		"updatesNoYay": updatesFile(t, "", 2),
		// repo 3 + AUR 2 eligible + 2 held; the same with yay's check failed
		// (aur null); only held AUR updates; and a schema-1 file (old installs).
		"updatesAur":     updatesWith(t, "a\nb\nc\n", &yayShim{out: yayListing}),
		"updatesNoAur":   updatesWith(t, "a\nb\nc\n", &yayShim{rc: 2}),
		"updatesAurHeld": updatesWith(t, "", &yayShim{out: "x 1 -> 2 [6h]\n"}),
		"updatesV1":      `{"v":1,"count":3,"checked":"` + time.Now().UTC().Format(time.RFC3339) + `"}` + "\n",
		"auditMany": auditState(t, audit.State{V: 1, Checked: t0, Packages: 1, SnapshotPresent: true, SnapshotReadable: true,
			High: []audit.StateFinding{{Pkg: "p1", Reason: "r"}, {Pkg: "p2", Reason: "r"}, {Pkg: "p3", Reason: "r"},
				{Pkg: "p4", Reason: "r"}, {Pkg: "p5", Reason: "r"}, {Pkg: "p6", Reason: "r"}, {Pkg: "p7", Reason: "r"}}}),
		// Every action that runs (and so can be last-run.json's action):
		// the popout must have a name for each.
		"runnable":     runnableIDs(),
		"restartTable": restartJSON(t),
	}
	if !strings.Contains(fx["auditClean"], `"high": []`) {
		t.Fatalf("audit writer changed shape: %s", fx["auditClean"])
	}
	work := t.TempDir()
	js, _ := json.Marshal(fx)
	if err := os.WriteFile(filepath.Join(work, "fixtures.js"), []byte(".pragma library\nvar fx = "+string(js)+";\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ from, to string }{{"archmaid/state.js", "state.js"}, {"tests/tst_state.qml", "tst_state.qml"}} {
		if err := os.WriteFile(filepath.Join(work, f.to), []byte(read(t, f.from)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(runner, "-input", work)
	cmd.Env = append(os.Environ(), "QT_QPA_PLATFORM=offscreen")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("qmltestrunner: %v\n%s", err, out)
	}
}
