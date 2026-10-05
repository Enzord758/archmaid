package helperclient

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestDryRun: DryRun reports the exact command line Run would start, emits
// a combined call's parts as Run would (the call's line on the first part,
// one line per part), ends with one StepExited, and starts nothing: a
// pkexec shim first on PATH is never run.
func TestDryRun(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	shim(t, "touch '"+marker+"'")

	var lone []engine.Input
	DryRun(seq, steps.Step{Verb: steps.VerbPaccache}, func(in engine.Input) { lone = append(lone, in) })
	want := []engine.Input{
		engine.StepOutput{Seq: seq, Lines: []string{"[dry-run] would run: pkexec " + HelperPath + " paccache " + PaccacheKeep}},
		engine.StepExited{Seq: seq},
	}
	if !slices.EqualFunc(lone, want, func(a, b engine.Input) bool { return equalInput(a, b) }) {
		t.Errorf("a lone step: %#v", lone)
	}

	var got []string
	call := fullCleanupCall(true, true, true)
	DryRun(seq, call, func(in engine.Input) { got = append(got, trace(recorded{inputs: []engine.Input{in}})...) })
	argv, _ := Argv(call)
	line := "[dry-run] would run: " + strings.Join(argv, " ")
	wantTrace := []string{
		"| " + line, "| [dry-run] part remove-orphans of the one root-helper call", "done orphans-remove",
		"start paccache", "| [dry-run] part paccache of the one root-helper call", "done paccache",
		"start paccache-wipe", "| [dry-run] part paccache-wipe of the one root-helper call", "done paccache-wipe",
		"start pkg-cache-uninstalled", "| [dry-run] part clean-uninstalled of the one root-helper call", "done pkg-cache-uninstalled",
		"start tmpfiles", "| [dry-run] part clean-tmp of the one root-helper call", "done tmpfiles",
		"start journal", "| [dry-run] part vacuum-journal of the one root-helper call", "done journal",
		"start integrity", "| [dry-run] part check-files of the one root-helper call",
		"exited <nil>",
	}
	if !slices.Equal(got, wantTrace) {
		t.Errorf("a combined call:\n got %q\nwant %q", got, wantTrace)
	}

	var bad []engine.Input
	DryRun(seq, steps.Step{Verb: steps.VerbFullCleanup}, func(in engine.Input) { bad = append(bad, in) })
	if len(bad) != 1 {
		t.Fatalf("a refused call: %#v", bad)
	}
	if e, ok := bad[0].(engine.StepExited); !ok || !errors.Is(e.Err, ErrBadParts) {
		t.Errorf("a refused call ends %#v, want ErrBadParts", bad[0])
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a dry run ran pkexec")
	}
}

// equalInput compares the two Inputs a lone dry run emits.
func equalInput(a, b engine.Input) bool {
	switch a := a.(type) {
	case engine.StepOutput:
		b, ok := b.(engine.StepOutput)
		return ok && a.Seq == b.Seq && slices.Equal(a.Lines, b.Lines)
	case engine.StepExited:
		b, ok := b.(engine.StepExited)
		return ok && a.Seq == b.Seq && a.Err == nil && b.Err == nil && a.NotGranted == b.NotGranted
	}
	return false
}
