// Command archmaid-helper is archmaid's root helper. It is installed at
// /usr/lib/archmaid/archmaid-helper (0755 root:root, never setuid) and run
// through pkexec, one polkit action per verb:
//
//	pkexec --disable-internal-agent /usr/lib/archmaid/archmaid-helper <verb> [args]
//
// (pkexec takes its own option; the helper's arguments start at the verb.)
//
// It runs a fixed set of maintenance commands by absolute path (never a
// shell), validates and recomputes its own input, and reports progress as
// JSON lines on stdout. See README.md for verbs, schema, exit codes and the
// threat model.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
)

func main() {
	// Capture the invoking user before the environment is wiped.
	caller := callerUID(os.Getenv)

	hardenProcess()
	catchSignals()

	o := helper.DefaultOptions()
	o.Euid = os.Geteuid()
	o.CallerUID = caller
	o.Dev = devBuild
	if devBuild {
		o.LockPath = devLockPath()
	}
	os.Exit(helper.Run(os.Args[1:], o))
}

// caughtSignals would otherwise end (or, for the job-control ones, stop)
// the helper before it writes its result line, possibly with pacman
// mid-transaction. SIGQUIT is Ctrl-\, SIGTSTP Ctrl-Z, and SIGTTIN/SIGTTOU
// stop a background job that touches its terminal.
var caughtSignals = []os.Signal{
	syscall.SIGPIPE, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM,
	syscall.SIGQUIT, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU,
}

// catchSignals makes the helper finish its run whatever happens to the
// terminal or GUI that started it: every run ends with its result line.
// The signals are caught and dropped. SIGKILL and SIGSTOP can't be caught,
// and not only root can send them: pkexec leaves the helper in
// archmaid-gui's user cgroup, so the user's cgroup.kill/cgroup.freeze,
// `systemctl --user` stopping the scope, a logout or systemd-oomd can kill
// or freeze it too.
//
// They are caught, not ignored (SIG_IGN), because an ignored signal stays
// ignored across exec: pacman, its install scriptlets and hooks would all
// start with SIGINT/SIGTERM/SIGHUP ignored, so a hook's own `timeout`
// couldn't stop a hung child and a script's `trap` on them would silently do
// nothing. A caught signal is reset to the default on exec, so every child
// starts clean. The children are out of the terminal's reach anyway: each
// runs in a session of its own (helper.ExecRunner).
//
// With SIGPIPE caught, Go doesn't exit when a write to stdout hits a closed
// pipe (the GUI died): the write just fails with EPIPE, the Emitter stops
// writing, and the helper keeps draining the child until it finishes.
//
// One corner: run in the background of a terminal set to `stty tostop`, a
// write to that terminal is retried on every caught SIGTTOU, so the helper
// spins until it is brought to the foreground instead of being stopped.
// The GUI uses pipes, and `tostop` is off by default.
func catchSignals() {
	ch := make(chan os.Signal, 16)
	signal.Notify(ch, caughtSignals...)
	go func() {
		for range ch {
		}
	}()
}

// callerUID returns PKEXEC_UID, else SUDO_UID, else "".
func callerUID(getenv func(string) string) string {
	if v := getenv("PKEXEC_UID"); v != "" {
		return v
	}
	return getenv("SUDO_UID")
}
