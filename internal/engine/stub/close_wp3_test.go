package stub

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// closesPromptly closes d from another goroutine and fails if Close hangs.
func closesPromptly(t *testing.T, d *Driver) {
	t.Helper()
	closed := make(chan struct{})
	go func() { d.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked")
	}
	for range d.Events() { // Events ends
	}
}

// TestCloseWhileWaitingForACue: Close while the replay sits in a cue's wait
// stops it there; the cue's event is never played.
func TestCloseWhileWaitingForACue(t *testing.T) {
	d := New(Script{
		{Event: engine.StepStarted{ID: "s"}},
		{After: time.Hour, Event: engine.StepDone{ID: "s", Status: steps.OK}},
	}, Options{Clock: stuckClock{}})
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.StepStarted{ID: "s"})
	closesPromptly(t, d)
}

// TestCloseWhileAwaitingAnAnswer: Close while a prompt waits for its answer
// stops the replay there, whether or not the prompt has a branch.
func TestCloseWhileAwaitingAnAnswer(t *testing.T) {
	d := New(twoPrompts(), Options{Clock: NewFakeClock(t0)})
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsConfirm{ID: 1, Msg: "first?"})
	closesPromptly(t, d)
}
