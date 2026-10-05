package ui

import "syscall"

// archmaid-gui stays dumpable: GTK finds the desktop portal through it.
// xdg-desktop-portal identifies a caller by opening /proc/<pid>/root, which
// a non-dumpable process makes root's ("Portal operation not allowed:
// Unable to open /proc/<pid>/root"), and without the portal GTK's settings
// and header bar fall back to defaults. The askpass relay is the process
// made non-dumpable (privilege's handshake), not the GUI.

// coreLimit is RLIMIT_CORE's soft and hard limit for archmaid-gui: 1 byte.
//
// With a pipe core_pattern (systemd-coredump, as here: "|/usr/lib/systemd/
// systemd-coredump %P %u %g %s %t %c %h %d %F %I"), the kernel ignores a
// limit of 0: it streams the whole core to systemd-coredump, which then
// drops it ("Resource limits disable core dumping", anything under a page;
// systemd v262 coredump-submit.c), so the password would still pass
// through root's helper. A limit of 1 is the kernel's own guard against
// recursive crashes: it aborts the dump before any helper runs ("RLIMIT_CORE
// is set to 1, aborting core", fs/coredump.c, coredump_pipe), and with a
// file core_pattern 1 byte is below binfmt->min_coredump, so no file is
// written either.
const coreLimit = 1

// NoCoreFiles sets RLIMIT_CORE to coreLimit (soft and hard, so nothing in
// the process raises it again), unless it is lower already. A crash of
// archmaid-gui then writes no core anywhere, so no heap copy of the run's
// password (the typed answer's engine.Secret, a short-lived copy of the
// kept one) reaches a core file or systemd-coredump. The children archmaid
// starts inherit it: a topgrade or yay that crashes under the GUI leaves no
// core either.
func NoCoreFiles() error {
	var cur syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &cur); err != nil {
		return err
	}
	if cur.Cur <= coreLimit && cur.Max <= coreLimit {
		return nil
	}
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: coreLimit, Max: coreLimit})
}

// coreRlimit is RLIMIT_CORE's soft limit.
func coreRlimit() uint64 {
	var cur syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &cur); err != nil {
		return ^uint64(0)
	}
	return cur.Cur
}

// dumpable is prctl PR_GET_DUMPABLE: 1 for a dumpable process.
func dumpable() int {
	r, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	return int(r)
}
