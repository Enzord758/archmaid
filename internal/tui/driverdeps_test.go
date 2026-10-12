package tui

// driver.Deps as the binary wires them (driverDeps in adapter.go), for the
// tests that run the real Driver from an askpass Server they started:
// TestTranscriptRealDriver and the askpass socket tests.

import (
	"context"
	"reflect"
	"testing"

	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/privilege"
)

// tuiDriverDeps is driverDeps with srv's requests, as main hands them over.
// srv may be nil (askpass couldn't start): sudo steps then run `sudo -n`.
// Its RefreshUpdates is TestMain's fake, as for every Driver the tests build.
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

// TestDriverDepsRefreshUpdates: the binary's Driver refreshes the widget
// with driver.RefreshUpdates, and every Driver the tests build gets
// TestMain's fake instead (the real one reaches systemctl and the installed
// check, which testenv traps).
func TestDriverDepsRefreshUpdates(t *testing.T) {
	ptr := func(f func(context.Context) error) uintptr { return reflect.ValueOf(f).Pointer() }
	if prodRefreshUpdates == nil || ptr(prodRefreshUpdates) != ptr(driver.RefreshUpdates) {
		t.Error("the binary's refreshUpdates is not driver.RefreshUpdates")
	}
	got := driverDeps(nil).RefreshUpdates
	if got == nil || ptr(got) == ptr(driver.RefreshUpdates) {
		t.Fatal("a test's driverDeps has the real RefreshUpdates: TestMain's fake is gone")
	}
	before := fakeRefreshes.Load()
	if err := got(context.Background()); err != nil || fakeRefreshes.Load() != before+1 {
		t.Errorf("driverDeps' RefreshUpdates is not TestMain's fake (err %v)", err)
	}
}
