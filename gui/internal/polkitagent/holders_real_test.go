package polkitagent

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// The terminal check on the real /proc (QA, batch 6): a real child that
// holds our pty is found by device numbers; once it is a zombie (fd table
// gone) or reaped it holds nothing; and the real session lookup of a reaped
// pid says "gone", so the /dev/tty path doesn't fail closed on it.
func TestHoldersRealKernel(t *testing.T) {
	m, s, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer s.Close()
	id, err := fileIDOf(s)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	cmd.Stdin = s // the child holds our pty as fd 0, under whatever name
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	pid := cmd.Process.Pid
	if got := sys.holders(os.Getpid(), id); !slices.Contains(got, pid) {
		t.Fatalf("holders = %v, want the child %d that holds the pty", got, pid)
	}
	// Another index on the same instance is another pty: not held.
	other := id
	other.rdev[1] += 1000
	if got := sys.holders(os.Getpid(), other); slices.Contains(got, pid) {
		t.Fatalf("holders of another pty = %v, must not list the child", got)
	}
	// A zombie holds nothing: its fd table is gone.
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !isZombie(pid) {
		if time.Now().After(deadline) {
			t.Fatal("child never became a zombie")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := sys.holders(os.Getpid(), id); slices.Contains(got, pid) {
		t.Fatalf("holders = %v, a zombie holds nothing", got)
	}
	_ = cmd.Wait()
	reaped = true
	if got := sys.holders(os.Getpid(), id); slices.Contains(got, pid) {
		t.Fatalf("holders = %v, a reaped process holds nothing", got)
	}
	if _, err := sys.session(pid); !gone(err) {
		t.Fatalf("session of a reaped pid: %v, want it gone", err)
	}
}

// isZombie: /proc/<pid>/stat's state letter (after the last ")") is Z.
func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == ')' {
			return i+2 < len(b) && b[i+2] == 'Z'
		}
	}
	return false
}
