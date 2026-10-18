package tui

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A wrong password on the late prompt (lateRoot) asks again over the same
// run screen; the right one after it goes back to the run, not to a stuck
// password screen over the menu.
func TestEngineLatePasswordWrongThenRight(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.QueueStarted{Action: "clearcaches", Steps: []engine.PlanStep{{ID: "u", Title: "U…", LogLabel: "U"}, {ID: "w", Title: "W…", LogLabel: "W", NeedsPriv: true}}})
	m = feed(m, engine.StepStarted{ID: "u", Index: 0}, engine.StepDone{ID: "u", Index: 0, Status: steps.OK})
	m = feed(m, engine.NeedsPassword{ID: 5, Kind: engine.PasswordUpFront, AttemptsLeft: 3})
	m = feed(m, engine.AuthWaiting{Kind: engine.WaitPasswordCheck}, engine.AuthResolved{AttemptsLeft: 2},
		engine.NeedsPassword{ID: 6, Kind: engine.PasswordUpFront, AttemptsLeft: 2, Error: "Incorrect password"})
	if m.screen != screenPassword || m.bg != screenRunning {
		t.Fatalf("second prompt: screen %d bg %d", m.screen, m.bg)
	}
	m = feed(m, engine.AuthWaiting{Kind: engine.WaitPasswordCheck}, engine.AuthResolved{Granted: true})
	if m.screen != screenRunning {
		t.Errorf("granted after a wrong try: screen %d", m.screen)
	}
}
