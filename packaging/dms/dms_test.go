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
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/logstore"
)

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

// withRefused turns a real record into the "incomplete" shape (outcome
// "incomplete" plus steps_refused) that the engine is gaining; the Go
// RunRecord does not carry those yet, so the two fields are set on the
// writer's own JSON.
func withRefused(t *testing.T, raw string, refused int) string {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	m["outcome"] = "incomplete"
	m["steps_refused"] = refused
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

// updatesFile runs archmaid-updates-check with a fake checkupdates that prints
// out and exits rc, in a scratch state dir. It returns the file written, or
// "" when none was.
func updatesFile(t *testing.T, out string, rc int) string {
	t.Helper()
	bin := t.TempDir()
	fake := "#!/bin/sh\nprintf '%s' '" + out + "'\nexit " + string(rune('0'+rc)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "checkupdates"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	cmd := exec.Command("/bin/sh", "../systemd/archmaid-updates-check")
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=/nonexistent", "XDG_STATE_HOME=" + state}
	err := cmd.Run()
	p := filepath.Join(state, "archmaid", "updates-cache.json")
	if rc == 1 {
		if err == nil {
			t.Fatal("script must fail when checkupdates fails")
		}
	} else if err != nil {
		t.Fatalf("script: %v", err)
	}
	b, rerr := os.ReadFile(p)
	if rerr != nil {
		return ""
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s mode = %v, want 0600", p, fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %v, want 0700", fi.Mode())
	}
	return string(b)
}

func TestUpdatesScript(t *testing.T) {
	var u struct {
		V       int
		Count   int
		Checked time.Time
	}
	for _, c := range []struct {
		name string
		out  string
		rc   int
		want int
	}{
		{"three pending", "a 1-1 -> 1-2\nb 1 -> 2\nc 3 -> 4\n", 0, 3},
		{"none pending", "", 2, 0},
	} {
		raw := updatesFile(t, c.out, c.rc)
		if err := json.Unmarshal([]byte(raw), &u); err != nil || u.V != 1 || u.Count != c.want || time.Since(u.Checked) > time.Minute {
			t.Fatalf("%s: %q -> %+v (%v)", c.name, raw, u, err)
		}
	}
	if raw := updatesFile(t, "", 1); raw != "" {
		t.Fatalf("failed checkupdates must leave no file, got %q", raw)
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
		"t0":               t0.Format(time.RFC3339),
		"lastOK":           okRaw,
		"lastFailed":       lastRun(t, logstore.OutcomeFailed, 2),
		"lastFailed1":      lastRun(t, logstore.OutcomeFailed, 1),
		"lastAborted":      lastRun(t, logstore.OutcomeAborted, 0),
		"lastCancelled":    lastRun(t, logstore.OutcomeCancelled, 0),
		"lastIncomplete":   withRefused(t, okRaw, 2),
		"lastUnknownField": strings.Replace(okRaw, `"v":1`, `"v":1,"future":{"x":1}`, 1),
		"lastV2":           strings.Replace(okRaw, `"v":1`, `"v":2`, 1),
		"lastTorn":         okRaw[:len(okRaw)/2],
		"lastDry":          strings.Replace(okRaw, `"dry_run":false`, `"dry_run":true`, 1),
		"auditClean":       auditState(t, audit.State{V: 1, Checked: t0, Packages: 27, SnapshotPresent: true, SnapshotReadable: true}),
		"auditHigh": auditState(t, audit.State{V: 1, Checked: t0, Packages: 27, SnapshotPresent: true, SnapshotReadable: true,
			High: []audit.StateFinding{{Pkg: "foo", Reason: "maintainer alice → mallory"}, {Pkg: "bar", Reason: "adopted by x"}}}),
		"updates3":    updatesFile(t, "a\nb\nc\n", 0),
		"updatesNone": updatesFile(t, "", 2),
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
