package runview

import (
	"fmt"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
)

// Header is the Run page's header (P8's Workbench, A10): the kicker line,
// the step counts, and the sidebar's Run badge. Elapsed is separate, since
// it ticks while nothing else changes.
type Header struct {
	// Kicker is "Running · step 2 of 9", "Dry run · step 6 of 9",
	// "Waiting for authorisation", "Waiting for your password", the
	// finished status ("Finished: failed (1 step)") or EngineStopped;
	// "" before the first run.
	Kicker string
	// Step is the running step's position (1-based), or the number of
	// steps settled when none runs; Of is the plan's length.
	Step, Of int
	// Done, Failed, Skipped, Refused, Stopped and NotRun count the rows
	// in each settled state.
	Done, Failed, Skipped, Refused, Stopped, NotRun int
	// Badge is the sidebar's Run badge: "02/09" while a queue runs,
	// "done" once it finished, "" otherwise.
	Badge string
}

// Header is the header as of the last Apply.
func (m *Model) Header() Header {
	h := Header{Of: len(m.Rows)}
	running := -1
	for i, r := range m.Rows {
		switch r.State {
		case RowRunning:
			running = i
		case RowOK:
			h.Done++
		case RowFailed:
			h.Failed++
		case RowSkipped:
			h.Skipped++
		case RowAuthRefused:
			h.Refused++
		case RowAborted:
			h.Stopped++
		case RowNotRun:
			h.NotRun++
		}
	}
	h.Step = h.Done + h.Failed + h.Skipped + h.Refused + h.Stopped + h.NotRun
	if running >= 0 {
		h.Step = running + 1
	}
	switch {
	case m.Stopped != "":
		h.Kicker = m.Stopped
	case m.Finished != nil && m.Discovery != nil:
		h.Kicker, h.Badge = DiscoveryText(*m.Discovery), "done"
	case m.Finished != nil:
		h.Kicker, _ = FinishedText(*m.Finished)
		h.Badge = "done"
	case m.Running:
		h.Badge = fmt.Sprintf("%02d/%02d", h.Step, h.Of)
		switch {
		case m.Auth != nil && m.Auth.Kind == engine.WaitPolkit:
			h.Kicker = "Waiting for authorisation"
		case m.Prompt != nil && m.Prompt.Password != nil:
			h.Kicker = "Waiting for your password"
		case m.DryRun:
			h.Kicker = fmt.Sprintf("Dry run · step %d of %d", h.Step, h.Of)
		default:
			h.Kicker = fmt.Sprintf("Running · step %d of %d", h.Step, h.Of)
		}
	}
	return h
}

// Elapsed is the run's time at now: since its QueueStarted while it runs,
// the engine's own figure once it finished, 0 before a queue.
func (m *Model) Elapsed(now time.Time) time.Duration {
	switch {
	case m.Finished != nil:
		return m.Finished.Elapsed
	case m.Started.IsZero() || !m.Running:
		return 0
	}
	return max(0, now.Sub(m.Started))
}
