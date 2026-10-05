package ui

import "syscall"

// NoCoreDumps makes the process non-dumpable (prctl PR_SET_DUMPABLE, 0):
// a crash of archmaid-gui never writes a core file, which could hold a heap
// copy of the run's password (the typed answer's engine.Secret, a short-
// lived copy of the kept one; the kept page itself is MADV_DONTDUMP).
//
// It also makes the process's /proc entries root-owned and closes the
// ptrace-guarded ones (exe, fd, environ, mem) to other processes of the
// user. What reads ours still can: polkitd and pkttyagent read stat,
// status and cgroup (mode 0444) for pkexec's subject and the agent's
// --process, and archmaid itself reads its own. The askpass relay mode
// (archmaid-gui --askpass) must stay dumpable until the GUI has verified
// it (privilege.Request.Verify reads its exe), so main calls this only
// after the relay dispatch; the relay makes itself non-dumpable in the
// askpass handshake, before the password reaches it (privilege.ReadAnswer).
func NoCoreDumps() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// dumpable is prctl PR_GET_DUMPABLE: 0 once NoCoreDumps ran.
func dumpable() int {
	r, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	return int(r)
}
