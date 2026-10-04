package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// hardenChild is the re-exec'd half of TestHardenProcess: it hardens itself
// and reports what it sees. hardenProcess changes process-global state, so
// it must never run inside the test process proper.
const hardenChildEnv = "ARCHMAID_HELPER_HARDEN_CHILD"

type hardenReport struct {
	Env      []string
	Cwd      string
	Umask    int
	Fd3Open  bool
	Fd4Exec  bool // fd 4 open-and-not-CLOEXEC (should be false)
	Children string
}

func TestMain(m *testing.M) {
	// The grandchild is started by helper.ExecRunner, which passes no test
	// variables through (its environment is exactly ChildEnv), so it is
	// told apart by its argv instead.
	if len(os.Args) == 3 && os.Args[1] == sessionGrandchildArg {
		sessionGrandchild(os.Args[2])
		os.Exit(0)
	}
	if os.Getenv(sessionChildEnv) == "1" {
		sessionChild(os.Getenv(sessionDirEnv))
		os.Exit(0)
	}
	if os.Getenv(hardenChildEnv) == "1" {
		syscall.Umask(0o077)
		hardenProcess()
		r := hardenReport{Env: os.Environ()}
		r.Cwd, _ = os.Getwd()
		r.Umask = syscall.Umask(0o022)
		_, _, e := syscall.Syscall(syscall.SYS_FCNTL, 3, syscall.F_GETFD, 0)
		r.Fd3Open = e == 0
		flags, _, e := syscall.Syscall(syscall.SYS_FCNTL, 4, syscall.F_GETFD, 0)
		r.Fd4Exec = e == 0 && flags&syscall.FD_CLOEXEC == 0
		// What does a child of the hardened process inherit?
		out, _ := exec.Command("/bin/sh", "-c", "ls /proc/self/fd").Output()
		r.Children = strings.Join(strings.Fields(string(out)), " ")
		json.NewEncoder(os.Stdout).Encode(r)
		os.Exit(0)
	}
	switch os.Getenv(signalChildEnv) {
	case "mask":
		catchSignals()
		for _, sig := range caughtSignals {
			syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		}
		// Still here. What does a child start with? (cat reads its own
		// status; it runs through the real runner, like pacman would.)
		out, _, _, _ := helper.ExecRunner{}.Query([]string{"/usr/bin/cat", "/proc/self/status"})
		for l := range strings.SplitSeq(out, "\n") {
			if v, ok := strings.CutPrefix(l, "SigIgn:"); ok {
				os.Stdout.WriteString(strings.TrimSpace(v))
			}
		}
		os.Exit(0)
	case "epipe":
		// stdout is a pipe nobody reads: the GUI died.
		catchSignals()
		for range 3 {
			if _, err := os.Stdout.WriteString("x\n"); err == nil {
				os.Stderr.WriteString("write to a closed pipe succeeded\n")
				os.Exit(1)
			}
		}
		os.Stderr.WriteString("survived")
		os.Exit(0)
	}
	// The tests proper run under testenv: a scratch home, and traps first on
	// PATH for su, sudo, pkexec and the like (the children above inherit them).
	os.Exit(testenv.Run(m))
}

// signalChild is the re-exec'd half of the signal tests.
const signalChildEnv = "ARCHMAID_HELPER_SIGNAL_CHILD"

// TestCatchSignals: a terminal's Ctrl-C, Ctrl-\, Ctrl-Z or hangup, a
// SIGTERM or a closed pipe must not end or stop the helper before its result
// line, and a child must not inherit any of them as ignored: scriptlets and
// hooks have to be stoppable (timeout) and trap them normally.
func TestCatchSignals(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = []string{signalChildEnv + "=mask"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child did not survive its own signals: %v", err)
	}
	mask, err := strconv.ParseUint(string(out), 16, 64)
	if err != nil {
		t.Fatalf("SigIgn %q: %v", out, err)
	}
	for _, sig := range caughtSignals {
		if n := sig.(syscall.Signal); mask&(1<<(n-1)) != 0 {
			t.Errorf("a child of the helper starts with %v ignored (SigIgn %s)", sig, out)
		}
	}
}

// TestCatchSignalsSurvivesClosedStdout: Go exits on EPIPE writing to stdout
// unless SIGPIPE is handled; the helper must keep going (and keep draining
// pacman) after the GUI went away.
func TestCatchSignalsSurvivesClosedStdout(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer w.Close()
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = []string{signalChildEnv + "=epipe"}
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.String() != "survived" {
		t.Fatalf("child: %v, stderr %q", err, stderr.String())
	}
}

func TestHardenProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f3, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	f4, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer f4.Close()

	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{
		hardenChildEnv + "=1", "LD_PRELOAD=/tmp/evil.so", "PATH=/tmp/evil:/usr/bin",
		"IFS=x", "PKEXEC_UID=1000", "GCONV_PATH=/tmp", "HOME=/root",
	}
	cmd.ExtraFiles = []*os.File{f3, f4} // inherited as fd 3 and 4, not CLOEXEC
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	var r hardenReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("child output %q: %v", out, err)
	}
	env := slices.Sorted(slices.Values(r.Env))
	want := slices.Sorted(slices.Values(helper.ChildEnv()))
	if !slices.Equal(env, want) {
		t.Errorf("env after scrub = %q, want %q", env, want)
	}
	if r.Cwd != "/" {
		t.Errorf("cwd = %q, want /", r.Cwd)
	}
	if r.Umask != 0o022 {
		t.Errorf("umask = %#o, want 022", r.Umask)
	}
	if r.Fd3Open || r.Fd4Exec {
		t.Errorf("inherited fds survive: fd3 open=%v, fd4 inheritable=%v", r.Fd3Open, r.Fd4Exec)
	}
	// The grandchild sees 0,1,2 and the fd ls itself opens for /proc/self/fd.
	if fds := strings.Fields(r.Children); len(fds) > 4 {
		t.Errorf("a child of the helper inherits extra fds: %v", fds)
	}
}

func TestCallerUID(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		m    map[string]string
		want string
	}{
		{map[string]string{"PKEXEC_UID": "1000", "SUDO_UID": "1001"}, "1000"},
		{map[string]string{"SUDO_UID": "1001"}, "1001"},
		{map[string]string{}, ""},
	} {
		if got := callerUID(env(c.m)); got != c.want {
			t.Errorf("callerUID(%v) = %q, want %q", c.m, got, c.want)
		}
	}
}

// TestRealBuildIsNotDev guards against the devhelper tag leaking into the
// gate (it would make the binary refuse root and skip the install check).
func TestRealBuildIsNotDev(t *testing.T) {
	if devBuild {
		t.Fatal("devBuild is true without -tags devhelper")
	}
}
