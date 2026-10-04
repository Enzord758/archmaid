package testenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// orphanChildEnv marks the re-exec'd half of TestRunReapsOrphans; its value
// is "<how>:<file>", file being where the child writes its background
// process's pid.
const orphanChildEnv = "ARCHMAID_TESTENV_ORPHAN_CHILD"

// TestOrphanChild starts `sleep 300` in the background and returns without
// waiting for it: the leak that let a real topgrade outlive its test. The
// test passes; the child's Run must kill the sleep and fail the package.
// Skipped unless re-exec'd. how is
//   - "marker": the sleep, in its own process group as a detached tool
//     would make, is found by the marker it inherits;
//   - "orphan": a shell with a hand-built environment (no marker) starts it
//     and exits, so it is found as an orphan reparented to the test binary.
func TestOrphanChild(t *testing.T) {
	how, pidFile, ok := strings.Cut(os.Getenv(orphanChildEnv), ":")
	if !ok {
		t.Skip("child half of TestRunReapsOrphans")
	}
	switch how {
	case "marker":
		cmd := exec.Command("sleep", "300")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
	case "orphan":
		cmd := exec.Command("sh", "-c", `sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "$1"`, "sh", pidFile)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown how %q", how)
	}
}

// TestRunReapsOrphans proves the orphan check: a test that leaves a process
// running fails its package, the report names the process, and the process
// is gone by the time Run returns; whether the process carries Run's marker
// or was orphaned with an environment of its own.
func TestRunReapsOrphans(t *testing.T) {
	for _, how := range []string{"marker", "orphan"} {
		t.Run(how, func(t *testing.T) { testRunReapsOrphans(t, how) })
	}
}

func testRunReapsOrphans(t *testing.T, how string) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// Never leave the sleep behind, whatever Run did: kill it here if it is
	// still the sleep this test started.
	t.Cleanup(func() {
		if pid, ok := sleepPid(pidFile); ok && isSleep300(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestOrphanChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), orphanChildEnv+"="+how+":"+pidFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	pid, ok := sleepPid(pidFile)
	if !ok {
		t.Fatalf("the child never started its sleep: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	alive := isRunning(pid)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("child: %v, want exit 1\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS") {
		t.Errorf("the child's test itself should pass (only Run fails):\n%s", stdout.String())
	}
	out := stderr.String()
	if want := "a test left processes running"; !strings.Contains(out, want) {
		t.Errorf("report misses %q:\n%s", want, out)
	}
	if want := strconv.Itoa(pid) + " sleep sleep 300"; !strings.Contains(out, want) {
		t.Errorf("report misses %q:\n%s", want, out)
	}
	if alive {
		t.Errorf("pid %d (sleep 300) still running after the child's Run returned", pid)
	}
}

// TestParseStat: comm may hold spaces and parentheses; the fields after the
// last ")" are the state, ppid and process group.
func TestParseStat(t *testing.T) {
	p, ok := parseStat("4242 (a (b) c) S 17 4242 17 0 -1 4194560\n")
	if !ok || p.pid != 4242 || p.comm != "a (b) c" || p.state != 'S' || p.ppid != 17 || p.pgid != 4242 {
		t.Fatalf("parseStat = %+v, %v", p, ok)
	}
	for _, bad := range []string{"", "4242 sleep S 1 1", "x (sleep) S 1 1", "4242 (sleep) S 1", "4242 (sleep) S x 1"} {
		if _, ok := parseStat(bad); ok {
			t.Errorf("parseStat(%q) ok", bad)
		}
	}
}

// sleepPid reads the pid the child wrote.
func sleepPid(file string) (int, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}

// isSleep300 reports whether pid is (still) a `sleep 300`, so the cleanup
// never kills a recycled pid.
func isSleep300(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err == nil && string(b) == "sleep\x00300\x00"
}

// isRunning reports whether pid exists and is not a zombie. A killed process
// can take a moment to go, so it gets a short grace.
func isRunning(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		// The state follows the ")" that ends comm.
		s := string(b)
		if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) && s[i+2] == 'Z' {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
}
