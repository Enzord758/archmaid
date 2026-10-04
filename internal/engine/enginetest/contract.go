// Package enginetest holds test helpers shared by the engine's tests and
// the front ends' (the stub Driver's, the transcript harness's
// sessionTarget). It is imported only from _test.go files.
package enginetest

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// CheckContract holds an event sequence — one or more whole actions — to
// the ordering rules in the engine package doc, reporting every breach on
// t: nothing names a step before QueueStarted or at an index the plan (as
// moved by StepInserted) doesn't put it at; one step runs at a time and only
// the running step has output; a step ends once, with StepDone or Aborted,
// never as StepDone{Aborted}, and a step that never ran is settled only as
// Skipped or AuthRefused; prompt IDs are never reused; Finished ends every
// action (only PhaseChanged may follow before the next one starts), and a
// completed one has every step settled.
func CheckContract(t testing.TB, evs []engine.Event) {
	t.Helper()
	var (
		plan     []string
		queued   bool
		running  = ""
		settled  = map[string]bool{}
		started  = map[string]bool{}
		prompts  = map[engine.PromptID]bool{}
		finished = false
	)
	at := func(i int, id string, idx int) {
		if !queued {
			t.Errorf("event %d names step %s before QueueStarted", i, id)
		} else if idx < 0 || idx >= len(plan) || plan[idx] != id {
			t.Errorf("event %d: step %s is not at index %d of %v", i, id, idx, plan)
		}
		if settled[id] {
			t.Errorf("event %d names step %s after it ended", i, id)
		}
	}
	for i, ev := range evs {
		if finished {
			switch ev.(type) {
			case engine.PhaseChanged:
				continue
			case engine.QueueStarted, engine.Notice, engine.NeedsConfirm, engine.NeedsPassword,
				engine.NeedsSelection, engine.OutputReport:
				// the next action
				plan, queued, running, finished = nil, false, "", false
				settled, started = map[string]bool{}, map[string]bool{}
			default:
				t.Errorf("event %d (%T) after Finished", i, ev)
				continue
			}
		}
		if id := promptID(ev); id != 0 {
			if prompts[id] {
				t.Errorf("event %d reuses prompt ID %d", i, id)
			}
			prompts[id] = true
		}
		switch ev := ev.(type) {
		case engine.QueueStarted:
			queued = true
			for _, s := range ev.Steps {
				plan = append(plan, s.ID)
			}
		case engine.StepInserted:
			if ev.Index < 0 || ev.Index > len(plan) {
				t.Fatalf("event %d inserts at %d into %d steps", i, ev.Index, len(plan))
			}
			plan = append(plan[:ev.Index], append([]string{ev.Step.ID}, plan[ev.Index:]...)...)
		case engine.StepStarted:
			at(i, ev.ID, ev.Index)
			if running != "" {
				t.Errorf("event %d starts %s while %s runs", i, ev.ID, running)
			}
			running, started[ev.ID] = ev.ID, true
		case engine.OutputChunk:
			at(i, ev.ID, ev.Index)
			if running != ev.ID {
				t.Errorf("event %d: output for %s, which isn't running", i, ev.ID)
			}
		case engine.StepDone:
			at(i, ev.ID, ev.Index)
			switch {
			case ev.Status == steps.Aborted || ev.Status == steps.Running || ev.Status == steps.Pending:
				t.Errorf("event %d: StepDone with status %d", i, ev.Status)
			case started[ev.ID] && running != ev.ID:
				t.Errorf("event %d: %s done but not running", i, ev.ID)
			case !started[ev.ID] && ev.Status != steps.Skipped && ev.Status != steps.AuthRefused:
				t.Errorf("event %d: %s settled without running, status %d", i, ev.ID, ev.Status)
			}
			settled[ev.ID] = true
			if running == ev.ID {
				running = ""
			}
		case engine.Aborted:
			at(i, ev.ID, ev.Index)
			if running != ev.ID {
				t.Errorf("event %d: aborted %s, which isn't running", i, ev.ID)
			}
			settled[ev.ID], running = true, ""
		case engine.Finished:
			finished = true
			if ev.Outcome == engine.OutcomeCompleted {
				for _, id := range plan {
					if !settled[id] {
						t.Errorf("completed with step %s never settled", id)
					}
				}
			}
		}
	}
	if !finished {
		t.Error("no Finished event")
	}
}

// promptID is the prompt ev opens, or 0.
func promptID(ev engine.Event) engine.PromptID {
	switch ev := ev.(type) {
	case engine.NeedsConfirm:
		return ev.ID
	case engine.NeedsPassword:
		return ev.ID
	case engine.NeedsSelection:
		return ev.ID
	case engine.OutputReport:
		return ev.ID
	}
	return 0
}
