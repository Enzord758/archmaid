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
// ORIGINAL environment) was not touched, and fails the run if it was.
//
// Run also puts traps first on PATH for the privileged binaries no test may
// reach for real (Trapped: su, sudo, pkexec, runuser, faillock, pkttyagent).
// A wrong password through the real su or sudo can cost the developer a
// pam_faillock failure, and deny=3 is one typo from a lockout. A trap logs its
// argv and exits TrapExit; if any trap was hit, Run fails the package. A test
// that means to run one of these puts its own shim first on PATH (or makes its
// shim dir the whole PATH), which still works. The traps catch subprocesses
// too: a re-exec'd test binary or a shell script inherits the PATH.
//
// What PATH can't catch: a hard-coded absolute path. No production code
// executes su, sudo or pkexec by absolute path (TestNoAbsolutePrivilegedExec
// keeps it that way), and archmaid-helper's absolute paths (/usr/bin/pacman
// and friends, run as root under pkexec) only ever run in its tests through
// a fake Runner.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const unset = "\x00unset"

// Trapped are the binaries Run traps: the ones that authenticate or that run
// something as another user. pkttyagent is polkit's terminal agent, which
// pkexec starts to ask for a password.
var Trapped = []string{"su", "sudo", "pkexec", "runuser", "faillock", "pkttyagent"}

// TrapExit is the status a trap exits with.
const TrapExit = 97

// Run isolates the environment, runs the package's tests and returns the exit
// code for os.Exit: m.Run's, or 1 if a test touched the real state directory
// or reached a trapped binary.
func Run(m interface{ Run() int }) int {
	realState := realStateDir()
	before := snapshot(realState)
	start := time.Now()

	// The traps live outside the scratch home: tests list and wipe that.
	trapRoot, err := os.MkdirTemp("", "archmaid-test-trap")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(trapRoot)
	trapBin, trapLog, err := InstallTraps(trapRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}

	home, err := os.MkdirTemp("", "archmaid-test-home")
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
	} {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = old
		} else {
			saved[k] = unset
		}
		os.Setenv(k, v)
	}

	code := m.Run()

	for k, v := range saved {
		if v == unset {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	if calls := TrapCalls(trapLog); len(calls) > 0 {
		fmt.Fprintln(os.Stderr, "\ntestenv: FAIL: a test reached a real privileged binary (shim it first on PATH):")
		for _, c := range calls {
			fmt.Fprintln(os.Stderr, "  ", c)
		}
		return 1
	}
	if bad := touched(realState, before, start); len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "\ntestenv: FAIL: tests wrote into the real state dir %s:\n", realState)
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "  ", b)
		}
		return 1
	}
	return code
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

// shQuote single-quotes s for sh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// realStateDir is where archmaid keeps its logs for the user who started the
// test run, from the environment as it was before Run overrides anything.
func realStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "archmaid")
}

// snapshot maps each entry of dir (and the dir itself, as ".") to its
// modification time. A missing dir gives an empty map.
func snapshot(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	if dir == "" {
		return out
	}
	if fi, err := os.Stat(dir); err == nil {
		out["."] = fi.ModTime()
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			out[e.Name()] = fi.ModTime()
		}
	}
	return out
}

// touched lists entries of dir that are new, changed, or newer than start.
func touched(dir string, before map[string]time.Time, start time.Time) []string {
	var bad []string
	for name, mt := range snapshot(dir) {
		old, existed := before[name]
		if !existed || !mt.Equal(old) || mt.After(start) {
			bad = append(bad, name)
		}
	}
	return bad
}
