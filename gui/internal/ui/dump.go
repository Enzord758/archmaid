package ui

import "syscall"

// archmaid-gui stays dumpable: GTK finds the desktop portal through it.
// xdg-desktop-portal identifies a caller by opening /proc/<pid>/root, which
// a non-dumpable process makes root's ("Portal operation not allowed:
// Unable to open /proc/<pid>/root"), and without the portal GTK's settings
// and header bar fall back to defaults. The askpass relay is the process
// made non-dumpable (privilege's handshake), not the GUI.

// dumpable is prctl PR_GET_DUMPABLE: 1 for a dumpable process.
func dumpable() int {
	r, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	return int(r)
}
