package tui

// driver.Deps as the binary wires them (driverDeps in adapter.go), for the
// tests that run the real Driver from an askpass Server they started:
// TestTranscriptRealDriver and the askpass socket tests.

import (
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/privilege"
)

// tuiDriverDeps is driverDeps with srv's requests, as main hands them over.
// srv may be nil (askpass couldn't start): sudo steps then run `sudo -n`.
func tuiDriverDeps(srv *privilege.Server) driver.Deps {
	var reqs <-chan *privilege.Request
	if srv != nil {
		reqs = srv.Requests()
	}
	return driverDeps(reqs)
}
