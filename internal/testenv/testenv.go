// Package testenv keeps a package's tests away from the developer's real home
// and from the real privileged binaries.
//
// Real run directories under ~/.local/state/archmaid are pruned to the newest
// few, so a test that writes there pushes real history out. Every test
// package calls Run from its TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
//
// Run points HOME and the XDG_* directories at a fresh temp dir for the whole
// package run, then checks the real state directory (resolved from the
// ORIGINAL environment) was not touched, and fails the run if it was. The
// check is scripts/state-guard.sh, the same one check.sh and the e2e harness
// use: a recursive snapshot of paths, types, sizes and mtimes, so a deleted,
// changed or added entry anywhere under the dir fails the package. Run finds
// the script by walking up from the package dir and fails closed without it.
//
// Run also puts traps first on PATH for every binary no test may reach for
// real (Trapped): the privileged ones (su, sudo, pkexec, runuser, faillock,
// pkttyagent) and every system-changing tool archmaid can invoke (topgrade,
// yay, paru, pacman, paccache, systemctl, journalctl, systemd-tmpfiles,
// hyprpm, flatpak, fwupdmgr). A wrong password through the real su or sudo
// can cost the developer a pam_faillock failure, and deny=3 is one typo from
// a lockout; a real topgrade once ran from a test for twelve minutes, until
// its yay reached the real sudo. A trap logs its argv and exits TrapExit; if
// any trap was hit, Run fails the package. Read-only calls are trapped too
// (`pacman -Qq`, `systemctl is-enabled`): the trap can't tell them apart, and
// a test must not depend on the machine's packages or units anyway. A test
// that means to run one of these puts its own shim first on PATH (or makes
// its shim dir the whole PATH), which still works. The traps catch
// subprocesses too: a re-exec'd test binary or a shell script inherits the
// PATH.
//
// What PATH can't catch: a hard-coded absolute path. No production code
// executes a privileged binary by absolute path (TestNoAbsolutePrivilegedExec
// keeps it that way), and archmaid-helper's absolute paths (/usr/bin/pacman
// and friends, run as root under pkexec) only ever run in its tests through
// a fake Runner. The programs archmaid's own package installs and archmaid
// runs by absolute path (the widget's update check) are variables in
// internal/installed, and Run points each at a trap of its own for the whole
// run: a test that reaches one fails the package like any trapped call, on a
// machine where the real one is installed as on one where it isn't
// (TestNoAbsoluteInstalledExec keeps their literals in that package).
//
// Nothing a test starts may outlive the package run. Run sets MarkerEnv to a
// fresh id for the whole run, which every process a test starts inherits, and
// makes the test binary a child subreaper, so an orphaned descendant (even
// one started with a hand-built environment) is reparented to it instead of
// escaping to the user manager. When the tests are done, and before the traps
// are removed, Run finds every process of this uid that carries the marker or
// descends from the test binary, SIGKILLs them (and the process groups they
// lead) and fails the package, naming each one.
package testenv

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Enzord758/archmaid/internal/installed"
)

const unset = "\x00unset"

// Privileged are the binaries that authenticate or that run something as
// another user. pkttyagent is polkit's terminal agent, which pkexec starts to
// ask for a password. No Go file may name one by absolute path.
var Privileged = []string{"su", "sudo", "pkexec", "runuser", "faillock", "pkttyagent"}

// Mutators are the system-changing tools archmaid can invoke: directly
// (internal/actions, internal/steps), through archmaid-helper, or through
// topgrade (paru, flatpak, fwupdmgr). They are trapped whatever the call,
// read-only ones included.
var Mutators = []string{
	"topgrade", "yay", "paru", "pacman", "paccache", "systemctl", "journalctl",
	"systemd-tmpfiles", "hyprpm", "flatpak", "fwupdmgr",
}

// Trapped are the binaries Run traps: Privileged and Mutators.
var Trapped = append(append([]string{}, Privileged...), Mutators...)

// MarkerEnv is the variable Run sets to a fresh id for the package run: a
// process that carries it was started by one of the package's tests.
const MarkerEnv = "ARCHMAID_TESTENV"

// TrapExit is the status a trap exits with.
const TrapExit = 97

// Run isolates the environment, runs the package's tests and returns the exit
// code for os.Exit: m.Run's, or 1 if a test touched the real state directory,
// reached a trapped binary or left a process running.
func Run(m interface{ Run() int }) int {
	var made []string
	code := run(m, &made)
	// run defers the removal of its scratch dirs; prove it happened.
	for _, p := range made {
		if _, err := os.Lstat(p); err == nil {
			fmt.Fprintln(os.Stderr, "\ntestenv: FAIL: left behind:", p, "(removed now)")
			_ = os.RemoveAll(p)
			code = 1
		}
	}
	return code
}

// CreatedEnv names a file that every Run (this process's and the re-exec'd
// test binaries it starts) appends the scratch dirs it creates to, one path
// per line, when set. scripts/testenv-leftovers.sh uses it to fail a gate run
// when any of them still exists afterwards, however its test binary ended.
const CreatedEnv = "ARCHMAID_TESTENV_CREATED"

// mkScratch makes a temp dir named prefix*, records it in made and in the
// CreatedEnv file.
func mkScratch(prefix string, made *[]string) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	*made = append(*made, dir)
	if f := os.Getenv(CreatedEnv); f != "" {
		if w, err := os.OpenFile(f, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
			fmt.Fprintln(w, dir)
			w.Close()
		}
	}
	return dir, nil
}

func run(m interface{ Run() int }, made *[]string) int {
	marker, err := newMarker()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	// Orphaned descendants are reparented to this process, not to the user
	// manager, so the scan below sees them whatever environment they got.
	if err := setChildSubreaper(); err != nil {
		fmt.Fprintln(os.Stderr, "testenv: PR_SET_CHILD_SUBREAPER:", err)
		return 1
	}
	// The traps and the snapshot live outside the scratch home: tests list
	// and wipe that. The trap dir goes last, after the orphan scan: a
	// process that outlived it would reach the real binaries.
	trapRoot, err := mkScratch("archmaid-test-trap", made)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(trapRoot)
	// Snapshot the real state dir before anything changes the environment.
	guard, err := guardScript()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	before := filepath.Join(trapRoot, "state.before")
	if err := guardSnapshot(guard, before); err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	trapBin, trapLog, err := InstallTraps(trapRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	// PATH can't catch an absolute path: point archmaid's installed
	// programs at traps of their own until the leftovers are reaped.
	restoreInstalled, err := TrapInstalled(trapBin, trapLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}

	home, err := mkScratch("archmaid-test-home", made)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(home)
	run := filepath.Join(home, "run")
	if err := os.Mkdir(run, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}

	saved := map[string]string{}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_RUNTIME_DIR": run,
		"PATH":            trapBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		MarkerEnv:         marker,
	} {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = old
		} else {
			saved[k] = unset
		}
		os.Setenv(k, v)
	}

	// Should this binary die before the check below (watchdog.go).
	dog, err := startWatchdog(marker, trapRoot, home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv: watchdog:", err)
		return 1
	}

	coverChildren()
	code := m.Run()

	// Kill what the tests left running first, while the traps are still on
	// its PATH. The watchdog goes first: its own kill round would hide a
	// leak from the report.
	dog.stop()
	left, scanErr := reapLeftovers(marker)
	restoreInstalled()

	for k, v := range saved {
		if v == unset {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	// Report every failure, not just the first: a test can do both.
	failed := false
	if scanErr != nil {
		fmt.Fprintln(os.Stderr, "\ntestenv: FAIL: can't check for processes the tests left running:", scanErr)
		failed = true
	}
	if len(left) > 0 {
		fmt.Fprintln(os.Stderr, "\ntestenv: FAIL: a test left processes running (killed now; a test must wait for or kill what it starts):")
		for _, p := range left {
			fmt.Fprintln(os.Stderr, "  ", p)
		}
		failed = true
	}
	if calls := TrapCalls(trapLog); len(calls) > 0 {
		fmt.Fprintln(os.Stderr, "\ntestenv: FAIL: a test reached a real privileged or system-changing binary"+
			" (shim it first on PATH; an installed program run by absolute path: fake what runs it):")
		for _, c := range calls {
			fmt.Fprintln(os.Stderr, "  ", c)
		}
		failed = true
	}
	if out, err := guardCompare(guard, before); err != nil {
		fmt.Fprintf(os.Stderr, "\ntestenv: FAIL: the tests changed the real state dir (%v):\n%s", err, out)
		failed = true
	}
	if failed {
		return 1
	}
	return code
}

// guardScript finds scripts/state-guard.sh in the module: a package's tests
// run in the package dir, somewhere below the repo root.
func guardScript() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		p := filepath.Join(dir, "scripts", "state-guard.sh")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("scripts/state-guard.sh not found above " + wd + ": can't guard the real state dir")
		}
		dir = parent
	}
}

// guardSnapshot writes state-guard.sh's snapshot of the real state dir (as
// the current environment resolves it) to file.
func guardSnapshot(guard, file string) error {
	cmd := exec.Command("bash", guard, "snapshot")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("state-guard snapshot: %v: %s", err, stderr.String())
	}
	return os.WriteFile(file, out, 0o600)
}

// guardCompare runs state-guard.sh's compare against the snapshot in file.
// A change, or any failure of the guard itself, is an error; the guard's
// output (the diff) comes back for the report.
func guardCompare(guard, file string) (string, error) {
	out, err := exec.Command("bash", guard, "compare", file).CombinedOutput()
	return string(out), err
}

// InstallTraps writes a trap for each of Trapped into dir/bin and returns
// that bin dir and the log the traps append to (created empty). A trap is a
// #!/bin/sh script of builtins only, so it works on any PATH, and it never
// reads stdin, so a password piped to it is never stored.
func InstallTraps(dir string) (bin, log string, err error) {
	bin = filepath.Join(dir, "bin")
	log = filepath.Join(dir, "trap.log")
	if err := os.Mkdir(bin, 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		return "", "", err
	}
	for _, name := range Trapped {
		script := "#!/bin/sh\n" +
			"# archmaid testenv trap: no test may run the real " + name + ".\n" +
			"printf '%s\\n' \"" + name + " $*\" >> " + shQuote(log) + "\n" +
			"echo 'testenv: trapped a call to the real " + name + "; shim it first on PATH' >&2\n" +
			fmt.Sprintf("exit %d\n", TrapExit)
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			return "", "", err
		}
	}
	return bin, log, nil
}

// TrapInstalled points every path in internal/installed at a trap in bin
// named after it, which logs "<real path> <argv>" to log (as InstallTraps'
// do) and exits TrapExit, and returns the func that points them back. The
// real paths are absolute, which PATH can't catch; under Run no test can
// execute one, whether or not this machine has archmaid installed.
func TrapInstalled(bin, log string) (restore func(), err error) {
	paths := installed.All()
	saved := make([]string, len(paths))
	for i, p := range paths {
		saved[i] = *p
	}
	restore = func() {
		for i, p := range paths {
			*p = saved[i]
		}
	}
	for _, p := range paths {
		real := *p
		trap := filepath.Join(bin, filepath.Base(real))
		script := "#!/bin/sh\n" +
			"# archmaid testenv trap: no test may run the installed " + real + ".\n" +
			"printf '%s\\n' \"" + real + " $*\" >> " + shQuote(log) + "\n" +
			"echo 'testenv: trapped a run of the installed " + real + " (by absolute path); fake what runs it' >&2\n" +
			fmt.Sprintf("exit %d\n", TrapExit)
		if err := os.WriteFile(trap, []byte(script), 0o700); err != nil {
			restore()
			return nil, err
		}
		*p = trap
	}
	return restore, nil
}

// TrapCalls returns the calls in a trap log, one "name argv" per call. A
// missing or empty log means none.
func TrapCalls(log string) []string {
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// newMarker returns a fresh id for MarkerEnv.
func newMarker() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// setChildSubreaper makes this process the reaper of its orphaned
// descendants (prctl PR_SET_CHILD_SUBREAPER).
func setChildSubreaper() error {
	const prSetChildSubreaper = 36
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); errno != 0 {
		return errno
	}
	return nil
}

// shQuote single-quotes s for sh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
