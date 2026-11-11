package tui

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// startFullCleanupModel is a model started as `archmaid --full-cleanup`
// starts it, on a fakeDriver, with Init's command run.
func startFullCleanupModel(t *testing.T) (model, *fakeDriver) {
	t.Helper()
	StartFullCleanup(true)
	t.Cleanup(func() { StartFullCleanup(false) })
	f := &fakeDriver{}
	m := sizedModel()
	m.startAction = startAction
	m.eng = newEngineState(f)
	m.Init() // Init's own Cmds tick and refresh; the Start goes out directly
	return m, f
}

// --full-cleanup asks the engine for Full Cleanup before any key: exactly the
// Start the menu's Enter sends, so its gate is the first thing shown, and
// nothing else has been sent (no Confirm: nothing runs before the user answers).
func TestStartFullCleanupOpensTheGateFirst(t *testing.T) {
	m, f := startFullCleanupModel(t)
	if len(f.sent) != 1 {
		t.Fatalf("sent %v, want exactly one Start", f.sent)
	}
	if s, ok := f.sent[0].(engine.Start); !ok || s.Action != actions.IDFullCleanup {
		t.Fatalf("sent %v, want Start{%s}", f.sent[0], actions.IDFullCleanup)
	}
	m = feed(m, engine.NeedsConfirm{ID: 1, Kind: engine.ConfirmGate, Msg: "Proceed with full cleanup?"})
	if !m.gateActive || !strings.Contains(m.View(), "Proceed with full cleanup") {
		t.Fatalf("no gate shown:\n%s", m.View())
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent %v before the user answered", f.sent)
	}
}

// Declining the gate is as after picking Full Cleanup by hand: the menu.
func TestStartFullCleanupDeclineLandsOnTheMenu(t *testing.T) {
	m, f := startFullCleanupModel(t)
	m = feed(m, engine.NeedsConfirm{ID: 1, Kind: engine.ConfirmGate, Msg: "Proceed with full cleanup?"})
	m, _ = press(m, "n")
	if c, ok := lastSent(t, f).(engine.Confirm); !ok || c.Yes {
		t.Fatalf("last sent %v, want Confirm{Yes: false}", lastSent(t, f))
	}
	m = feed(m, engine.PromptClosed{ID: 1}, engine.Finished{Outcome: engine.OutcomeCancelled})
	if m.screen != screenMenu || m.gateActive {
		t.Fatalf("screen %d gate %v after declining, want the menu", m.screen, m.gateActive)
	}
}

// Without the flag nothing is sent at start-up.
func TestNoStartActionSendsNothing(t *testing.T) {
	f := &fakeDriver{}
	m := sizedModel()
	m.eng = newEngineState(f)
	m.Init()
	if len(f.sent) != 0 {
		t.Fatalf("sent %v at start-up without the flag", f.sent)
	}
}
