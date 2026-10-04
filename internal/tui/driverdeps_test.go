package tui

// driver.Deps as the binary wires them (driverDeps in adapter.go), for the
// tests that run the real Driver from an askpass Server they started:
// TestTranscriptRealDriver and the askpass socket tests.

import (
	"testing"

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

// TestTUIDoesNotDetach: the TUI's Driver runs its steps as plain children,
// in its session (internal/proc's TestNoSetsid): only the GUI detaches
// them (driver.Deps.Detach), and the TUI's sudo ticket depends on it.
func TestTUIDoesNotDetach(t *testing.T) {
	if driverDeps(nil).Detach {
		t.Fatal("the TUI's driver.Deps set Detach: its steps must share its session for the sudo ticket")
	}
}
