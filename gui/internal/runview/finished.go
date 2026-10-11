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

// EndToast is the toast for the action that ended with f. One that ended
// in a notice is told by the notice itself (what its discovery found, or
// why it couldn't look: "No enabled-but-inactive services found."), as the
// TUI's notice popup tells it; the notice is often all the action shows,
// since it ends before any queue and the Run page keeps the last run. Any
// other end is FinishedText's toast.
func (m *Model) EndToast(f engine.Finished) string {
	if f.Outcome == engine.OutcomeNotice && m.Notice != "" {
		return Shorten(m.Notice, ShortMax)
	}
	_, toast := FinishedText(f)
	return toast
}

// ShortMax is how long a toast or the status line lets a notice run.
const ShortMax = 120

// Shorten cuts s to at most n runes, ending in "…" when it cut; the whole
// text stays on the Run page.
func Shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// DiscoveryText is the header's word for an action that ended at its
// discovery.
func DiscoveryText(d engine.DiscoveryResult) string {
	if d.Outcome == engine.DiscoveryFailed {
		return "Failed: the check didn't finish"
	}
	return "Nothing found"
}
