package runview

import (
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestGateNotes: the gate's notes come from the plan: the root count (the
// helper's steps), each asking step by its number, the orphans and the AUR
// gate; one-password's text when the Driver keeps the password.
func TestGateNotes(t *testing.T) {
	full := []engine.PlanStep{
		{ID: steps.IDAURAudit},
		{ID: steps.IDTopgrade, NeedsPriv: true},
		{ID: steps.IDOrphansProbe},
		{ID: steps.IDPaccache, Verb: steps.VerbPaccache, NeedsPriv: true},
		{ID: steps.IDPaccacheWipe, Verb: steps.VerbPaccacheWipe, NeedsPriv: true, Confirm: true},
		{ID: steps.IDJournal, Verb: "vacuum-journal", NeedsPriv: true},
		{ID: steps.IDYayCache},
	}
	want := []string{
		"3 steps run as root, together, after one polkit authorisation that comes after topgrade. If topgrade's sudo needs your password, archmaid asks for it in its own dialog.",
		"Step 5 asks before it deletes the entire package cache.",
		"Orphans are listed for you to confirm before anything is removed.",
		"If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.",
	}
	if got := GateNotes(full, false); !slices.Equal(got, want) {
		t.Errorf("notes\n%q\nwant\n%q", got, want)
	}
	want[0] = "3 steps run as root, together. You type your password once, the first time a step needs it; archmaid keeps it for this run only."
	if got := GateNotes(full, true); !slices.Equal(got, want) {
		t.Errorf("kept: notes\n%q\nwant\n%q", got, want)
	}
	if got := GateKicker(full); got != "7 steps, in this order" {
		t.Errorf("kicker %q", got)
	}

	// Run topgrade: no helper step; topgrade's sudo asks in our dialog.
	tg := []engine.PlanStep{{ID: steps.IDAURAudit}, {ID: steps.IDTopgrade, NeedsPriv: true}}
	if got := GateNotes(tg, true); !slices.Equal(got, []string{
		"You type your password once, the first time a step needs it; archmaid keeps it for this run only.",
		"If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.",
	}) {
		t.Errorf("topgrade: %q", got)
	}
	one := []engine.PlanStep{{ID: "x", Verb: "v", NeedsPriv: true, Confirm: true}}
	if got := GateNotes(one, false); !slices.Equal(got, []string{"1 step runs as root, after one polkit authorisation.", "Step 1 asks before it runs."}) {
		t.Errorf("one: %q", got)
	}
	if got := GateNotes(nil, false); got != nil {
		t.Errorf("no plan: %q", got)
	}
}
