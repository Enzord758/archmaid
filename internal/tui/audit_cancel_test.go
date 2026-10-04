package tui

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── aborting the audit step ───────────────────────────────────────────────────
//
// The audit runs in-process, so an abort can't signal it: it has to be
// cancelled. These tests hold the RPC open on a fake AUR that never answers
// and check that an abort ends the step at once and leaves nothing running
// behind it. The deadline and the cancel inside the audit itself are tested
// in internal/audit; the helpers here are shared with the prefetch tests.

// cancelSlack is how soon after a cancel the audit must have returned.
const cancelSlack = 100 * time.Millisecond

// hangingAUR points aurRPCURL at a server that takes every request and never
// answers it. Each arrival is signalled on the returned channel; a handler
// returns only once its client has gone, so a leaked connection would leave a
// handler goroutine behind too.
func hangingAUR(t *testing.T) (arrived <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{}, 16)
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		// Read the form first: net/http only watches for the client hanging
		// up (cancelling r.Context) once the request body is consumed.
		r.ParseForm()
		ch <- struct{}{}
		<-r.Context().Done()
	})
	return ch
}

func waitArrived(t *testing.T, arrived <-chan struct{}) {
	t.Helper()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the audit's request never reached the fake AUR")
	}
}

// settleGoroutines waits briefly for the goroutine count to fall back to
// base, then fails if it hasn't.
func settleGoroutines(t *testing.T, base int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines: %d before, %d after %s (leaked %d)\n%s",
			base, n, what, n-base, buf[:runtime.Stack(buf, true)])
	}
}

// awaitFinished reads evs until the action's Finished, failing after 5 s.
func awaitFinished(t *testing.T, evs <-chan engine.Event) engine.Finished {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-evs:
			if !ok {
				t.Fatal("Events closed without a Finished")
			}
			if f, ok := ev.(engine.Finished); ok {
				return f
			}
		case <-timeout:
			t.Fatal("no Finished")
		}
	}
}

// TestAbortCancelsAuditStep: aborting while the audit step waits on the AUR
// (through its prefetch, as the binary runs it: the real Driver from
// newDriver) ends the step at once, and nothing it started (connection,
// handler, pacman) is left running once the Driver is closed.
func TestAbortCancelsAuditStep(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "victim\n")
	arrived := hangingAUR(t)
	probe := actions.Action{ID: "auditonly", Label: "Audit only", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{auditStep()}
	}}
	old := actions.Menu
	actions.Menu = append(slices.Clone(old), probe)
	t.Cleanup(func() { actions.Menu = old })
	base := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		d := newDriver(engine.New(engine.Options{Env: actionEnv()}), nil)
		d.Send(engine.Start{Action: probe.ID})
		waitArrived(t, arrived)

		start := time.Now()
		d.Send(engine.Abort{})
		fin := awaitFinished(t, d.Events())
		if el := time.Since(start); el > cancelSlack {
			t.Fatalf("the audit step ended %s after the abort, want within %s", el, cancelSlack)
		}
		if fin.Outcome != engine.OutcomeAborted || fin.Errs != 0 {
			t.Fatalf("finished %+v, want aborted with no failure", fin)
		}
		d.Close()
	}
	settleGoroutines(t, base, "3 aborted audits")
}

// TestCloseStopsMenuAudit: quitting while "Audit AUR packages" waits on
// the AUR (the Driver's Close, as main does after the program) ends the
// lookup at once and writes no snapshot. The menu audit used to run on
// context.Background(), so Close waited for the audit's deadline, and the
// audit could save its snapshot after the user had quit.
func TestCloseStopsMenuAudit(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "victim\n")
	arrived := hangingAUR(t)
	old := auditDeadline
	auditDeadline = 3 * time.Second // the old code's wait, kept short
	t.Cleanup(func() { auditDeadline = old })
	base := runtime.NumGoroutine()

	d := newDriver(engine.New(engine.Options{Env: actionEnv()}), nil)
	d.Send(engine.Start{Action: actions.IDAURAudit})
	waitArrived(t, arrived)

	start := time.Now()
	d.Close()
	if el := time.Since(start); el > cancelSlack {
		t.Fatalf("Close returned %s after it was called, want within %s", el, cancelSlack)
	}
	if _, err := os.Stat(baselinePath()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the stopped audit wrote its snapshot (stat: %v)", err)
	}
	settleGoroutines(t, base, "a closed menu audit")
}
