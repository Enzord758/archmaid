package main

import (
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// A private pty shared by every step child, so all sudo activity lives in ONE
// credential-ticket domain.
//
// sudo keys its "authenticated recently" tickets to the invoking process's
// controlling terminal (timestamp_type=tty, the default) — and to the parent
// pid when there is no terminal. That made prompting environment-dependent:
// detached children (topgrade's internal sudos) each got their own ppid-keyed
// ticket and re-asked repeatedly, while tty-attached runs depended on which
// terminal archmaid happened to start from.
//
// Instead, archmaid opens one pseudo-terminal at startup and attaches every
// step child (and the sudo probe/prime/keep-alive) to it as the controlling
// tty. One successful authentication creates a ticket for this pty; every
// other sudo — ours or one a tool like topgrade/yay spawns internally — finds
// it and doesn't prompt. The keep-alive refreshes the same domain. Password
// prompts that do happen route to the askpass popup (`sudo -A`); nothing can
// prompt on the real terminal because no child ever has it as its ctty.
//
// The master side is drained and discarded: step output is captured via
// stdout/stderr pipes as before, so nothing user-visible goes through the pty.

var (
	ptyMaster *os.File
	ptySlave  *os.File
)

// startPTY opens the shared pty and drains its master. Best-effort: on failure
// children simply run unattached (sudo -A popups still work; tickets may not be
// shared).
func startPTY() {
	m, s, err := pty.Open()
	if err != nil {
		return
	}
	ptyMaster, ptySlave = m, s
	go io.Copy(io.Discard, m) // keep children from blocking on tty writes
}

func closePTY() {
	if ptySlave != nil {
		ptySlave.Close()
	}
	if ptyMaster != nil {
		ptyMaster.Close()
	}
}

// attachPTY makes the shared pty cmd's controlling terminal (fd 3 in the child
// via ExtraFiles; Setsid+Setctty perform the attach). A no-op when the pty
// isn't available (tests, startPTY failure).
func attachPTY(cmd *exec.Cmd) {
	if ptySlave == nil {
		return
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, ptySlave)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    2 + len(cmd.ExtraFiles), // ExtraFiles start at fd 3
	}
}
