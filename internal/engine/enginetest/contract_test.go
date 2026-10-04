package enginetest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// spy is a testing.TB that keeps what the checker reports instead of
// failing the test.
type spy struct {
	testing.TB
	errs []string
}

func (s *spy) Helper()                       {}
func (s *spy) Error(a ...any)                { s.errs = append(s.errs, fmt.Sprint(a...)) }
func (s *spy) Errorf(f string, a ...any)     { s.errs = append(s.errs, fmt.Sprintf(f, a...)) }
func (s *spy) Fatalf(f string, a ...any)     { s.Errorf(f, a...) }
func (s *spy) check(evs []engine.Event) *spy { CheckContract(s, evs); return s }

var (
	plan2 = engine.QueueStarted{Steps: []engine.PlanStep{{ID: "a"}, {ID: "b"}}}
	gated = engine.PlanChanged{Index: 1, Step: engine.PlanStep{ID: "b", Confirm: true, Optional: true}}
	ins   = engine.StepInserted{Index: 1, Step: engine.PlanStep{ID: "x"}}
)

func run(id string, idx int) []engine.Event {
	return []engine.Event{engine.StepStarted{ID: id, Index: idx}, engine.OutputChunk{ID: id, Index: idx, Lines: []string{"l"}},
		engine.StepDone{ID: id, Index: idx, Status: steps.OK}}
}

func seq(parts ...any) []engine.Event {
	var evs []engine.Event
	for _, p := range parts {
		switch p := p.(type) {
		case []engine.Event:
			evs = append(evs, p...)
		case engine.Event:
			evs = append(evs, p)
		}
	}
	return evs
}

var done = engine.Finished{Outcome: engine.OutcomeCompleted}

// The plan changes a result makes come after its step's StepDone, before
// the next step's events, and never touch a step that ran.
func TestContractPlanChanges(t *testing.T) {
	ok := map[string][]engine.Event{
		"gate after done": seq(plan2, run("a", 0), gated, engine.NeedsConfirm{ID: 1, Step: "b"}, run("b", 1), done),
		"insert after done": seq(plan2, run("a", 0), ins, engine.NeedsConfirm{ID: 1, Step: "x"}, run("x", 1),
			run("b", 2), done),
	}
	for name, evs := range ok {
		if errs := (&spy{TB: t}).check(evs).errs; len(errs) > 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
	bad := map[string]struct {
		evs  []engine.Event
		want string
	}{
		"PlanChanged while its step runs": {seq(plan2, engine.StepStarted{ID: "a"}, gated,
			engine.StepDone{ID: "a", Status: steps.OK}, run("b", 1), done), "while a runs"},
		"StepInserted while its step runs": {seq(plan2, engine.StepStarted{ID: "a"}, ins,
			engine.StepDone{ID: "a", Status: steps.OK}, run("x", 1), run("b", 2), done), "while a runs"},
		"PlanChanged after the next confirm": {seq(plan2, run("a", 0), engine.NeedsConfirm{ID: 1, Step: "b"}, gated,
			run("b", 1), done), "confirm is open"},
		"PlanChanged at a step that ran": {seq(plan2, run("a", 0),
			engine.PlanChanged{Index: 0, Step: engine.PlanStep{ID: "a"}}, run("b", 1), done), "already ran"},
		"StepInserted before a step that ran": {seq(plan2, run("a", 0),
			engine.StepInserted{Index: 0, Step: engine.PlanStep{ID: "x"}}, run("b", 2), done), "already ran"},
		"PlanChanged at the wrong index": {seq(plan2, run("a", 0),
			engine.PlanChanged{Index: 1, Step: engine.PlanStep{ID: "c"}}, run("b", 1), done), "not at index 1"},
		"PlanChanged out of range": {seq(plan2, run("a", 0),
			engine.PlanChanged{Index: 2, Step: engine.PlanStep{ID: "b"}}, run("b", 1), done), "not at index 2"},
		"PlanChanged before QueueStarted": {seq(gated, plan2, run("a", 0), run("b", 1), done), "before QueueStarted"},
		"confirm for a step that ran": {seq(plan2, run("a", 0), engine.NeedsConfirm{ID: 1, Step: "a"},
			run("b", 1), done), "already ran"},
	}
	for name, c := range bad {
		errs := (&spy{TB: t}).check(c.evs).errs
		if !strings.Contains(strings.Join(errs, "\n"), c.want) {
			t.Errorf("%s: reports %q, want one saying %q", name, errs, c.want)
		}
	}
}

// StatusUpdated may come between any two events, after Finished too (L4).
func TestContractStatusAfterFinished(t *testing.T) {
	evs := seq(plan2, run("a", 0), run("b", 1), done, engine.StatusUpdated{}, engine.PhaseChanged{})
	if errs := (&spy{TB: t}).check(evs).errs; len(errs) > 0 {
		t.Errorf("StatusUpdated after Finished: %v", errs)
	}
	evs = append(evs, engine.StepDone{ID: "a"})
	if errs := (&spy{TB: t}).check(evs).errs; len(errs) == 0 {
		t.Error("a StepDone after Finished passed")
	}
}

// A Checker fed piece by piece numbers events across the pieces.
func TestCheckerPieces(t *testing.T) {
	s := &spy{TB: t}
	c := NewChecker(s)
	c.Check(seq(plan2, engine.StepStarted{ID: "a"}))
	c.Check(seq(gated))
	c.Done()
	errs := strings.Join(s.errs, "\n")
	if !strings.Contains(errs, "event 2: PlanChanged b while a runs") || !strings.Contains(errs, "no Finished") {
		t.Errorf("reports %q", s.errs)
	}
}
