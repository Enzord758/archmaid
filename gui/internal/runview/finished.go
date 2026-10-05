package runview

import (
	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/engine"
)

// FinishedText is how the window reports a run's end, as last-run.json
// judges it: the status line's part ("Finished: failed (1 step)") and the
// toast ("Run finished with 1 failed step"). The engine's Outcome alone
// says only how the queue ended: a queue that ran to its end is
// OutcomeCompleted even with a failed step (Errs) or steps polkit refused
// (Refused), which last-run.json records as failed and incomplete.
func FinishedText(f engine.Finished) (status, toast string) {
	switch f.Outcome {
	case engine.OutcomeCompleted:
		switch {
		case f.Errs > 0:
			return "Finished: failed (" + plural.N(f.Errs, "step", "steps") + ")",
				"Run finished with " + plural.N(f.Errs, "failed step", "failed steps")
		case f.Refused > 0:
			return "Finished: incomplete (" + plural.N(f.Refused, "step", "steps") + " not authorised)",
				"Run incomplete: " + plural.N(f.Refused, "step was", "steps were") + " not authorised"
		}
		return "Finished: completed", "Run completed"
	case engine.OutcomeAborted:
		if f.Errs > 0 {
			return "Finished: aborted (" + plural.N(f.Errs, "step", "steps") + " failed)",
				"Run aborted, " + plural.N(f.Errs, "step", "steps") + " failed"
		}
		return "Finished: aborted", "Run aborted"
	case engine.OutcomeCancelled:
		return "Finished: cancelled", "Run cancelled, nothing ran"
	}
	return "Finished", "Run finished" // a notice or a report: the run's end is what it showed
}
