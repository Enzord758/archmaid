// Package privtest gives other packages' tests what internal/privilege keeps
// out of its production API: a Request over a connection of their own, and a
// fake view of the system for Request.Verify. It must only be imported from
// _test.go files; outside a test binary every function panics.
package privtest

import (
	"net"
	"testing"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/internal/seam"
	"github.com/Enzord758/archmaid/internal/proc"
)

func testOnly() {
	if !testing.Testing() {
		panic("privtest is for tests only")
	}
}

// NewRequest is a Request over conn (net.Pipe, say) from a fake peer, as a
// Server would deliver it. It is unverified like any other: Verify it before
// Answer.
func NewRequest(conn net.Conn, prompt string, peer privilege.Peer) *privilege.Request {
	testOnly()
	return seam.NewRequest(conn, prompt, peer.Pid, peer.UID, peer.Start).(*privilege.Request)
}

// FakeSystem makes Request.Verify (and servers started meanwhile) see uid as
// ours, exe as archmaid's binary ("" keeps os.Executable) and readProc as
// /proc, until t ends.
func FakeSystem(t testing.TB, uid func() int, exe string, readProc func(pid int) (proc.Info, error)) {
	t.Helper()
	testOnly()
	t.Cleanup(seam.SetSystem(uid, exe, readProc))
}
