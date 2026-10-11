package runview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestFinishedText: the end of a run reads as last-run.json judges it, not
// as the engine's Outcome alone: a completed queue with a failed step is a
// failed run, one with refused steps an incomplete one.
func TestFinishedText(t *testing.T) {
	for _, c := range []struct {
		f             engine.Finished
		status, toast string
	}{
		{engine.Finished{Outcome: engine.OutcomeCompleted}, "Finished: completed", "Run completed"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 1}, "Finished: failed (1 step)", "Run finished with 1 failed step"},
		// A step failed and others were refused: both are said, as the
		// Overview and the DMS widget say them.
		{engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 2, Refused: 1}, "Finished: failed (2 steps) · 1 step not authorised", "Run finished with 2 failed steps · 1 step not authorised"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 1, Refused: 5}, "Finished: failed (1 step) · 5 steps not authorised", "Run finished with 1 failed step · 5 steps not authorised"},
		{engine.Finished{Outcome: engine.OutcomeAborted, Errs: 1, Refused: 5}, "Finished: aborted (1 step failed) · 5 steps not authorised", "Run aborted, 1 step failed · 5 steps not authorised"},
		{engine.Finished{Outcome: engine.OutcomeAborted, Refused: 1}, "Finished: aborted · 1 step not authorised", "Run aborted · 1 step not authorised"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1, Skipped: 1}, "Finished: incomplete (1 step not authorised)", "Run incomplete: 1 step was not authorised"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 6, Skipped: 6}, "Finished: incomplete (6 steps not authorised)", "Run incomplete: 6 steps were not authorised"},
		{engine.Finished{Outcome: engine.OutcomeAborted, NotRun: 3}, "Finished: aborted", "Run aborted"},
		{engine.Finished{Outcome: engine.OutcomeAborted, Errs: 1}, "Finished: aborted (1 step failed)", "Run aborted, 1 step failed"},
		{engine.Finished{Outcome: engine.OutcomeCancelled}, "Finished: cancelled", "Run cancelled, nothing ran"},
		{engine.Finished{Outcome: engine.OutcomeNotice}, "Finished", "Run finished"},
		{engine.Finished{Outcome: engine.OutcomeReport}, "Finished", "Run finished"},
	} {
		if s, toast := NewModel().FinishedText(c.f); s != c.status || toast != c.toast {
			t.Errorf("%+v: %q / %q, want %q / %q", c.f, s, toast, c.status, c.toast)
		}
	}
}

// TestEndToast: an action that ended in a notice is told by what the
// notice says (the services discovery's "No enabled-but-inactive services
// found.", or why it couldn't look), never by a bare "Run finished"; any
// other end is FinishedText's toast, a stale notice notwithstanding.
func TestEndToast(t *testing.T) {
	m := NewModel()
	none := "No enabled-but-inactive services found."
	m.Apply([]engine.Event{
		engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Finding unused services…"},
		engine.Notice{Text: none},
		engine.Finished{Action: "services", Outcome: engine.OutcomeNotice},
		engine.PhaseChanged{Phase: engine.PhaseIdle},
	})
	if got := m.EndToast(*m.Finished); got != none {
		t.Errorf("notice: toast %q, want %q", got, none)
	}
	if got := m.EndToast(engine.Finished{Outcome: engine.OutcomeCancelled}); got != "Run cancelled, nothing ran" {
		t.Errorf("cancelled after a notice: toast %q", got)
	}
	if got := NewModel().EndToast(engine.Finished{Outcome: engine.OutcomeNotice}); got != "Run finished" {
		t.Errorf("a notice without text: toast %q", got)
	}
}

// TestShowReport: a report's page is the action's own and empty, not the
// last run's steps and line count.
func TestShowReport(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{
		engine.QueueStarted{Action: "a", Steps: []engine.PlanStep{{ID: "s1", Title: "S1"}}},
		engine.StepStarted{ID: "s1"},
		engine.OutputChunk{ID: "s1", Lines: []string{"x", "y"}},
		engine.StepDone{ID: "s1"},
		engine.Finished{Action: "a", Outcome: engine.OutcomeCompleted},
	})
	c := m.ShowReport("auraudit")
	if !c.Reset || !c.Status || c.OutRemove == 0 {
		t.Errorf("change %+v: want a reset that removes the old lines", c)
	}
	if m.Action != "auraudit" || len(m.Rows) != 0 || m.Lines != 0 || m.Finished != nil || len(m.Output()) != 0 {
		t.Errorf("page: action %q rows %d lines %d finished %v", m.Action, len(m.Rows), m.Lines, m.Finished)
	}
}

// TestFinishedTextNotAuthorised: a queue whose every step was refused ran
// nothing, which last-run.json records as cancelled with steps_refused N;
// the Run page and the toast say what the Overview and the DMS widget say.
// A queue where any step ran is still incomplete.
func TestFinishedTextNotAuthorised(t *testing.T) {
	plan := []engine.PlanStep{{ID: "a", NeedsPriv: true}, {ID: "b", NeedsPriv: true}}
	run := func(evs ...engine.Event) *Model {
		m := NewModel()
		m.Apply(append([]engine.Event{engine.QueueStarted{Steps: plan}}, evs...))
		return m
	}
	m := run(engine.StepDone{ID: "a", Index: 0, Status: steps.AuthRefused}, engine.StepDone{ID: "b", Index: 1, Status: steps.AuthRefused})
	f := engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 2, Skipped: 2}
	s, toast := m.FinishedText(f)
	if s != "Finished: not authorised · nothing ran (2 steps not authorised)" || toast != "Run not authorised · nothing ran (2 steps not authorised)" {
		t.Errorf("all refused: %q / %q", s, toast)
	}
	if got := m.EndToast(f); got != toast {
		t.Errorf("EndToast %q, want %q", got, toast)
	}
	m.Apply([]engine.Event{f})
	if h := m.Header(); h.Kicker != s {
		t.Errorf("header kicker %q, want %q", h.Kicker, s)
	}
	one := run(engine.StepDone{ID: "a", Index: 0, Status: steps.AuthRefused})
	if s, _ := one.FinishedText(engine.Finished{Outcome: engine.OutcomeCancelled, Refused: 1}); s != "Finished: not authorised · nothing ran (1 step not authorised)" {
		t.Errorf("one refused, cancelled: %q", s)
	}
	// One step ran: incomplete, as before.
	ran := run(engine.StepStarted{ID: "a", Index: 0}, engine.StepDone{ID: "a", Index: 0, Status: steps.OK}, engine.StepDone{ID: "b", Index: 1, Status: steps.AuthRefused})
	if s, _ := ran.FinishedText(engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1, Skipped: 1}); s != "Finished: incomplete (1 step not authorised)" {
		t.Errorf("one ran: %q", s)
	}
	// A plain cancel stays cancelled.
	if s, _ := one.FinishedText(engine.Finished{Outcome: engine.OutcomeCancelled}); s != "Finished: cancelled" {
		t.Errorf("plain cancel: %q", s)
	}
}
