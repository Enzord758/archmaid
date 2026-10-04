package ui

// The real Driver (internal/driver, through gui/internal/realdriver) under
// the headless harness, on the polkit path. Every privileged binary is a
// shim first on PATH, ahead of testenv's traps: pkexec speaks the root
// helper's JSON lines (granted, or refused with 127), sudo asks through
// $SUDO_ASKPASS — archmaid's wrapper, which execs this test binary in
// --askpass mode (TestMain's Relay) — and records what it got, su takes
// one password. topgrade and yay are shims too; the faillock count, the
// orphan plan and the foreign-package list are fakes. The real pkexec,
// sudo, su and helper are never reached (the traps would fail the
// package), and HOME and the XDG dirs are testenv's scratch ones.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Enzord758/archmaid/gui/internal/realdriver"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The child's view of the shims (set by the parent).
const (
	envShims    = "ARCHMAID_GUI_SHIMS"    // the shim dir (also first on PATH)
	envFaillock = "ARCHMAID_GUI_FAILLOCK" // "", "last" or "locked"
	shimPw      = "hunter2"               // what the su shim takes
)

// helperStandIn is the empty file in the shim dir that Run's install check
// finds (helpertest.UseHelperFile); nothing runs it.
const helperStandIn = "archmaid-helper"

// childTB is enough of a testing.TB for privtest.FakeSystem in the child,
// which runs its scenario from TestMain, outside any test.
type childTB struct {
	testing.TB
	cleanups []func()
}

func (*childTB) Helper()            {}
func (c *childTB) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }

var fakeTB childTB

// closeFakeSystem restores privilege's real view of the system.
func closeFakeSystem() {
	for _, f := range slices.Backward(fakeTB.cleanups) {
		f()
	}
	fakeTB.cleanups = nil
}

// startRealWiring is the child's Wiring: the real Driver with fakes for
// what reads the system, and a view of /proc in which the sudo shim (a
// shell script named sudo) is /usr/bin/sudo, so Request.Verify walks the
// real process tree (askpass client → sudo shim → topgrade shim, the
// running step) and passes it.
func startRealWiring(fail func(string, ...any)) *realdriver.Wiring {
	shims := os.Getenv(envShims)
	if shims == "" {
		fail("%s not set", envShims)
	}
	privtest.FakeSystem(&fakeTB, os.Getuid, "", func(pid int) (proc.Info, error) {
		in, err := proc.Read(pid)
		if err == nil && in.Comm == "sudo" {
			in.Exe = "/usr/bin/sudo"
		}
		return in, err
	})
	// The helper isn't installed here: Run's install check stats installShims'
	// stand-in instead (pkexec still names helperclient.HelperPath).
	helpertest.UseHelperFile(&fakeTB, filepath.Join(shims, helperStandIn))
	direct, cascade := []string{"oldlib"}, []string{"oldlib 1.0-1"}
	lock := engine.FaillockRead{Deny: 3}
	switch os.Getenv(envFaillock) {
	case "last":
		lock.Failures = 2
	case "locked":
		lock.Failures, lock.Unlock = 3, time.Now().Add(10*time.Minute)
	}
	var now func() time.Time
	if os.Getenv("ARCHMAID_GUI_CHILD") == "real-runs" {
		now = runsClock()
	}
	grace := 100 * time.Millisecond // proc.Child.Stop's SIGKILL timer outlives Close by this much
	if os.Getenv("ARCHMAID_GUI_CHILD") == "real-sigint-twice" {
		// Far past the exit: only the second signal's KillNow can end
		// what the step left running.
		grace = 30 * time.Second
	}
	return realdriver.Start(realdriver.Config{
		Now:        now,
		StateDir:   os.Getenv("ARCHMAID_GUI_STATE"),
		RuntimeDir: shims,
		Faillock:   func() engine.FaillockRead { return lock },
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) {
			return steps.OrphanPlan{Hash: helperclient.PlanHash(direct, cascade), Direct: direct, Cascade: cascade}, nil
		},
		Foreign:   func(context.Context) ([]string, error) { return nil, nil },
		LookPath:  func(string) (string, error) { return "", errors.New("not found") },
		KillGrace: grace,
		WaitDelay: 500 * time.Millisecond,
		Report:    func(err error) { fail("driver report: %v", err) },
	})
}

// realRuns is how many real runs real-runs makes in one window: one more
// than the history keeps.
const realRuns = logstore.KeepRuns + 1

// runsStart is real-runs' clock's start; runsClock steps a minute per
// read, so each Driver (one per Start) stamps its own run directory.
var runsStart = time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local)

func runsClock() func() time.Time {
	var n atomic.Int64
	return func() time.Time { return runsStart.Add(time.Duration(n.Add(1)) * time.Minute) }
}

// childStarted: for the scenarios that stop a hanging topgrade (real-close,
// real-close-topgrade, real-sigint, real-sighup), the topgrade shim has
// written its pid and its leftover's (so the parent can check both are
// gone); true for every other scenario.
func childStarted(scenario string) bool {
	switch scenario {
	case "real-close", "real-close-topgrade", "real-sigint", "real-sighup", "real-sigint-twice":
	default:
		return true
	}
	_, err := os.Stat(filepath.Join(os.Getenv(envShims), "topgrade.bg"))
	return err == nil
}

// shimOpts: refuse are the helper verbs pkexec refuses (exit 127, as for a
// dismissed dialog); topgrade is what the topgrade shim does after its
// first line: "" exits 0, "sudo" runs sudo, "hang" sleeps; yay "sudo" has
// the yay shim run `sudo pacman -Sc`, as yay -Sc does.
type shimOpts struct {
	refuse   []string
	topgrade string
	yay      string
}

// installShims writes the shims into a fresh dir and returns it, with the
// child's environment (PATH first).
func installShims(t *testing.T, o shimOpts) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	pkexec := `#!/bin/sh
printf '%s\n' "$*" >> ` + q(dir+"/pkexec.log") + `
[ "$1" = ` + helperclient.HelperPath + ` ] || exit 126
verb=$2
case " ` + strings.Join(o.refuse, " ") + ` " in *" $verb "*) exit 127;; esac
sleep 0.3
printf '{"v":1,"t":"start","verb":"%s","version":"0.1.0"}\n' "$verb"
if [ "$verb" = full-cleanup ]; then
	parts=
	case " $* " in *" --orphans "*) parts=remove-orphans;; esac
	parts="$parts paccache"
	case " $* " in *" --wipe "*) parts="$parts paccache-wipe";; esac
	parts="$parts clean-tmp vacuum-journal check-files"
	of=$(echo $parts | wc -w) n=0
	for p in $parts; do
		n=$((n+1))
		printf '{"v":1,"t":"progress","step":%d,"of":%d,"label":"shim %s","part":"%s"}\n' $n $of "$p" "$p"
		printf '{"v":1,"t":"line","stream":"out","text":"shim %s"}\n' "$p"
		printf '{"v":1,"t":"done","step":%d,"of":%d,"part":"%s","ok":true,"code":0}\n' $n $of "$p"
	done
	printf '{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}\n'
	exit 0
fi
printf '{"v":1,"t":"line","stream":"out","text":"shim %s"}\n' "$verb"
[ "$verb" = check-files ] && printf '{"v":1,"t":"check","checked":["linux"],"broken":[]}\n'
printf '{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}\n'
`
	// The real sudo prefers a terminal to SUDO_ASKPASS: with a controlling
	// terminal it would prompt there, where a GUI's user isn't looking.
	// So the shim refuses to run with one at all.
	sudo := `#!/bin/sh
printf '%s\n' "$*" >> ` + q(dir+"/sudo.log") + `
if [ -t 0 ] || sh -c ': </dev/tty' 2>/dev/null; then
	echo "sudo: would prompt on the terminal" >&2
	echo tty >> ` + q(dir+"/sudo.tty") + `
	exit 1
fi
[ -n "$SUDO_ASKPASS" ] || { echo "sudo: a password is required" >&2; exit 1; }
if pw=$("$SUDO_ASKPASS" "[sudo] password for shim: "); then
	printf '%s\n' "$pw" >> ` + q(dir+"/sudo.got") + `
	echo "sudo shim ran: $*"
else
	echo "sudo: no password was provided" >&2
	exit 1
fi
`
	su := "#!/bin/sh\nread pw\n[ \"$pw\" = " + shimPw + " ]\n"
	topgrade := "#!/bin/sh\necho $$ > " + q(dir+"/topgrade.pid") + "\necho 'topgrade shim: updating'\n"
	switch o.topgrade {
	case "sudo":
		topgrade += "sudo pacman -Syu || exit 1\n"
	case "hang":
		// It leaves a sleep behind that ignores SIGINT, as a tool's
		// background helper might: stopping the step must take it too.
		topgrade += "(trap '' INT; exec sleep 30) >/dev/null 2>&1 &\necho $! > " + q(dir+"/topgrade.bg") + "\nexec sleep 30\n"
	}
	yay := "#!/bin/sh\necho $$ > " + q(dir+"/yay.pid") + "\n"
	if o.yay == "sudo" {
		yay += "sudo pacman -Sc || exit 1\n"
	}
	yay += "echo 'yay shim: cache cleaned'\n"
	for name, body := range map[string]string{"pkexec": pkexec, "sudo": sudo, "su": su, "topgrade": topgrade, "yay": yay} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, helperStandIn), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, []string{envShims + "=" + dir, "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func hasRow(subs []string, want string) bool {
	return slices.ContainsFunc(subs, func(s string) bool { return strings.Contains(s, want) })
}

// TestGUIRealDriver runs the real Driver under the harness:
//
//   - real-dry: Full Cleanup as a dry run (the Settings page's, from
//     gui.json): completed, nothing started, no pkexec;
//   - real-refused: Full Cleanup's ONE polkit dialog (the root steps'
//     full-cleanup call) is refused (pkexec 127) after "Waiting for
//     authorisation…" (with the warning: one more failure locks the
//     account): every root row reads "Not authorised" and the run goes on
//     with yay -Sc; real-runs has the granted call;
//   - real-locked: a locked account: no helper step starts (no pkexec at
//     all), each reads "Not authorised · account locked until …";
//   - real-askpass, real-askpass-cancel: topgrade's sudo asks through the
//     relay; the right password reaches the sudo shim, a cancel doesn't;
//   - real-close: the window closed mid-topgrade: the child is stopped and
//     nothing outlives the window (goleak, the topgrade shim's pid);
//   - close-topgrade: Run topgrade, then the window closed mid-topgrade
//     with Stop and Quit: last-run.json says topgrade was aborted, and the
//     run's folder holds what it logged (the audit).
func TestGUIRealDriver(t *testing.T) {
	needDisplay(t)

	t.Run("dry", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{})
		cfg := t.TempDir()
		if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{DryRun: true}); err != nil {
			t.Fatal(err)
		}
		rep, _ := child(t, "real-dry", append(env, "XDG_CONFIG_HOME="+cfg)...)
		if rep.Outcome != "completed" || !slices.Equal(rep.Sent, []string{"confirm 1 true", "confirm 2 true", "confirm 3 true"}) {
			t.Errorf("outcome %q sent %q", rep.Outcome, rep.Sent)
		}
		for _, r := range rep.Rows {
			if !strings.HasSuffix(r, "=ok") {
				t.Errorf("row %s", r)
			}
		}
		if rep.Banner != "" || rep.Title != "archmaid" || strings.Contains(rep.FirstStatus, "STUB") {
			t.Errorf("the real Driver's window reads as the stub: banner %q title %q status %q", rep.Banner, rep.Title, rep.FirstStatus)
		}
		if !slices.Contains(rep.Rows, steps.IDOrphansRemove+"=ok") || !slices.Equal(rep.DriverOpts, []string{`dry=true downloads=""`, `dry=true downloads=""`}) {
			t.Errorf("rows %q drivers %q", rep.Rows, rep.DriverOpts)
		}
		for _, f := range []string{"pkexec.log", "sudo.log", "topgrade.pid"} {
			if l := readLines(t, filepath.Join(dir, f)); l != nil {
				t.Errorf("a dry run left %s: %q", f, l)
			}
		}
	})

	t.Run("refused", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{refuse: []string{steps.VerbFullCleanup}})
		rep, _ := child(t, "real-refused", append(env, envFaillock+"=last")...)
		if rep.Outcome != "completed" {
			t.Errorf("outcome %q rows %q", rep.Outcome, rep.Rows)
		}
		want := map[string]string{
			steps.IDAURAudit: "ok", steps.IDTopgrade: "ok", steps.IDOrphansProbe: "ok",
			steps.IDOrphansRemove: "auth refused", steps.IDPaccache: "auth refused", steps.IDPaccacheWipe: "auth refused",
			steps.IDTmpfiles: "auth refused", steps.IDJournal: "auth refused", steps.IDIntegrity: "auth refused",
			steps.IDYayCache: "ok",
		}
		for id, st := range want {
			if !slices.Contains(rep.Rows, id+"="+st) {
				t.Errorf("rows %q: want %s=%s", rep.Rows, id, st)
			}
		}
		if !hasRow(rep.Subtitles, "Cleaning package cache…: Not authorised") {
			t.Errorf("the refused row never read Not authorised: %q", rep.Subtitles)
		}
		if !hasRow(rep.Subtitles, ": Waiting for authorisation… · one more failure locks your account") {
			t.Errorf("no row waited for authorisation with the warning: %q", rep.Subtitles)
		}
		if !slices.Contains(rep.Toasts, "One more failed authorisation locks your account") {
			t.Errorf("toasts %q", rep.Toasts)
		}
		calls := readLines(t, filepath.Join(dir, "pkexec.log"))
		var verbs []string
		for _, c := range calls {
			if f := strings.Fields(c); len(f) >= 2 {
				verbs = append(verbs, f[1])
			}
		}
		if wantVerbs := []string{steps.VerbFullCleanup}; !slices.Equal(verbs, wantVerbs) {
			t.Errorf("pkexec ran %q, want one dialog: %q", calls, wantVerbs)
		}
	})

	t.Run("locked", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{})
		rep, _ := child(t, "real-locked", append(env, envFaillock+"=locked")...)
		if rep.Outcome != "completed" || !slices.Contains(rep.Rows, steps.IDPaccache+"=auth refused") || !slices.Contains(rep.Rows, steps.IDIntegrity+"=auth refused") {
			t.Errorf("outcome %q rows %q", rep.Outcome, rep.Rows)
		}
		// One faillock read for the one call: the first root row carries
		// the lock's detail, the rest read Not authorised.
		if !hasRow(rep.Subtitles, "Removing orphaned packages…: Not authorised · account locked until ") ||
			!hasRow(rep.Subtitles, "Cleaning package cache…: Not authorised") {
			t.Errorf("subtitles %q", rep.Subtitles)
		}
		if hasRow(rep.Subtitles, "Waiting for authorisation") {
			t.Errorf("a locked account still waited for a dialog: %q", rep.Subtitles)
		}
		if l := readLines(t, filepath.Join(dir, "pkexec.log")); l != nil {
			t.Errorf("pkexec ran on a locked account: %q", l)
		}
	})

	t.Run("askpass", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{topgrade: "sudo"})
		rep, _ := child(t, "real-askpass", env...)
		if rep.Outcome != "completed" || len(rep.Sent) != 1 || !strings.HasSuffix(rep.Sent[0], fmt.Sprintf("len %d", len(shimPw))) {
			t.Errorf("outcome %q sent %q rows %q", rep.Outcome, rep.Sent, rep.Rows)
		}
		pid := readLines(t, filepath.Join(dir, "topgrade.pid"))
		if len(rep.PwBodies) != 1 || len(pid) != 1 || !strings.Contains(rep.PwBodies[0], "(pid "+pid[0]+")") || !strings.Contains(rep.PwBodies[0], "[sudo] password for shim:") {
			t.Errorf("askpass dialog said %q; want the topgrade shim (pid %q) and sudo's prompt", rep.PwBodies, pid)
		}
		if got := readLines(t, filepath.Join(dir, "sudo.got")); !slices.Equal(got, []string{shimPw}) {
			t.Errorf("sudo got %q", got)
		}
	})

	t.Run("askpass-cancel", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{topgrade: "sudo"})
		rep, _ := child(t, "real-askpass-cancel", env...)
		if len(rep.Sent) != 1 || !strings.HasPrefix(rep.Sent[0], "cancel ") || !slices.Contains(rep.Rows, steps.IDTopgrade+"=failed") {
			t.Errorf("outcome %q sent %q rows %q", rep.Outcome, rep.Sent, rep.Rows)
		}
		if got := readLines(t, filepath.Join(dir, "sudo.got")); got != nil {
			t.Errorf("sudo got %q after a cancel", got)
		}
	})

	t.Run("runs", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{})
		state, cfg := t.TempDir(), t.TempDir()
		rep, _ := child(t, "real-runs", append(env, "ARCHMAID_GUI_STATE="+state, "XDG_CONFIG_HOME="+cfg)...)
		if len(rep.Outcomes) != realRuns+1 || slices.ContainsFunc(rep.Outcomes, func(o string) bool { return o != "completed" }) {
			t.Fatalf("outcomes %q, want %d completed runs", rep.Outcomes, realRuns+1)
		}
		// The clock's reads: wiring.Options, the window's first Driver,
		// then a fresh Driver per Start: realRuns real ones, the dry one.
		if len(rep.DriverOpts) != realRuns+2 || rep.DriverOpts[realRuns] != `dry=false downloads=""` || rep.DriverOpts[realRuns+1] != `dry=true downloads=""` {
			t.Errorf("drivers %q: want a fresh one per Start, the last dry", rep.DriverOpts)
		}
		if n := int(rep.DriverCloses); n != len(rep.DriverOpts) {
			t.Errorf("%d Drivers made, %d closed", len(rep.DriverOpts), n)
		}
		var want []string
		for i := 2; i <= realRuns; i++ { // the oldest real run is rotated out
			want = append(want, runsStart.Add(time.Duration(i+2)*time.Minute).Format("20060102-1504"))
		}
		want = append(want, "dryrun")
		var got []string
		ents, err := os.ReadDir(state)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.IsDir() {
				got = append(got, e.Name())
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("run dirs %q, want %q: one per real run, the %d newest kept, and the dryrun slot", got, want, logstore.KeepRuns)
		}
		for _, d := range want {
			if _, err := os.Stat(filepath.Join(state, d, "topgrade.log")); err != nil {
				t.Errorf("run dir %s: %v", d, err)
			}
		}
		r, err := logstore.New(logstore.Options{Dir: state}).ReadLastRun()
		if err != nil || r.Action != "topgrade" || r.DryRun {
			t.Errorf("the root's last-run.json: %+v, %v; want the last real run's", r, err)
		}
		if l := readLines(t, filepath.Join(dir, "pkexec.log")); l != nil {
			t.Errorf("pkexec ran: %q", l)
		}
	})

	t.Run("close", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{topgrade: "hang"})
		rep, _ := child(t, "real-close", env...)
		if rep.Outcome != "" {
			t.Errorf("the run finished (%q) before the window closed", rep.Outcome)
		}
		pid := readLines(t, filepath.Join(dir, "topgrade.pid"))
		if len(pid) != 1 {
			t.Fatalf("topgrade shim pid %q: it never started", pid)
		}
		n, _ := strconv.Atoi(pid[0])
		if err := syscall.Kill(n, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("the topgrade shim (pid %d) outlived the window: %v", n, err)
		}
	})

	t.Run("tty", func(t *testing.T) {
		// The GUI started from a shell: its process has a controlling
		// terminal. topgrade's sudo and yay -Sc's must still ask through
		// the GUI's askpass dialog, never on that terminal (the sudo shim
		// fails if it has one).
		dir, env := installShims(t, shimOpts{topgrade: "sudo", yay: "sudo"})
		rep, _ := childCmd(t, "real-tty", withTerminal(t), env...)
		if tty := readLines(t, filepath.Join(dir, "sudo.tty")); tty != nil {
			t.Errorf("sudo ran with a controlling terminal %d times: it would have prompted there", len(tty))
		}
		if rep.Outcome != "completed" || !slices.Contains(rep.Rows, steps.IDTopgrade+"=ok") || !slices.Contains(rep.Rows, steps.IDYayCache+"=ok") {
			t.Errorf("outcome %q rows %q", rep.Outcome, rep.Rows)
		}
		tg, yay := readLines(t, filepath.Join(dir, "topgrade.pid")), readLines(t, filepath.Join(dir, "yay.pid"))
		if len(rep.PwBodies) != 2 || len(tg) != 1 || len(yay) != 1 ||
			!strings.Contains(rep.PwBodies[0], "(pid "+tg[0]+")") || !strings.Contains(rep.PwBodies[1], "(pid "+yay[0]+")") {
			t.Errorf("askpass dialogs %q; want one for the topgrade shim (pid %q), then one for the yay shim (pid %q)", rep.PwBodies, tg, yay)
		}
		if got := readLines(t, filepath.Join(dir, "sudo.got")); !slices.Equal(got, []string{shimPw, shimPw}) {
			t.Errorf("sudo got %q, want the password twice", got)
		}
	})

	// Run topgrade, then end the GUI mid-topgrade: with Stop and Quit, or
	// with SIGINT (ctrl+c in the terminal it was started from) or SIGHUP
	// (that terminal closing), which are Stop and Quit too. Each time
	// last-run.json says topgrade was aborted, the run's folder holds what
	// it logged (the audit), and neither the topgrade shim nor what it left
	// running (a sleep that ignores SIGINT) outlives the GUI.
	for _, tc := range []struct{ name, scenario string }{
		{"close-topgrade", "real-close-topgrade"},
		{"sigint", "real-sigint"},
		{"sighup", "real-sighup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, env := installShims(t, shimOpts{topgrade: "hang"})
			state := t.TempDir()
			rep, _ := child(t, tc.scenario, append(env, "ARCHMAID_GUI_STATE="+state)...)
			if rep.Outcome != "" {
				t.Errorf("the run finished (%q) before the GUI was ended", rep.Outcome)
			}
			pids := append(readLines(t, filepath.Join(dir, "topgrade.pid")), readLines(t, filepath.Join(dir, "topgrade.bg"))...)
			if len(pids) != 2 {
				t.Fatalf("topgrade shim and leftover pids %q: they never started", pids)
			}
			for _, p := range pids {
				if n, _ := strconv.Atoi(p); !ended(n) {
					t.Errorf("pid %d (topgrade shim or its leftover) outlived the GUI", n)
				}
			}
			r, err := logstore.New(logstore.Options{Dir: state}).ReadLastRun()
			if err != nil || r.Action != actions.IDTopgrade || r.Outcome != "aborted" || r.DryRun {
				t.Errorf("last-run.json: %+v, %v; want topgrade aborted", r, err)
			}
			var runs []string
			ents, err := os.ReadDir(state)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range ents {
				if e.IsDir() {
					runs = append(runs, e.Name())
				}
			}
			if len(runs) != 1 {
				t.Errorf("run folders %q, want the aborted run's", runs)
			}
			t.Logf("last-run.json: %+v; run folders %q", r, runs)
		})
	}
}

// A second SIGINT while the first one's Stop and Quit is still stopping
// topgrade exits at once (130, no report: the window never finished
// closing), but only after the aborted record is written, and with the
// step's whole process group SIGKILLed: nothing it started outlives the
// GUI.
func TestGUISignalTwice(t *testing.T) {
	needDisplay(t)
	dir, env := installShims(t, shimOpts{topgrade: "hang"})
	state := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), "ARCHMAID_GUI_CHILD=real-sigint-twice",
		"ARCHMAID_GUI_REPORT="+filepath.Join(t.TempDir(), "report.json"), "ARCHMAID_GUI_STATE="+state), env...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child still running after 60s")
	}
	// The GUI exited long before Stop's grace (30 s here) ran out: the
	// topgrade shim and its leftover (it ignores SIGINT) must be gone all
	// the same, SIGKILLed before the exit. (Should one still run, it is
	// killed once checked, not to help the check.)
	pids := append(readLines(t, filepath.Join(dir, "topgrade.pid")), readLines(t, filepath.Join(dir, "topgrade.bg"))...)
	if len(pids) != 2 {
		t.Fatalf("topgrade shim and leftover pids %q: they never started", pids)
	}
	deadline := time.Now().Add(time.Second) // SIGKILL sent: only its delivery may lag
	for _, p := range pids {
		n, _ := strconv.Atoi(p)
		for !ended(n) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !ended(n) {
			t.Errorf("pid %d (topgrade shim or its leftover) outlived the GUI", n)
			_ = syscall.Kill(n, syscall.SIGKILL)
		}
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128+int(syscall.SIGINT) {
		t.Fatalf("child: %v; want exit status %d (the second SIGINT's, at once)", err, 128+int(syscall.SIGINT))
	}
	r, err := logstore.New(logstore.Options{Dir: state}).ReadLastRun()
	if err != nil || r.Action != actions.IDTopgrade || r.Outcome != "aborted" {
		t.Errorf("last-run.json: %+v, %v; want topgrade aborted, written before the exit", r, err)
	}
}

// ended: pid is gone, or a zombie (its parent, the test binary as
// subreaper, hasn't reaped it).
func ended(pid int) bool {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return true
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	return len(f) > 0 && f[0] == "Z"
}

// withTerminal gives the child a controlling terminal, as archmaid-gui
// started from a shell has: a session of its own whose terminal is a fresh
// pty's (its stdin), the other end drained and closed when t ends.
func withTerminal(t *testing.T) func(*exec.Cmd) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open a pty: %v", err)
	}
	ioctl := func(req uintptr, arg unsafe.Pointer) {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), req, uintptr(arg)); errno != 0 {
			t.Fatalf("pty ioctl %#x: %v", req, errno)
		}
	}
	var unlock int32
	ioctl(syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	var n uint32
	ioctl(syscall.TIOCGPTN, unsafe.Pointer(&n))
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open the pty's terminal: %v", err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, master) // ends with EIO once the child is gone
	}()
	t.Cleanup(func() {
		slave.Close()
		master.Close()
		<-drained
	})
	return func(cmd *exec.Cmd) {
		cmd.Stdin = slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	}
}
