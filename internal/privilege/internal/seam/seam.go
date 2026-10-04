// Package seam carries internal/privilege's test hooks to privtest, without
// exporting them from privilege itself. Being internal to internal/privilege,
// only privilege and its subpackages can import it: production code elsewhere
// can't make a Request or swap the process table Request.Verify checks
// against. privilege fills the hooks in when it initialises.
package seam

import (
	"net"

	"github.com/Enzord758/archmaid/internal/proc"
)

// NewRequest is privilege.newRequest, with the peer spelt out (seam can't
// name privilege's types): a Request over conn from the given peer, as a
// Server would deliver it. It returns a *privilege.Request.
var NewRequest func(conn net.Conn, prompt string, pid, uid int, start uint64) any

// SetSystem makes Request.Verify and Listen check against uid, exe and
// readProc instead of os.Getuid, os.Executable and proc.Read, until the
// returned restore runs.
var SetSystem func(uid func() int, exe string, readProc func(pid int) (proc.Info, error)) (restore func())
