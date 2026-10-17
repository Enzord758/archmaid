package ui

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestGateNotesFollowKeepPreview (#172, backlog #152): the gate sheet asks
// Options.KeepPreview whether the kept password's PATH guard would turn the
// password off, and words itself from it: the dialog's reason when off, the
// "keeps it" text when on; no preview is asked in a dry run or when the
// Driver keeps nothing.
func TestGateNotesFollowKeepPreview(t *testing.T) {
	plan := []engine.PlanStep{{ID: steps.IDTopgrade, NeedsPriv: true}, {ID: "x", Verb: "v", NeedsPriv: true}}
	const why = "the PATH check found something that could stand in for sudo"
	asked := 0
	preview := func(reason string) func() string {
		return func() string { asked++; return reason }
	}
	base := Options{Engine: engine.Options{Auth: engine.AuthPolkit}, KeepsPassword: true}

	on := base
	on.KeepPreview = preview("")
	if got := gateNotesFor(on, plan, false)[0]; !strings.Contains(got, "archmaid keeps it for this run only") || strings.Contains(got, "not keeping") {
		t.Errorf("guard would keep it: %q", got)
	}
	off := base
	off.KeepPreview = preview(why)
	got := gateNotesFor(off, plan, false)[0]
	if strings.Contains(got, "keeps it for this run only") || !strings.Contains(got, "archmaid is not keeping your password for this run ("+why+")") {
		t.Errorf("guard would turn it off: %q", got)
	}
	if asked != 2 {
		t.Fatalf("preview asked %d times, want 2", asked)
	}
	gateNotesFor(off, plan, true)
	none := off
	none.KeepsPassword = false
	gateNotesFor(none, plan, false)
	if asked != 2 {
		t.Errorf("preview asked in a dry run or without a kept password (%d)", asked)
	}
}
