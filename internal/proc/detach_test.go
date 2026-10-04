package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDetach: a detached child (the GUI's, Options.Detach) leads a session
// and process group of its own, so it has no controlling terminal, reads
// /dev/null, and Stop takes its whole group down — what it left running
// included.
func TestDetach(t *testing.T) {
	t.Run("own session, no terminal, stdin /dev/null", func(t *testing.T) {
		ch, quit := make(chan Msg, 1), make(chan struct{})
		defer close(quit)
		c := New(Options{Detach: true, KillGrace: time.Second}).Start(Spec{Name: "sleep", Args: []string{"30"}}, ch, quit)
		if c == nil {
			t.Fatal("did not start")
		}
		defer c.Stop()
		self, err := Read(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		kid, err := Read(c.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if kid.Session != c.Pid || kid.Pgrp != c.Pid || kid.Session == self.Session {
			t.Fatalf("child %d: session %d, group %d (ours %d); want it to lead its own", c.Pid, kid.Session, kid.Pgrp, self.Session)
		}
		if tty := ttyNr(t, c.Pid); tty != 0 {
			t.Errorf("child has controlling terminal %d, want none", tty)
		}
		if in, err := os.Readlink("/proc/" + strconv.Itoa(c.Pid) + "/fd/0"); err != nil || in != os.DevNull {
			t.Errorf("child's stdin is %q (%v), want %s", in, err, os.DevNull)
		}
	})

	t.Run("Stop signals the group", func(t *testing.T) {
		// The shell leaves a sleep behind that ignores SIGINT (a
		// non-interactive shell's background job does), then waits on
		// one that doesn't. SIGINT to the group ends the shell; the
		// leftover is SIGKILLed at the grace.
		const grace = 300 * time.Millisecond
		ch := make(chan Msg, 4)
		c := New(Options{Detach: true, KillGrace: grace, WaitDelay: 50 * time.Millisecond}).Start(
			bash(`(trap '' INT; exec sleep 30) >/dev/null & echo "holder $!"; sleep 30`), ch, nil)
		if c == nil {
			t.Fatal("did not start")
		}
		msg := <-ch
		killHolder(t, msg.Line)
		holder, _ := strconv.Atoi(strings.TrimPrefix(msg.Line, "holder "))
		c.Stop()
		if _, err := drain(t, ch); err == nil {
			t.Fatal("the shell exited cleanly after Stop")
		}
		if err := syscall.Kill(holder, 0); err != nil {
			t.Fatalf("the leftover sleep went with the SIGINT (%v); it should ignore it", err)
		}
		select {
		case <-c.GroupStopped():
		case <-time.After(5 * time.Second):
			t.Fatal("GroupStopped never closed")
		}
		deadline := time.Now().Add(time.Second) // SIGKILL sent: only its delivery is left
		for alive(holder) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if alive(holder) {
			t.Fatalf("the shell's leftover sleep (pid %d) outlived Stop's grace", holder)
		}
	})

	t.Run("KillGroup kills the group at once", func(t *testing.T) {
		// A grace of a minute: only KillGroup can end the leftover in time.
		ch := make(chan Msg, 4)
		c := New(Options{Detach: true, KillGrace: time.Minute, WaitDelay: 50 * time.Millisecond}).Start(
			bash(`(trap '' INT; exec sleep 30) >/dev/null & echo "holder $!"; sleep 30`), ch, nil)
		if c == nil {
			t.Fatal("did not start")
		}
		msg := <-ch
		killHolder(t, msg.Line)
		holder, _ := strconv.Atoi(strings.TrimPrefix(msg.Line, "holder "))
		c.Stop() // SIGINT: the shell goes, the leftover stays
		if _, err := drain(t, ch); err == nil {
			t.Fatal("the shell exited cleanly after Stop")
		}
		if !alive(holder) {
			t.Fatal("the leftover went with the SIGINT; it should ignore it")
		}
		c.KillGroup() // the child is reaped: the guard finds the leftover
		deadline := time.Now().Add(time.Second)
		for alive(holder) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if alive(holder) {
			t.Fatalf("the leftover (pid %d) outlived KillGroup", holder)
		}
		select {
		case <-c.GroupStopped():
		case <-time.After(5 * time.Second):
			t.Fatal("the group's escalation never noticed the group is gone")
		}
	})

	t.Run("KillGroup of a plain child is a no-op", func(t *testing.T) {
		ch := make(chan Msg, 1)
		c := New(Options{KillGrace: time.Second}).Start(Spec{Name: "sleep", Args: []string{"30"}}, ch, nil)
		if c == nil {
			t.Fatal("did not start")
		}
		c.KillGroup()
		time.Sleep(50 * time.Millisecond)
		if !alive(c.Pid) {
			t.Fatal("KillGroup killed a child that isn't detached")
		}
		c.Stop()
		drain(t, ch)
		var nilChild *Child
		nilChild.KillGroup()
	})

	t.Run("Stop of an ended group signals nothing", func(t *testing.T) {
		ch := make(chan Msg, 1)
		c := New(Options{Detach: true, KillGrace: time.Second}).Start(Spec{Name: "true"}, ch, nil)
		if c == nil {
			t.Fatal("did not start")
		}
		if _, err := drain(t, ch); err != nil {
			t.Fatal(err)
		}
		<-c.reaped
		if groupAlive(c.Pid) {
			t.Fatal("an exited child's group reads as alive")
		}
		c.Stop() // no group left: a no-op, its id never signalled
	})
}

// alive: pid exists and isn't a zombie.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	return len(f) > 0 && f[0] != "Z"
}

// ttyNr is field 7 of /proc/<pid>/stat: the controlling terminal, 0 for none.
func ttyNr(t *testing.T, pid int) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:]) // f[0] is field 3
	n, err := strconv.Atoi(f[4])
	if err != nil {
		t.Fatal(err)
	}
	return n
}
