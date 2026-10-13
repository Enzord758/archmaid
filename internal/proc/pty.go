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
// The slave is opened from the master (TIOCGPTPEER, no path lookup) and
// then made exclusive (TIOCEXCL): nobody without CAP_SYS_ADMIN can open
// /dev/pts/N again, so nothing under the step can re-open it by name — the
// one way a session leader would pick it up as its controlling terminal.

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

// ptyTerm is the TERM a pty child gets: what its output is drawn by
// (internal/vt and the Run view take 16, 256 and 24-bit colour).
const ptyTerm = "TERM=xterm-256color"

// openPty opens a pty of cols columns (defaultCols when < 1): the master
// for archmaid to read (pollable, so a read deadline works), the slave for
// the child, exclusive and close-on-exec.
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
	ws := &unix.Winsize{Row: ptyRows, Col: uint16(cols)}
	if err := unix.IoctlSetWinsize(sfd, unix.TIOCSWINSZ, ws); err != nil {
		unix.Close(sfd)
		m.Close()
		return nil, nil, err
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sfd), unix.TIOCEXCL, 0); errno != 0 {
		unix.Close(sfd)
		m.Close()
		return nil, nil, errno
	}
	return m, os.NewFile(uintptr(sfd), "pty"), nil
}

// peer opens the slave of master fd: TIOCGPTPEER (Linux 4.13), else by its
// /dev/pts name, never as a controlling terminal.
func peer(fd int) (int, error) {
	flags := uintptr(unix.O_RDWR | unix.O_NOCTTY | unix.O_CLOEXEC)
	s, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCGPTPEER, flags)
	if errno == 0 {
		return int(s), nil
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		return -1, err
	}
	return unix.Open("/dev/pts/"+strconv.FormatUint(uint64(n), 10), int(flags), 0)
}
