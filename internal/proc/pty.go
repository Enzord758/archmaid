package proc

// The GUI's pty (Options.Pty): a terminal as the child's output only. This
// file is the one place in the module that opens a pseudo-terminal
// (TestChildrenNeverRunOnATerminal holds that); Query never uses it.
//
// The child gets the slave as fd 1 and fd 2, stdin stays /dev/null, and it
// runs in a session of its own (detachAttr: Setsid, never Setctty). An
// inherited tty fd does not make a terminal the controlling one: only a
// session leader's open() of a tty without O_NOCTTY, or TIOCSCTTY, does.
// So the child has no controlling terminal: /dev/tty can't be opened
// (ENXIO), tty_nr in /proc/<pid>/stat is 0, and sudo — which looks for its
// tty there and then tries /dev/tty — still finds none and asks through
// SUDO_ASKPASS or not at all. What changes is only isatty(1) and isatty(2):
// tools turn on colour and progress bars, and read the width from
// TIOCGWINSZ.
//
// The slave is opened from the master (TIOCGPTPEER, no path lookup),
// O_NOCTTY and write-only, and its /dev/pts/N is made mode 0200. That
// makes "output only" the kernel's rule, not a convention (review #135):
//
//   - TIOCSCTTY needs read access (EPERM without it, short of
//     CAP_SYS_ADMIN), and a session leader's open() of a tty makes it the
//     controlling terminal only when the open can read. So neither the
//     inherited fds nor a write-only open by name can make the pty one.
//   - An open for reading by name fails for the user (EACCES, the mode).
//     Root (sudo's pacman) passes the mode; nothing there opens a
//     terminal by name as a session leader.
//   - A read of fd 1 or 2 fails at once (EBADF) instead of waiting for
//     input archmaid never writes: a pager that reads its keys from fd 2
//     when /dev/tty fails (less; git or journalctl with stdout a terminal)
//     gives up instead of hanging the step.
//   - Opens for writing by name work: `>/dev/stderr`, `>/dev/stdout` and
//     `>/proc/self/fd/1` in scripts and PKGBUILDs. That is why the slave is
//     not exclusive (TIOCEXCL), which refused them all (EBUSY) and could
//     fail an AUR build under yay.

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// Default pty size: 80 columns when the front end gave none, and a fixed,
// generous height (no step draws a full screen; a few size their output by
// it).
const (
	defaultCols = 80
	ptyRows     = 50
	maxCols     = 1000
)

// slaveMode is the slave's /dev/pts/N mode: its owner may open it for
// writing only (`>/dev/stderr`), nobody else at all (not the tty group's
// write(1) and wall(1) either).
const slaveMode = 0o200

// ptyTerm is the TERM a pty child gets: what its output is drawn by
// (internal/vt and the Run view take 16, 256 and 24-bit colour).
const ptyTerm = "TERM=xterm-256color"

// openPty opens a pty of cols columns (defaultCols when < 1): the master
// for archmaid to read (pollable, so a read deadline works), the slave for
// the child, write-only, mode slaveMode and close-on-exec.
func openPty(cols int) (master, slave *os.File, err error) {
	if cols < 1 {
		cols = defaultCols
	}
	cols = min(cols, maxCols)
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var sfd int
	var ierr error
	raw, err := m.SyscallConn()
	if err == nil {
		err = raw.Control(func(fd uintptr) {
			if ierr = unix.IoctlSetPointerInt(int(fd), unix.TIOCSPTLCK, 0); ierr != nil {
				return
			}
			sfd, ierr = peer(int(fd))
		})
	}
	if err == nil {
		err = ierr
	}
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	err = unix.IoctlSetWinsize(sfd, unix.TIOCSWINSZ, &unix.Winsize{Row: ptyRows, Col: uint16(cols)})
	if err == nil {
		err = unix.Fchmod(sfd, slaveMode)
	}
	if err != nil {
		unix.Close(sfd)
		m.Close()
		return nil, nil, err
	}
	return m, os.NewFile(uintptr(sfd), "pty"), nil
}

// Seams: a test makes TIOCGPTPEER fail (a kernel before 4.13) and sees
// how the slave is then opened by name.
var (
	ptyPeerIoctl = func(fd int, flags uintptr) (int, error) {
		s, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCGPTPEER, flags)
		if errno != 0 {
			return -1, errno
		}
		return int(s), nil
	}
	ptyOpenByName = unix.Open
)

// peer opens the slave of master fd: TIOCGPTPEER (Linux 4.13), else by its
// /dev/pts name, for writing only and never as a controlling terminal.
func peer(fd int) (int, error) {
	flags := uintptr(unix.O_WRONLY | unix.O_NOCTTY | unix.O_CLOEXEC)
	if s, err := ptyPeerIoctl(fd, flags); err == nil {
		return s, nil
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		return -1, err
	}
	return ptyOpenByName("/dev/pts/"+strconv.FormatUint(uint64(n), 10), int(flags), 0)
}
