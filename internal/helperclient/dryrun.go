package helperclient

import (
	"strings"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// DryRun reports what Run would start for st, and starts nothing: no
// pkexec, no polkit dialog, no root helper. It emits the Inputs Run would,
// in the same order, so a dry run previews the real route:
//
//   - "[dry-run] would run: pkexec --disable-internal-agent
//     /usr/lib/archmaid/archmaid-helper <verb> <args…>" (proc.DryRunPrefix) as the step's output: the very command
//     line Argv builds, so a step Argv refuses fails here as it would there;
//   - for a combined call (Parts), the call's line once, on its first part,
//     then one line per part naming it, each part started (PartStarted) and
//     ended (PartDone) in order as the helper would;
//   - last, exactly one StepExited.
//
// No PrivilegeGranted: nothing was authorised.
func DryRun(seq uint64, st steps.Step, emit func(engine.Input)) Outcome {
	argv, err := Argv(st)
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: err})
	}
	line := proc.DryRunPrefix + strings.Join(argv, " ")
	if len(st.Parts) == 0 {
		emit(engine.StepOutput{Seq: seq, Lines: []string{line}})
		return exited(seq, emit, Outcome{})
	}
	for i, p := range st.Parts {
		if i > 0 {
			emit(engine.PartStarted{Seq: seq, Step: p.ID})
		}
		lines := []string{"[dry-run] part " + partName(p) + " of the one root-helper call"}
		if i == 0 {
			lines = append([]string{line}, lines...)
		}
		emit(engine.StepOutput{Seq: seq, Lines: lines})
		if i < len(st.Parts)-1 {
			emit(engine.PartDone{Seq: seq, Step: p.ID})
		}
	}
	return exited(seq, emit, Outcome{})
}
