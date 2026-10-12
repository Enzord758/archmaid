package testenv

import (
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

// watchdogChildEnv marks the re-exec'd half of TestWatchdogKillsAfterCrash;
// its value is the file the child writes its sleep's pid to.
const watchdogChildEnv = "ARCHMAID_TESTENV_WATCHDOG_CHILD"

// TestWatchdogChild starts `sleep 300` in a process group of its own (a
// detached step) and then dies by SIGKILL, as a test binary does on a
// crash or a timeout: no cleanup and no check of Run's runs. Skipped unless
// re-exec'd.
func TestWatchdogChild(t *testing.T) {
	pidFile := os.Getenv(watchdogChildEnv)
	if pidFile == "" {
		t.Skip("child half of TestWatchdogKillsAfterCrash")
	}
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}

// TestWatchdogKillsAfterCrash: what a test started still dies when its test
// binary is killed before Run's own check (the keep tests' step loops once
// outlived a crashed run by days, reparented to the user manager).
func TestWatchdogKillsAfterCrash(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() {
		if pid, ok := sleepPid(pidFile); ok && isSleep300(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestWatchdogChild$", "-test.count=1")
	created := filepath.Join(t.TempDir(), "created")
	cmd.Env = append(os.Environ(), watchdogChildEnv+"="+pidFile, CreatedEnv+"="+created)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("child: %v, want killed by SIGKILL", err)
	}
	pid, ok := sleepPid(pidFile)
	if !ok {
		t.Fatal("the child never started its sleep")
	}
	if isRunning(pid) && isSleep300(pid) {
		t.Fatalf("pid %d (sleep 300) outlived its crashed test binary", pid)
	}
	waitOrphanedWatchdogs(t)
	// A crashed binary skips Run's deferred cleanup: the watchdog removes
	// the scratch dirs (they used to stay in TMPDIR).
	b, _ := os.ReadFile(created)
	dirs := strings.Fields(string(b))
	if len(dirs) != 2 {
		t.Fatalf("the child recorded %q, want its trap and home dirs", dirs)
	}
	for _, d := range dirs {
		if _, err := os.Lstat(d); err == nil {
			t.Errorf("%s outlived its crashed test binary", d)
			_ = os.RemoveAll(d)
		}
	}
}

// waitOrphanedWatchdogs waits for the crashed child's watchdog to finish
// its round: this binary, the child's subreaper, inherited it, and this
// package's own check would otherwise count it as left running.
func waitOrphanedWatchdogs(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		ents, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		busy := 0
		for _, e := range ents {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			p, ok := readStat(pid)
			if !ok || p.ppid != os.Getpid() {
				continue
			}
			b, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
			if p.state == 'Z' {
				var ws syscall.WaitStatus
				_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
				continue
			}
			// Ours (this binary's own, for its marker) stays up.
			if strings.HasPrefix(string(b), "/bin/sh\x00-c\x00"+watchdogScript) &&
				!strings.Contains(string(b), "\x00"+os.Getenv(MarkerEnv)+"\x00") {
				busy++
			}
		}
		if busy == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the crashed child's watchdog never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
