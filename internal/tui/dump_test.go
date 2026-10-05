package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// envDumpChild runs TestDumpChild: this test binary again, as a process
// that calls DisableCoreDumps the way main does.
const envDumpChild = "ARCHMAID_DUMP_CHILD"

// TestDumpChild is DisableCoreDumps' subject; on its own it does nothing.
// It reports PR_GET_DUMPABLE and whether it can still read its own
// /proc/self/exe, then waits for its stdin to close.
func TestDumpChild(t *testing.T) {
	if os.Getenv(envDumpChild) == "" {
		t.Skip("run by TestDisableCoreDumps")
	}
	if err := DisableCoreDumps(); err != nil {
		t.Fatal(err)
	}
	d, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	self, err := os.Readlink("/proc/self/exe")
	fmt.Printf("dumpable=%d self-exe-ok=%v\n", d, err == nil && self != "")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// TestDisableCoreDumps: after DisableCoreDumps the process is not
// dumpable, so no core file can hold the password typed into it; it can
// still read its own /proc (os.Executable, the askpass Server's own
// checks); and another process of the same user can no longer read its
// ptrace-guarded /proc entries (exe here; mem, environ and fd alike).
func TestDisableCoreDumps(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestDumpChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), envDumpChild+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("the child: %v", err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("the child never exited")
		}
	}()
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "dumpable=") {
				line <- sc.Text()
				break
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	var got string
	select {
	case got = <-line:
	case <-time.After(20 * time.Second):
		t.Fatal("the child never reported")
	}
	if got != "dumpable=0 self-exe-ok=true" {
		t.Fatalf("child reported %q, want dumpable=0 and its own /proc readable", got)
	}
	_, err = os.Readlink(fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid))
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("another process read a non-dumpable process's exe link: %v", err)
	}
}
