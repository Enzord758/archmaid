package runview

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestPolkitProgress: under polkit a helper step's row says it waits for
// the dialog until AuthResolved; the faillock count read before it warns
// on the row (and once in a toast) when one more failure locks the
// account; a locked account's step is refused without starting and says
// until when; a refusal reads "Not authorised", not an error.
func TestPolkitProgress(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{engine.QueueStarted{Action: "a", Steps: []engine.PlanStep{{ID: "p", NeedsPriv: true}, {ID: "q", NeedsPriv: true}, {ID: "r", NeedsPriv: true}}}})
	last := engine.FaillockState{Failures: 2, Deny: 3}
	c := m.Apply([]engine.Event{
		last,
		engine.StepStarted{ID: "p", Index: 0},
		engine.AuthWaiting{Kind: engine.WaitPolkit, Step: "p"},
	})
	if r := m.Rows[0]; !r.Waiting || r.Subtitle() != "Waiting for authorisation… · one more failure locks your account" {
		t.Errorf("waiting row: %+v %q", r, r.Subtitle())
	}
	if !slices.Equal(c.Toasts, []string{"One more failed authorisation locks your account"}) || !slices.Equal(c.Rows, []int{0}) {
		t.Errorf("toasts %q rows %v", c.Toasts, c.Rows)
	}
	m.Apply([]engine.Event{engine.AuthResolved{}, engine.StepDone{ID: "p", Index: 0, Status: steps.AuthRefused}})
	if r := m.Rows[0]; r.Waiting || r.Subtitle() != "Not authorised" || m.Auth != nil {
		t.Errorf("refused row: %+v %q", r, r.Subtitle())
	}
	// The same count again: no second toast.
	c = m.Apply([]engine.Event{last, engine.StepStarted{ID: "q", Index: 1}, engine.AuthWaiting{Kind: engine.WaitPolkit, Step: "q"}, engine.AuthResolved{Granted: true}})
	if len(c.Toasts) != 0 || m.Rows[1].Waiting || m.Rows[1].Subtitle() != "Running" {
		t.Errorf("granted: toasts %q row %+v %q", c.Toasts, m.Rows[1], m.Rows[1].Subtitle())
	}
	unlock := time.Date(2026, 10, 4, 15, 4, 0, 0, time.Local)
	c = m.Apply([]engine.Event{
		engine.StepDone{ID: "q", Index: 1, Status: steps.OK},
		engine.FaillockState{Failures: 3, Deny: 3, Locked: true, Unlock: unlock},
		engine.StepDone{ID: "r", Index: 2, Status: steps.AuthRefused},
	})
	if got := m.Rows[2].Subtitle(); got != "Not authorised · account locked until 15:04" {
		t.Errorf("locked row %q", got)
	}
	if m.Rows[1].Faillock == nil || m.Rows[1].Faillock.Locked {
		t.Errorf("the locked count landed on the wrong row: %+v", m.Rows[1])
	}
	if len(c.Toasts) != 1 || !strings.Contains(c.Toasts[0], "locked until 15:04") {
		t.Errorf("toasts %q", c.Toasts)
	}
}
