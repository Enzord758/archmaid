// Command archmaid-helper is archmaid's root helper. It is installed at
// /usr/lib/archmaid/archmaid-helper (0755 root:root, never setuid) and run
// through pkexec, one polkit action per verb:
//
//	pkexec /usr/lib/archmaid/archmaid-helper <verb> [args]
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
	ignoreSignals()

	o := helper.DefaultOptions()
	o.Euid = os.Geteuid()
	o.CallerUID = caller
	o.Dev = devBuild
	if devBuild {
		o.LockPath = devLockPath()
	}
	os.Exit(helper.Run(os.Args[1:], o))
}

// ignoredSignals would otherwise end (or, for the job-control ones, stop)
// the helper before it writes its result line, possibly with pacman
// mid-transaction. SIGQUIT is Ctrl-\, SIGTSTP Ctrl-Z, and SIGTTIN/SIGTTOU
// stop a background job that touches its terminal.
var ignoredSignals = []os.Signal{
	syscall.SIGPIPE, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM,
	syscall.SIGQUIT, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU,
}

// ignoreSignals makes the helper finish its run whatever happens to the
// terminal or GUI that started it. With SIGPIPE ignored, a write to a
// closed stdout just fails, the Emitter stops writing, and we keep draining
// the child until it finishes. SIGINT (Ctrl-C), SIGHUP (the terminal
// closed) and SIGTERM are ignored for the same reason: every run ends with
// its result line. The children are out of reach anyway: each runs in a
// session of its own (helper.ExecRunner). The dispositions are SIG_IGN, so
// the children inherit them; pacman installs its own SIGINT/SIGHUP handlers
// regardless. Only root can still stop the helper, with SIGKILL.
func ignoreSignals() { signal.Ignore(ignoredSignals...) }

// callerUID returns PKEXEC_UID, else SUDO_UID, else "".
func callerUID(getenv func(string) string) string {
	if v := getenv("PKEXEC_UID"); v != "" {
		return v
	}
	return getenv("SUDO_UID")
}
