package proc

// Detached children (Options.Detach), the GUI's: the one place under cmd/
// and internal/ outside the root helper that may set a session field
// (TestNoSetsid exempts this file, and checks that the TUI never sets
// Detach).

import (
	"os"
	"strconv"
	"syscall"
)

// detachAttr starts a child in a session of its own: it leads a new session
// and process group and has no controlling terminal (none is given, and
// Setctty stays off), so neither it nor anything it starts can open
// /dev/tty. sudo then prompts through SUDO_ASKPASS or not at all; it can't
// read a password from the terminal archmaid-gui was started from, where
// the user isn't looking.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// signalGroup sends sig to the detached child's process group, which its
// pid names: the child and everything it started that stayed in the group.
// Once the child is reaped the group's id is only safe to signal while a
// member is left (the kernel doesn't reuse the id until then), so it is
// signalled only if groupAlive finds one.
func (c *Child) signalGroup(sig syscall.Signal) {
	select {
	case <-c.reaped:
		if !groupAlive(c.Pid) {
			return
		}
	default:
	}
	_ = syscall.Kill(-c.Pid, sig)
}

// KillGroup SIGKILLs a detached child and its whole process group at once,
// with no grace: for a front end that must exit now and may leave nothing
// running. After the child is reaped the group is signalled only while a
// member is left (signalGroup). A nil Child, or one that isn't detached,
// is a no-op.
func (c *Child) KillGroup() {
	if c == nil || c.p == nil || !c.group {
		return
	}
	_ = c.p.Kill() // a no-op once reaped
	c.signalGroup(syscall.SIGKILL)
	c.killOnce.Do(func() { close(c.killed) }) // a running Stop's timer ends now
}

// groupAlive reports whether any process is still in process group pgid
// of session pgid: the detached child's own group.
func groupAlive(pgid int) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false // can't tell: never signal an id that may be reused
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if in, err := Read(pid); err == nil && in.Pgrp == pgid && in.Session == pgid {
			return true
		}
	}
	return false
}
