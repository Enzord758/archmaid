package runview

import (
	"fmt"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// FloodID is the one step of a Flood script.
const FloodID = "flood"

// Flood is a stub script for the output-throughput proof: one step that
// prints n lines, perChunk lines per OutputChunk, waiting every between
// chunks (0: as fast as the reader takes them). Every tenth line carries an
// SGR colour, like real pacman output.
func Flood(start time.Time, n, perChunk int, every time.Duration) stub.Script {
	if perChunk <= 0 {
		perChunk = 1
	}
	step := engine.PlanStep{ID: FloodID, Title: fmt.Sprintf("Printing %d lines…", n), Argv: []string{"flood"}, Category: logstore.Cleanup}
	s := stub.Script{
		{Event: engine.PhaseChanged{Phase: engine.PhaseRunning}},
		{Event: engine.QueueStarted{Action: "flood", Steps: []engine.PlanStep{step}, Started: start}},
		{Event: engine.StepStarted{ID: FloodID, Index: 0, At: start}},
	}
	for i := 0; i < n; i += perChunk {
		lines := make([]string, 0, perChunk)
		for j := i; j < min(i+perChunk, n); j++ {
			if j%10 == 0 {
				lines = append(lines, fmt.Sprintf("\x1b[1;32m==>\x1b[0m line %d: checking package files", j+1))
			} else {
				lines = append(lines, fmt.Sprintf("line %d: (%d/%d) checking keys in keyring", j+1, j+1, n))
			}
		}
		s = append(s, stub.Cue{After: every, Event: engine.OutputChunk{ID: FloodID, Index: 0, Lines: lines}})
	}
	elapsed := time.Duration((n+perChunk-1)/perChunk) * every
	return append(s,
		stub.Cue{Event: engine.StepDone{ID: FloodID, Index: 0, Status: steps.OK, Elapsed: elapsed}},
		stub.Cue{Event: engine.Finished{Action: "flood", Outcome: engine.OutcomeCompleted, Elapsed: elapsed}},
		stub.Cue{Event: engine.PhaseChanged{Phase: engine.PhaseIdle}},
	)
}

// FloodLine is line i (1-based) of a Flood script, as the view shows it.
func FloodLine(i, n int) string {
	if (i-1)%10 == 0 {
		return fmt.Sprintf("==> line %d: checking package files", i)
	}
	return fmt.Sprintf("line %d: (%d/%d) checking keys in keyring", i, i, n)
}
