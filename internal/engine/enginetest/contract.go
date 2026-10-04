// Package enginetest holds test helpers shared by the engine's tests and
// the front ends' (the stub Driver's, the transcript harness's
// sessionTarget). It is imported only from _test.go files.
package enginetest

import (
	"slices"
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
// Skipped or AuthRefused; a step's confirm names a step of the plan that
// hasn't run, and may be asked ahead of its step (a combined run asks all
// its confirms first), but no step at or after it starts while it is
// open; the plan changes (StepInserted, PlanChanged) only between
// steps — never while a step runs or a step's confirm is open, so what a
// result changes comes after that step's StepDone and before the next
// step's events — and only at steps that haven't run; an AuthWaiting is
// resolved (AuthResolved) before anything starts, asks or finishes, and
// nothing resolves what isn't waiting; prompt IDs are never reused; Finished ends every action (only PhaseChanged and StatusUpdated
// may follow before the next one starts), and a completed one has every
// step settled.
func CheckContract(t testing.TB, evs []engine.Event) {
	t.Helper()
	c := NewChecker(t)
	c.Check(evs)
	c.Done()
}

// Checker is CheckContract fed in pieces: a harness passes it each
// Output's events as they come (Check), so a breach is reported at the
// Output that made it, and calls Done once the stream is over.
type Checker struct {
	t        testing.TB
	n        int // events seen so far: the next one's index in the stream
	plan     []string
	queued   bool
	running  string          // the step between its StepStarted and its end
	asking   string          // the step whose confirm is open
	asked    map[string]bool // steps whose confirm was asked, not yet started or ended
	waiting  bool            // an AuthWaiting has no AuthResolved yet
	settled  map[string]bool
	started  map[string]bool
	prompts  map[engine.PromptID]bool
	finished bool
}

// NewChecker starts a Checker that reports on t.
func NewChecker(t testing.TB) *Checker {
	return &Checker{t: t, settled: map[string]bool{}, started: map[string]bool{},
		prompts: map[engine.PromptID]bool{}, asked: map[string]bool{}}
}

// Done reports a stream that never finished its action.
func (c *Checker) Done() {
	c.t.Helper()
	if !c.finished {
		c.t.Error("no Finished event")
	}
}

// Check holds the next events of the stream to the contract; event
// numbers in its reports count from the start of the stream. It reports
// whether evs broke nothing.
func (c *Checker) Check(evs []engine.Event) (ok bool) {
	c.t.Helper()
	ok = true
	errorf := func(format string, a ...any) {
		c.t.Helper()
		c.t.Errorf(format, a...)
		ok = false
	}
	at := func(i int, id string, idx int) {
		if !c.queued {
			errorf("event %d names step %s before QueueStarted", i, id)
		} else if idx < 0 || idx >= len(c.plan) || c.plan[idx] != id {
			errorf("event %d: step %s is not at index %d of %v", i, id, idx, c.plan)
		}
		if c.settled[id] {
			errorf("event %d names step %s after it ended", i, id)
		}
	}
	// replan checks a plan change at idx (where a StepInserted inserts, the
	// step a PlanChanged changes): it comes between steps and moves or
	// changes no step that has already run or ended. It reports whether idx
	// is in range.
	replan := func(i int, what string, idx int) bool {
		if !c.queued {
			errorf("event %d: %s before QueueStarted", i, what)
		}
		if c.running != "" {
			errorf("event %d: %s while %s runs (a result's plan change comes after its StepDone)", i, what, c.running)
		}
		if c.asking != "" {
			errorf("event %d: %s while %s's confirm is open (it comes before the next step's events)", i, what, c.asking)
		}
		if idx < 0 || idx > len(c.plan) {
			errorf("event %d: %s at %d of %d steps", i, what, idx, len(c.plan))
			return false
		}
		if j := slices.IndexFunc(c.plan[idx:], func(id string) bool { return c.started[id] || c.settled[id] }); j >= 0 {
			errorf("event %d: %s at %d, at or before %s, which already ran or ended", i, what, idx, c.plan[idx+j])
		}
		return true
	}
	for _, ev := range evs {
		i := c.n
		c.n++
		switch ev.(type) {
		case engine.AuthResolved:
			if !c.waiting {
				errorf("event %d: AuthResolved with no AuthWaiting open", i)
			}
			c.waiting = false
		case engine.AuthWaiting, engine.QueueStarted, engine.StepStarted, engine.NeedsConfirm,
			engine.NeedsPassword, engine.NeedsSelection, engine.OutputReport, engine.Finished:
			if c.waiting {
				errorf("event %d (%T) before the open AuthWaiting's AuthResolved", i, ev)
			}
			_, c.waiting = ev.(engine.AuthWaiting)
		}
		if c.finished {
			switch ev.(type) {
			case engine.PhaseChanged, engine.StatusUpdated:
				continue
			case engine.QueueStarted, engine.Notice, engine.NeedsConfirm, engine.NeedsPassword,
				engine.NeedsSelection, engine.OutputReport:
				// the next action
				c.plan, c.queued, c.running, c.asking, c.waiting, c.finished = nil, false, "", "", false, false
				c.settled, c.started, c.asked = map[string]bool{}, map[string]bool{}, map[string]bool{}
			default:
				errorf("event %d (%T) after Finished", i, ev)
				continue
			}
		}
		if id := promptID(ev); id != 0 {
			if c.prompts[id] {
				errorf("event %d reuses prompt ID %d", i, id)
			}
			c.prompts[id] = true
		}
		switch ev := ev.(type) {
		case engine.QueueStarted:
			c.queued = true
			for _, s := range ev.Steps {
				c.plan = append(c.plan, s.ID)
			}
		case engine.NeedsConfirm:
			if ev.Step == "" {
				break // the gate in front of the whole queue
			}
			if !c.queued || !slices.Contains(c.plan, ev.Step) {
				errorf("event %d: confirm for step %s, which isn't in the plan %v", i, ev.Step, c.plan)
			}
			if c.started[ev.Step] || c.settled[ev.Step] {
				errorf("event %d: confirm for step %s, which already ran or ended", i, ev.Step)
			}
			if c.running != "" {
				errorf("event %d: confirm for step %s while %s runs", i, ev.Step, c.running)
			}
			c.asking, c.asked[ev.Step] = ev.Step, true
		case engine.StepInserted:
			if replan(i, "StepInserted "+ev.Step.ID, ev.Index) {
				c.plan = slices.Insert(c.plan, ev.Index, ev.Step.ID)
			}
		case engine.PlanChanged:
			if replan(i, "PlanChanged "+ev.Step.ID, ev.Index) &&
				(ev.Index == len(c.plan) || c.plan[ev.Index] != ev.Step.ID) {
				errorf("event %d: PlanChanged names step %s, not at index %d of %v", i, ev.Step.ID, ev.Index, c.plan)
			}
		case engine.StepStarted:
			at(i, ev.ID, ev.Index)
			if c.running != "" {
				errorf("event %d starts %s while %s runs", i, ev.ID, c.running)
			}
			// A confirm asked ahead (a combined run's) is fine for a step
			// before its own; one at or before this step was jumped over.
			for a := range c.asked {
				if a != ev.ID && slices.Index(c.plan, a) <= slices.Index(c.plan, ev.ID) {
					errorf("event %d starts %s while %s's confirm is open", i, ev.ID, a)
				}
			}
			delete(c.asked, ev.ID)
			c.running, c.started[ev.ID] = ev.ID, true
			if c.asking == ev.ID || len(c.asked) == 0 {
				c.asking = ""
			}
		case engine.OutputChunk:
			at(i, ev.ID, ev.Index)
			if c.running != ev.ID {
				errorf("event %d: output for %s, which isn't running", i, ev.ID)
			}
		case engine.StepDone:
			at(i, ev.ID, ev.Index)
			switch {
			case ev.Status == steps.Aborted || ev.Status == steps.Running || ev.Status == steps.Pending:
				errorf("event %d: StepDone with status %d", i, ev.Status)
			case c.started[ev.ID] && c.running != ev.ID:
				errorf("event %d: %s done but not running", i, ev.ID)
			case !c.started[ev.ID] && ev.Status != steps.Skipped && ev.Status != steps.AuthRefused:
				errorf("event %d: %s settled without running, status %d", i, ev.ID, ev.Status)
			}
			c.settled[ev.ID] = true
			if c.running == ev.ID {
				c.running = ""
			}
			delete(c.asked, ev.ID)
			if c.asking == ev.ID {
				c.asking = ""
			}
		case engine.Aborted:
			at(i, ev.ID, ev.Index)
			if c.running != ev.ID {
				errorf("event %d: aborted %s, which isn't running", i, ev.ID)
			}
			c.settled[ev.ID], c.running = true, ""
		case engine.Finished:
			c.finished, c.asking = true, ""
			clear(c.asked)
			if ev.Outcome == engine.OutcomeCompleted {
				for _, id := range c.plan {
					if !c.settled[id] {
						errorf("completed with step %s never settled", id)
					}
				}
			}
		}
	}
	return ok
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
