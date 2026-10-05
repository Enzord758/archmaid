// Package ttyguard keeps the TUI's step children off its terminal.
//
// The TUI reads its keys from the terminal it runs in, and its step
// children must stay in its session, because sudo's ticket is keyed to that
// terminal and session (internal/proc, TestNoSetsid). Their stdin is
// /dev/null, but a child can still open /dev/tty — the controlling terminal
// of the session — on its own: git over HTTPS asking for a username, ssh
// for a passphrase or a host key, gpg's pinentry-tty, a tcell program. It
// then reads the same keys as Bubble Tea, and keystrokes alternate between
// the two: the TUI looks frozen and can't be aborted, and a "y" meant for
// the abort question becomes git's username.
//
// Hold puts the terminal into exclusive mode (TIOCEXCL) while the TUI runs:
// the kernel then refuses every new open of it (/dev/tty, /dev/pts/N,
// /proc/<pid>/fd/…) to any process without CAP_SYS_ADMIN, with EBUSY. File
// descriptors already open keep working, so Bubble Tea and the user's shell
// are untouched, and nothing about processes changes: same session, same
// process group, same controlling terminal, so sudo's ticket works as
// before. A child that asks on the terminal fails at once, as it would with
// no terminal at all (the GUI's case), instead of stealing keys.
//
// What it does not cover: root. sudo and su are setuid root and may still
// open the terminal (so a password prompt of theirs is not broken), and so
// may a command sudo runs as root without its own pty (use_pty off). And it
// is the TUI's to undo: Release on exit, and Pause around a program that is
// handed the terminal on purpose (the editor). A SIGKILLed archmaid leaves
// the flag set until the terminal is closed; new opens of it then fail.
package ttyguard

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// tiocgexcl is TIOCGEXCL (_IOR('T', 0x40, int)), which the syscall package
// lacks.
const tiocgexcl = 0x80045440

// Guard is a terminal held exclusive. Its methods are safe for concurrent
// use; a nil Guard does nothing.
type Guard struct {
	mu     sync.Mutex
	f      *os.File
	held   bool // TIOCEXCL is ours to undo
	paused int
}

// Hold puts f's terminal into exclusive mode and returns the Guard that
// undoes it. f not being a terminal, or one already exclusive (someone
// else's to undo), is a Guard that does nothing; so is a failed ioctl,
// which it reports.
func Hold(f *os.File) (*Guard, error) {
	g := &Guard{f: f}
	if f == nil || !IsTerminal(f) {
		return g, nil
	}
	if on, err := Exclusive(f); err != nil || on {
		return g, err
	}
	if err := ioctl(f, syscall.TIOCEXCL, nil); err != nil {
		return g, err
	}
	g.held = true
	return g, nil
}

// Release leaves exclusive mode for good (TIOCNXCL), if Hold entered it.
func (g *Guard) Release() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.held {
		return nil
	}
	g.held = false
	if g.paused > 0 {
		return nil // already off
	}
	return ioctl(g.f, syscall.TIOCNXCL, nil)
}

// Pause leaves exclusive mode until the returned func is called, for a
// program the TUI hands its terminal to (the editor: one built on tcell
// opens /dev/tty). Pauses nest.
func (g *Guard) Pause() (resume func()) {
	if g == nil {
		return func() {}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held && g.paused == 0 {
		_ = ioctl(g.f, syscall.TIOCNXCL, nil)
	}
	g.paused++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.paused--
			if g.held && g.paused == 0 {
				_ = ioctl(g.f, syscall.TIOCEXCL, nil)
			}
		})
	}
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	var t syscall.Termios
	return ioctl(f, syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// Exclusive reports whether f's terminal is in exclusive mode.
func Exclusive(f *os.File) (bool, error) {
	var on int32
	if err := ioctl(f, tiocgexcl, unsafe.Pointer(&on)); err != nil {
		return false, err
	}
	return on != 0, nil
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
