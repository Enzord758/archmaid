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

	// A GUI that goes away must not kill us mid-transaction: with SIGPIPE
	// ignored a write to the closed stdout just fails, the Emitter stops
	// writing, and we keep draining the child until it finishes.
	signal.Ignore(syscall.SIGPIPE)

	o := helper.DefaultOptions()
	o.Euid = os.Geteuid()
	o.CallerUID = caller
	o.Dev = devBuild
	if devBuild {
		o.LockPath = devLockPath()
	}
	os.Exit(helper.Run(os.Args[1:], o))
}

// callerUID returns PKEXEC_UID, else SUDO_UID, else "".
func callerUID(getenv func(string) string) string {
	if v := getenv("PKEXEC_UID"); v != "" {
		return v
	}
	return getenv("SUDO_UID")
}
