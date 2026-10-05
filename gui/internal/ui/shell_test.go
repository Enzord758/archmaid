package ui

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
)

// TestRunBadge: the Run entry's badge is the step running of the plan's
// while busy, "done" once the run finished, and nothing before.
func TestRunBadge(t *testing.T) {
	m := runview.NewModel()
	if got := runBadge(false, m); got != "" {
		t.Errorf("idle: %q", got)
	}
	m.Apply([]engine.Event{engine.QueueStarted{Steps: []engine.PlanStep{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Started: time.Now()}})
	if got := runBadge(true, m); got != "01/03" {
		t.Errorf("started: %q", got)
	}
	m.Apply([]engine.Event{engine.StepStarted{ID: "a", Index: 0}, engine.StepDone{ID: "a", Index: 0}, engine.StepStarted{ID: "b", Index: 1}})
	if got := runBadge(true, m); got != "02/03" {
		t.Errorf("step 2: %q", got)
	}
	m.Apply([]engine.Event{engine.Finished{}})
	if got := runBadge(false, m); got != "done" {
		t.Errorf("finished: %q", got)
	}
}
