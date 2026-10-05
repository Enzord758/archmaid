package runview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
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
		{engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 2, Refused: 1}, "Finished: failed (2 steps)", "Run finished with 2 failed steps"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1, Skipped: 1}, "Finished: incomplete (1 step not authorised)", "Run incomplete: 1 step was not authorised"},
		{engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 6, Skipped: 6}, "Finished: incomplete (6 steps not authorised)", "Run incomplete: 6 steps were not authorised"},
		{engine.Finished{Outcome: engine.OutcomeAborted, NotRun: 3}, "Finished: aborted", "Run aborted"},
		{engine.Finished{Outcome: engine.OutcomeAborted, Errs: 1}, "Finished: aborted (1 step failed)", "Run aborted, 1 step failed"},
		{engine.Finished{Outcome: engine.OutcomeCancelled}, "Finished: cancelled", "Run cancelled, nothing ran"},
		{engine.Finished{Outcome: engine.OutcomeNotice}, "Finished", "Run finished"},
		{engine.Finished{Outcome: engine.OutcomeReport}, "Finished", "Run finished"},
	} {
		if s, toast := FinishedText(c.f); s != c.status || toast != c.toast {
			t.Errorf("%+v: %q / %q, want %q / %q", c.f, s, toast, c.status, c.toast)
		}
	}
}
