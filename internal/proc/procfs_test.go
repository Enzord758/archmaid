package proc

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// TestRead reads the real /proc: our own entry matches what the runtime says,
// a child started after us has us as parent and a later start time, a root
// process's exe link is unreadable to a user (which is why the askpass check
// can't rely on it), and a missing pid is an error.
func TestRead(t *testing.T) {
	self, err := Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	sid, _, _ := syscall.RawSyscall(syscall.SYS_GETSID, 0, 0, 0) // no syscall.Getsid on linux
	if self.PPid != os.Getppid() || self.Pgrp != syscall.Getpgrp() || self.Session != int(sid) ||
		self.EUID != os.Geteuid() || self.Exe != exe || self.Start == 0 || self.Comm == "" {
		t.Fatalf("self = %+v; want ppid %d, pgrp %d, session %d, euid %d, exe %s and a start time",
			self, os.Getppid(), syscall.Getpgrp(), sid, os.Geteuid(), exe)
	}

	child := exec.Command("sleep", "10")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	defer child.Process.Kill()
	kid, err := Read(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if kid.PPid != os.Getpid() || kid.Start < self.Start || kid.Comm != "sleep" {
		t.Fatalf("child = %+v; want sleep, ppid %d and a start time ≥ %d", kid, os.Getpid(), self.Start)
	}

	if os.Geteuid() != 0 {
		init, err := Read(1)
		if err != nil {
			t.Fatal(err)
		}
		if init.EUID != 0 || init.Exe != "" {
			t.Fatalf("pid 1 = %+v; want euid 0 and an unreadable exe", init)
		}
	}

	// Above the kernel's PID_MAX_LIMIT (2^22): no process can have it.
	if _, err := Read(5_000_100); err == nil {
		t.Fatal("no error for a pid that can't exist")
	}
}
