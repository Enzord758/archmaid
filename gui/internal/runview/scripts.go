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
	return flood(start, n, perChunk, every, floodRaw)
}

// FloodColour is Flood with every line in colour, seven styled spans each:
// bold and the 16 colours, a 256-colour and a 24-bit one, the worst case
// for the parser and the view.
func FloodColour(start time.Time, n, perChunk int, every time.Duration) stub.Script {
	return flood(start, n, perChunk, every, colourRaw)
}

func floodRaw(j, n int) string {
	if j%10 == 0 {
		return fmt.Sprintf("\x1b[1;32m==>\x1b[0m line %d: checking package files", j+1)
	}
	return fmt.Sprintf("line %d: (%d/%d) checking keys in keyring", j+1, j+1, n)
}

func colourRaw(j, n int) string {
	return fmt.Sprintf("\x1b[1;34m::\x1b[0m \x1b[1mline %d\x1b[22m (\x1b[32m%d\x1b[39m/\x1b[93m%d\x1b[0m) \x1b[38;5;208mupgrading\x1b[0m \x1b[38;2;120;180;255mlib%d\x1b[0m \x1b[4;31m[!]\x1b[0m",
		j+1, j+1, n, j%97)
}

// FloodColourLine is line i (1-based) of a FloodColour script, as text.
func FloodColourLine(i, n int) string {
	return fmt.Sprintf(":: line %d (%d/%d) upgrading lib%d [!]", i, i, n, (i-1)%97)
}

func flood(start time.Time, n, perChunk int, every time.Duration, raw func(j, n int) string) stub.Script {
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
			lines = append(lines, raw(j, n))
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

// FloodSteps is a stub script for the timeline's throughput proof: a plan
// of k steps that print n lines between them (n/k each, perChunk lines per
// OutputChunk, unpaced), each finishing before the next starts, so the
// timeline folds steps mid-flood and the OutputCap trims across step
// boundaries. Every tenth line carries an SGR colour, as in Flood; the last
// line is FloodLine(n, n).
func FloodSteps(start time.Time, n, k, perChunk int) stub.Script {
	if perChunk <= 0 {
		perChunk = 1
	}
	var plan []engine.PlanStep
	for i := range k {
		plan = append(plan, engine.PlanStep{ID: fmt.Sprintf("flood-%02d", i+1), Title: fmt.Sprintf("Printing part %d of %d…", i+1, k),
			LogLabel: fmt.Sprintf("Part %d printed", i+1), Argv: []string{"flood"}, Category: logstore.Cleanup})
	}
	s := stub.Script{
		{Event: engine.PhaseChanged{Phase: engine.PhaseRunning}},
		{Event: engine.QueueStarted{Action: "flood", Steps: plan, Started: start}},
	}
	per := n / k
	for i, p := range plan {
		s = append(s, stub.Cue{Event: engine.StepStarted{ID: p.ID, Index: i, At: start}})
		from, to := i*per, (i+1)*per
		if i == k-1 {
			to = n
		}
		for c := from; c < to; c += perChunk {
			lines := make([]string, 0, perChunk)
			for j := c; j < min(c+perChunk, to); j++ {
				lines = append(lines, floodRaw(j, n))
			}
			s = append(s, stub.Cue{Event: engine.OutputChunk{ID: p.ID, Index: i, Lines: lines}})
		}
		s = append(s, stub.Cue{Event: engine.StepDone{ID: p.ID, Index: i, Status: steps.OK}})
	}
	return append(s,
		stub.Cue{Event: engine.Finished{Action: "flood", Outcome: engine.OutcomeCompleted}},
		stub.Cue{Event: engine.PhaseChanged{Phase: engine.PhaseIdle}},
	)
}
