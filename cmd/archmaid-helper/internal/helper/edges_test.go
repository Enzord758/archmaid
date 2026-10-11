package helper

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The helper's error paths: a command that can't run, a lock or pacman
// check that can't be made, input it can't read. Each fails closed (exit
// 1, 4 or 5, or the package skipped), and none runs the root command it
// was guarding.

var errNoRun = errors.New("fork/exec: resource temporarily unavailable")

// ranNothing fails if any root command (a Stream) ran.
func ranNothing(t *testing.T, f *fakeRunner) {
	t.Helper()
	if len(f.streams) != 0 {
		t.Fatalf("ran %q", join(f.streams))
	}
}

// resultError is the result line's error text.
func resultError(t *testing.T, out *bytes.Buffer) string {
	t.Helper()
	msg, _ := result(t, out)["error"].(string)
	return msg
}

// ---- the install check's seams ---------------------------------------------

// TestLstatFile: the real FileStat source reports the owner and mode of
// the path itself, never of what a symlink points to (CheckInstall must
// see a symlinked install as a symlink), and fails for a missing path.
func TestLstatFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bin")
	if err := os.WriteFile(file, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o640); err != nil { // past the umask
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	st, err := LstatFile(file)
	if err != nil || st.Uid != uint32(os.Getuid()) || st.Mode != 0o640 {
		t.Fatalf("LstatFile(file) = %+v, %v; want uid %d, mode 0640", st, err, os.Getuid())
	}
	if st, err := LstatFile(link); err != nil || st.Mode&fs.ModeSymlink == 0 {
		t.Fatalf("LstatFile(link) = %+v, %v; want the symlink itself", st, err)
	}
	if _, err := LstatFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LstatFile(missing) err = %v", err)
	}
}

// TestDefaultOptionsAreTheRealThing: what main runs with is the real
// runner, the real own-path and lstat (not a stat that follows symlinks),
// stdout/stderr and the fixed lock paths.
func TestDefaultOptionsAreTheRealThing(t *testing.T) {
	o := DefaultOptions()
	fn := func(f any) uintptr { return reflect.ValueOf(f).Pointer() }
	switch {
	case o.Runner != (ExecRunner{}):
		t.Errorf("Runner = %#v", o.Runner)
	case o.Stdout != os.Stdout || o.Stderr != os.Stderr:
		t.Error("Stdout/Stderr are not the process's")
	case o.Exe == nil || fn(o.Exe) != fn(os.Executable):
		t.Error("Exe is not os.Executable")
	case o.Stat == nil || fn(o.Stat) != fn(LstatFile):
		t.Error("Stat is not LstatFile")
	case o.LockPath != DefaultLockPath || o.PacmanDBLock != DefaultPacmanDBLock:
		t.Errorf("lock paths %q, %q", o.LockPath, o.PacmanDBLock)
	case o.Dev || o.CallerUID != "":
		t.Errorf("main's fields set: %+v", o)
	}
}

// TestOwnPathUnknown: a helper that can't tell its own path is refused
// (exit 4) before anything runs, whether the verb needs root or is run as
// root anyway.
func TestOwnPathUnknown(t *testing.T) {
	for _, verb := range [][]string{{"check-files"}, {"plan-orphans"}} {
		f := newFake()
		o, out := rootOpts(t, f)
		o.Exe = func() (string, error) { return "", errors.New("readlink /proc/self/exe: no such file") }
		if code := Run(verb, o); code != ExitRefused {
			t.Fatalf("%s: exit %d, want 4:\n%s", verb[0], code, out)
		}
		if len(f.queries) != 0 {
			t.Fatalf("%s: queried %q", verb[0], join(f.queries))
		}
		ranNothing(t, f)
		if msg := resultError(t, out); !strings.Contains(msg, "cannot determine own path") {
			t.Errorf("%s: error %q", verb[0], msg)
		}
	}
}

// ---- Run's own guards ------------------------------------------------------

// TestRunWithoutSeams: no stdout still ends in a result (to nowhere), and
// no runner refuses a known verb (exit 1) instead of panicking.
func TestRunWithoutSeams(t *testing.T) {
	o, _ := rootOpts(t, newFake())
	o.Stdout = nil
	if code := Run([]string{"clean-tmp"}, o); code != ExitOK {
		t.Fatalf("no stdout: exit %d", code)
	}
	o, out := rootOpts(t, nil)
	o.Runner = nil
	if code := Run([]string{"clean-tmp"}, o); code != ExitFailed {
		t.Fatalf("no runner: exit %d, want 1:\n%s", code, out)
	}
	if msg := resultError(t, out); msg != "no runner configured" {
		t.Errorf("error %q", msg)
	}
}

// ---- locks -----------------------------------------------------------------

// TestLockUnusable: a lock the helper can't take, for a reason other than
// another helper holding it, fails the verb (exit 1, not "busy") before
// any root command, for every verb that locks; a symlink at the lock path
// is never followed (the target is left alone).
func TestLockUnusable(t *testing.T) {
	verbs := map[string][]string{
		"check-files":       {"check-files"},
		"check-files names": {"check-files", "a"},
		"paccache":          {"paccache", "3"},
		"remove-orphans":    {"remove-orphans", hash0},
		"repair":            {"repair", "a"},
		"disable-unit":      {"disable-unit", "a.service"},
		"full-cleanup":      {"full-cleanup", "--keep", "3", "--journal", "2weeks"},
	}
	locks := map[string]func(t *testing.T, o *Options) (check func()){
		"none configured": func(t *testing.T, o *Options) func() { o.LockPath = ""; return func() {} },
		"dir missing": func(t *testing.T, o *Options) func() {
			o.LockPath = filepath.Join(t.TempDir(), "gone", "helper.lock")
			return func() {}
		},
		"a symlink": func(t *testing.T, o *Options) func() {
			target := filepath.Join(t.TempDir(), "precious")
			if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, o.LockPath); err != nil {
				t.Fatal(err)
			}
			return func() {
				if b, err := os.ReadFile(target); err != nil || string(b) != "x" {
					t.Errorf("the symlink's target changed: %q, %v", b, err)
				}
			}
		},
	}
	for lname, lock := range locks {
		for vname, args := range verbs {
			t.Run(lname+"/"+vname, func(t *testing.T) {
				f := newFake()
				o, out := rootOpts(t, f)
				check := lock(t, &o)
				if code := Run(args, o); code != ExitFailed {
					t.Fatalf("exit %d, want 1:\n%s", code, out)
				}
				want := "open lock " + o.LockPath + ": "
				if lname == "none configured" {
					want = "no lock path configured"
				}
				if msg := resultError(t, out); !strings.HasPrefix(msg, want) {
					t.Errorf("error %q, want %q…", msg, want)
				}
				if len(f.queries) != 0 {
					t.Errorf("queried %q", join(f.queries))
				}
				ranNothing(t, f)
				check()
			})
		}
	}
}

// TestPacmanLockUncheckable: when pacman's lock can't be checked (its
// path runs through a file), pacman counts as running: exit 5, nothing
// run. removeOrphans checks it itself, not relying on the verb's lock
// check: called on its own while pacman's lock exists, it removes nothing
// and asks pacman nothing.
func TestPacmanLockUncheckable(t *testing.T) {
	f := newFake()
	o, out := rootOpts(t, f)
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	o.PacmanDBLock = filepath.Join(notDir, "db.lck")
	if code := Run([]string{"paccache", "3"}, o); code != ExitBusy {
		t.Fatalf("exit %d, want 5:\n%s", code, out)
	}
	ranNothing(t, f)
	if msg := resultError(t, out); !strings.Contains(msg, "cannot check") {
		t.Errorf("error %q", msg)
	}

	f = orphanFake("a\n", "a 1-1\n")
	o, out = rootOpts(t, f)
	if err := os.WriteFile(o.PacmanDBLock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &session{o: o, em: NewEmitter(out)}
	code, msg := s.removeOrphans(1, 1, PlanHash([]string{"a"}, []string{"a 1-1"}))
	if code != ExitBusy || !strings.Contains(msg, "orphans were not removed") {
		t.Fatalf("removeOrphans = %d, %q; want 5", code, msg)
	}
	if len(f.queries)+len(f.streams) != 0 {
		t.Fatalf("ran %q %q while pacman was running", join(f.queries), join(f.streams))
	}
	if len(s.skipped) != 1 || s.skipped[0].Reason != "pacman is running" {
		t.Errorf("skipped %+v", s.skipped)
	}
}

// ---- commands that can't run -----------------------------------------------

// TestRootCommandCantStart: a root command that can't be started fails the
// verb (exit 1) naming the step, for the single-command verbs, the orphan
// removal, the repair and the unit disable.
func TestRootCommandCantStart(t *testing.T) {
	h := PlanHash([]string{"a"}, []string{"a 1-1"})
	cases := map[string]struct {
		f    *fakeRunner
		args []string
		want string
	}{
		"clean-tmp":      {newFake().on(cleanTmpArgv(), reply{fail: errNoRun}), []string{"clean-tmp"}, "Cleaning temporary files: fork/exec"},
		"remove-orphans": {orphanFake("a\n", "a 1-1\n").on(removeArgv([]string{"a"}), reply{fail: errNoRun}), []string{"remove-orphans", h}, "Removing 1 orphaned package(s): fork/exec"},
		"repair": {
			onPlan(onPkg(newFake(), "a", "1-1", "1-1", 2), []string{"a"}, "a 1-1").on(repairArgv([]string{"a"}), reply{fail: errNoRun}),
			[]string{"repair", "a"}, "Reinstalling 1 package(s): fork/exec",
		},
		"disable-unit": {
			unitFake(map[string]string{"a.service": idle("a.service")}).on(disableArgv([]string{"a.service"}), reply{fail: errNoRun}),
			[]string{"disable-unit", "a.service"}, "Disabling 1 service(s): fork/exec",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o, out := rootOpts(t, c.f)
			if code := Run(c.args, o); code != ExitFailed {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if msg := resultError(t, out); !strings.HasPrefix(msg, c.want) {
				t.Errorf("error %q, want %q…", msg, c.want)
			}
			if res := result(t, out); res["child_exit"] != nil {
				t.Errorf("child_exit %v for a command that never ran", res["child_exit"])
			}
		})
	}
}

// TestRootCommandFails: a root command that runs and fails is exit 1 for
// the orphan removal, the repair and the unit disable, as for the others.
func TestRootCommandFails(t *testing.T) {
	h := PlanHash([]string{"a"}, []string{"a 1-1"})
	cases := map[string]struct {
		f    *fakeRunner
		args []string
	}{
		"remove-orphans": {orphanFake("a\n", "a 1-1\n").on(removeArgv([]string{"a"}), reply{exit: 1}), []string{"remove-orphans", h}},
		"repair":         {onPlan(onPkg(newFake(), "a", "1-1", "1-1", 2), []string{"a"}, "a 1-1").on(repairArgv([]string{"a"}), reply{exit: 1}), []string{"repair", "a"}},
		"disable-unit":   {unitFake(map[string]string{"a.service": idle("a.service")}).on(disableArgv([]string{"a.service"}), reply{exit: 1}), []string{"disable-unit", "a.service"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o, out := rootOpts(t, c.f)
			if code := Run(c.args, o); code != ExitFailed {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if res := result(t, out); res["child_exit"] != float64(1) || res["ok"] != false {
				t.Errorf("result %v", res)
			}
		})
	}
}

// TestCheckFilesCantRun: check-files fails (exit 1) with no check event
// when pacman can't be run: a broken list is what a repair is queued from,
// so none may come from a run that didn't happen.
func TestCheckFilesCantRun(t *testing.T) {
	cases := map[string]struct {
		f    *fakeRunner
		args []string
		want string
	}{
		"full -Qk":      {newFake().on(checkFilesArgv(), reply{fail: errNoRun}), []string{"check-files"}, "Checking package files: fork/exec"},
		"names -Qq":     {newFake().on(installedArgv(), reply{fail: errNoRun}), []string{"check-files", "a"}, "pacman -Qq: fork/exec"},
		"names -Qk -- ": {installedFake("a").on(checkPackagesArgv([]string{"a"}), reply{fail: errNoRun}), []string{"check-files", "a"}, "Checking 1 package(s) for missing files: fork/exec"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o, out := rootOpts(t, c.f)
			if code := Run(c.args, o); code != ExitFailed {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if checks := ofType(events(t, out), EventCheck); len(checks) != 0 {
				t.Errorf("a check event from a failed run: %v", checks)
			}
			if msg := resultError(t, out); !strings.HasPrefix(msg, c.want) {
				t.Errorf("error %q, want %q…", msg, c.want)
			}
		})
	}
}

// TestCheckFilesUnreadableCounts: a summary whose count doesn't fit an int
// is not read as a package with nothing missing: with names it is skipped
// ("could not check its files"), in the full check it is left out of
// checked; lines that aren't summaries are ignored.
func TestCheckFilesUnreadableCounts(t *testing.T) {
	huge := "99999999999999999999999"
	full := newFake().on(checkFilesArgv(), reply{out: "warning: odd: /x (No such file or directory)\n" +
		"odd: 9 total files, " + huge + " missing files\nfine: 3 total files, 0 missing files\n"})
	o, out := rootOpts(t, full)
	if code := Run([]string{"check-files"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	checks := ofType(events(t, out), EventCheck)
	if len(checks) != 1 || fmt.Sprint(checks[0]["checked"]) != "[fine]" || fmt.Sprint(checks[0]["broken"]) != "[]" {
		t.Fatalf("check events %v, want only fine checked", checks)
	}

	names := installedFake("odd").on(checkPackagesArgv([]string{"odd"}), reply{out: "odd: 9 total files, " + huge + " missing files\n", exit: 1})
	o, out = rootOpts(t, names)
	if code := Run([]string{"check-files", "odd"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	checks = ofType(events(t, out), EventCheck)
	if len(checks) != 1 || fmt.Sprint(checks[0]["checked"]) != "[]" {
		t.Fatalf("check events %v, want nothing checked", checks)
	}
	if reason := skippedNames(t, out)["odd"]; reason != "could not check its files" {
		t.Errorf("odd skipped with %q", reason)
	}
}

// TestRepairCantAsk: a repair whose checks can't run reinstalls nothing:
// a -Qk that can't run skips the package, a -Sp plan that can't run fails
// the verb.
func TestRepairCantAsk(t *testing.T) {
	f := onPkg(newFake(), "a", "1-1", "1-1", 2).on(pq("-Qk", "--", "a"), reply{fail: errNoRun})
	o, out := rootOpts(t, f)
	if code := Run([]string{"repair", "a"}, o); code != ExitOK {
		t.Fatalf("-Qk: exit %d:\n%s", code, out)
	}
	ranNothing(t, f)
	if reason := skippedNames(t, out)["a"]; reason != "could not check its files" {
		t.Errorf("-Qk: a skipped with %q", reason)
	}

	f = onPkg(newFake(), "a", "1-1", "1-1", 2).on(repairPlanArgv([]string{"a"}), reply{fail: errNoRun})
	o, out = rootOpts(t, f)
	if code := Run([]string{"repair", "a"}, o); code != ExitFailed {
		t.Fatalf("-Sp: exit %d, want 1:\n%s", code, out)
	}
	ranNothing(t, f)
	if msg := resultError(t, out); !strings.HasPrefix(msg, "pacman -Sp: fork/exec") {
		t.Errorf("-Sp: error %q", msg)
	}
}

// ---- the orphan plan -------------------------------------------------------

// TestPlanCantAsk: a plan pacman couldn't make is an error (no plan event,
// exit 1), never an empty plan: an empty plan's hash would let a removal
// confirmed for "nothing" through.
func TestPlanCantAsk(t *testing.T) {
	cascade := []string{Pacman, "-Rs", "--print", "--print-format", "%n %v", "--", "a"}
	cases := map[string]struct {
		f    *fakeRunner
		want string
	}{
		"-Qdtq can't run":  {newFake().on([]string{Pacman, "-Qdtq"}, reply{fail: errNoRun}), "pacman -Qdtq: fork/exec"},
		"-Qdtq fails":      {newFake().on([]string{Pacman, "-Qdtq"}, reply{exit: 1, err: "error: db locked\n"}), "pacman -Qdtq exited 1: error: db locked"},
		"-Qdtq fails mute": {newFake().on([]string{Pacman, "-Qdtq"}, reply{exit: 2}), "pacman -Qdtq exited 2: (no output)"},
		"-Rs can't run":    {orphanFake("a\n", "").on(cascade, reply{fail: errNoRun}), "pacman -Rs --print: fork/exec"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o, out := rootOpts(t, c.f)
			if code := Run([]string{"plan-orphans"}, o); code != ExitFailed {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if plans := ofType(events(t, out), EventPlan); len(plans) != 0 {
				t.Errorf("plan events %v", plans)
			}
			if msg := resultError(t, out); !strings.HasPrefix(msg, c.want) {
				t.Errorf("error %q, want %q…", msg, c.want)
			}
			// The same plan, recomputed as root right before -Rns: nothing
			// removed.
			c.f.streams = nil
			o, out = rootOpts(t, c.f)
			if code := Run([]string{"remove-orphans", PlanHash(nil, nil)}, o); code != ExitFailed {
				t.Fatalf("remove-orphans: exit %d, want 1:\n%s", code, out)
			}
			ranNothing(t, c.f)
		})
	}
}

// ---- disable-unit ----------------------------------------------------------

// TestDisableUnitCount: no unit, or more than maxNames, is refused before
// systemctl is asked anything.
func TestDisableUnitCount(t *testing.T) {
	var many []string
	for i := range maxNames + 1 {
		many = append(many, fmt.Sprintf("u%d.service", i))
	}
	for _, args := range [][]string{nil, many} {
		f := newFake()
		o, out := rootOpts(t, f)
		if code := Run(append([]string{"disable-unit"}, args...), o); code != ExitUsage {
			t.Fatalf("%d units: exit %d, want 2:\n%s", len(args), code, out)
		}
		if len(f.queries) != 0 {
			t.Fatalf("%d units: queried %q", len(args), join(f.queries))
		}
	}
}

// TestDisableUnitCantAsk: what systemctl can't tell the helper about a
// unit (show or cat can't run or fails, for the unit, an Also= companion
// or a trigger) fails the request (exit 1), or refuses it (exit 4) for a
// companion's [Install]; nothing is disabled either way.
func TestDisableUnitCantAsk(t *testing.T) {
	trig := idle("a.service") + "TriggeredBy=a.socket\n"
	cases := map[string]struct {
		f    *fakeRunner
		code int
		want string
	}{
		"show can't run": {newFake().on(unitShowArgv("a.service"), reply{fail: errNoRun}), ExitFailed, "systemctl show a.service: fork/exec"},
		"show fails": {
			newFake().on(unitShowArgv("a.service"), reply{exit: 1, err: "Failed to connect to bus\n"}),
			ExitFailed, "systemctl show a.service exited 1: Failed to connect to bus",
		},
		"cat can't run": {unitFake(map[string]string{"a.service": idle("a.service")}).on(unitCatArgv("a.service"), reply{fail: errNoRun}), ExitFailed, "systemctl cat a.service: fork/exec"},
		"companion show": {
			installFake(map[string]string{"a.service": idle("a.service")}, map[string]string{"a.service": "[Install]\nAlso=b.socket\n"}).
				on(unitShowArgv("b.socket"), reply{fail: errNoRun}),
			ExitFailed, "systemctl show b.socket: fork/exec",
		},
		"companion cat": {
			installFake(map[string]string{"a.service": idle("a.service"), "b.socket": idle("b.socket")}, map[string]string{"a.service": "[Install]\nAlso=b.socket\n"}).
				on(unitCatArgv("b.socket"), reply{fail: errNoRun}),
			ExitRefused, "refusing to disable a.service: cannot read the [Install] section of its Also= unit b.socket",
		},
		"trigger show": {
			unitFake(map[string]string{"a.service": trig}).on(unitShowArgv("a.socket"), reply{fail: errNoRun}),
			ExitFailed, "systemctl show a.socket: fork/exec",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o, out := rootOpts(t, c.f)
			if code := Run([]string{"disable-unit", "a.service"}, o); code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out)
			}
			ranNothing(t, c.f)
			if msg := resultError(t, out); !strings.HasPrefix(msg, c.want) {
				t.Errorf("error %q, want %q…", msg, c.want)
			}
		})
	}
}

// TestDisableUnitAlsoOdd: an Also= the helper can't vet (not a unit name:
// a path) refuses the request; a companion whose state systemctl doesn't
// say counts as running ("in an unknown state") and refuses it too.
func TestDisableUnitAlsoOdd(t *testing.T) {
	f := installFake(map[string]string{"a.service": idle("a.service")}, map[string]string{"a.service": "[Install]\nAlso=../../etc/x.service\n"})
	if msg := refusedWithoutRunning(t, f, "a.service"); !strings.Contains(msg, "cannot vet Also=../../etc/x.service") {
		t.Errorf("error %q", msg)
	}
	if slices.ContainsFunc(f.queries, func(q []string) bool { return slices.Contains(q, "../../etc/x.service") }) {
		t.Errorf("asked systemctl about %q", "../../etc/x.service")
	}

	f = installFake(
		map[string]string{"a.service": idle("a.service"), "b.socket": "Id=b.socket\nNames=b.socket\nLoadState=loaded\nUnitFileState=enabled\n"},
		map[string]string{"a.service": "[Install]\nAlso=b.socket\n"},
	)
	if msg := refusedWithoutRunning(t, f, "a.service"); !strings.Contains(msg, "b.socket too, which is in an unknown state") {
		t.Errorf("error %q", msg)
	}
}

// TestDisableUnitAlsoTooMany: Also= that pulls in more than maxVetted
// units is refused before anything is disabled.
func TestDisableUnitAlsoTooMany(t *testing.T) {
	var also []string
	states := map[string]string{"a.service": idle("a.service")}
	for i := range maxVetted {
		u := fmt.Sprintf("c%d.socket", i)
		also = append(also, u)
		states[u] = idle(u)
	}
	f := installFake(states, map[string]string{"a.service": "[Install]\nAlso=" + strings.Join(also, " ") + "\n"})
	if msg := refusedWithoutRunning(t, f, "a.service"); !strings.Contains(msg, fmt.Sprintf("Also= pulls in more than %d units", maxVetted)) {
		t.Errorf("error %q", msg)
	}
}

// ---- output ----------------------------------------------------------------

// TestSanitizeUnterminatedString: an OSC, DCS, APC, PM or SOS that never
// ends swallows the rest of the line: nothing after its introducer can
// reach a terminal.
func TestSanitizeUnterminatedString(t *testing.T) {
	for _, intro := range []string{"]", "P", "_", "^", "X"} {
		in := "ok\x1b" + intro + "0;evil title\x1b"
		if got := Sanitize(in); got != "ok" {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, "ok")
		}
	}
}
