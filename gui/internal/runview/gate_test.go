package runview

import (
	"slices"
	"strings"
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
	if got := GateNotes(full, false, false); !slices.Equal(got, want) {
		t.Errorf("notes\n%q\nwant\n%q", got, want)
	}
	// The kept password (the real Driver): the count is the root tags,
	// and topgrade, tagged sudo, is named beside it (Dev 5's VM round b6:
	// "6 steps run as root" beside 7 root tags).
	want[0] = "3 steps run as root, each through sudo; topgrade gets root through its own sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only."
	if got := GateNotes(full, true, false); !slices.Equal(got, want) {
		t.Errorf("kept: notes\n%q\nwant\n%q", got, want)
	}
	// A dry run asks for no password, kept or not (Dev 5).
	want[0] = DryNoPassword
	for _, kept := range []bool{false, true} {
		if got := GateNotes(full, kept, true); !slices.Equal(got, want) {
			t.Errorf("dry, kept %v: notes\n%q\nwant\n%q", kept, got, want)
		}
	}
	if got := GateKicker(full); got != "7 steps, in this order" {
		t.Errorf("kicker %q", got)
	}

	// Run topgrade: no helper step; topgrade's sudo asks in our dialog.
	tg := []engine.PlanStep{{ID: steps.IDAURAudit}, {ID: steps.IDTopgrade, NeedsPriv: true}}
	if got := GateNotes(tg, true, false); !slices.Equal(got, []string{
		"You type your password once, the first time a step needs it; archmaid keeps it for this run only.",
		"If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.",
	}) {
		t.Errorf("topgrade: %q", got)
	}
	one := []engine.PlanStep{{ID: "x", Verb: "v", NeedsPriv: true, Confirm: true}}
	if got := GateNotes(one, false, false); !slices.Equal(got, []string{"1 step runs as root, after one polkit authorisation.", "Step 1 asks before it runs."}) {
		t.Errorf("one: %q", got)
	}
	if got := GateNotes(one, true, false); !slices.Equal(got, []string{"1 step runs as root, through sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only.", "Step 1 asks before it runs."}) {
		t.Errorf("one, kept: %q", got)
	}
	// Steps other than topgrade that run sudo themselves are named by number.
	other := []engine.PlanStep{{ID: "a", NeedsPriv: true}, {ID: "b", Verb: "v", NeedsPriv: true}, {ID: "c", NeedsPriv: true}, {ID: "d", NeedsPriv: true}}
	if got := GateNotes(other, true, false); len(got) != 1 || !strings.HasPrefix(got[0], "1 step runs as root, through sudo; steps 1, 3 and 4 get root through their own sudo. ") {
		t.Errorf("other sudo steps: %q", got)
	}
	if got := GateNotes(other[:2], true, false); len(got) != 1 || !strings.HasPrefix(got[0], "1 step runs as root, through sudo; step 1 gets root through its own sudo. ") {
		t.Errorf("one other sudo step: %q", got)
	}
	if got := GateNotes(nil, false, true); got != nil {
		t.Errorf("no plan: %q", got)
	}
}

// TestPrivTag: a step archmaid runs as root through the helper (a Verb)
// is tagged root; one that needs root without a Verb gets it through its
// own sudo (topgrade) and is tagged sudo; the gate's note counts the root
// tags, so its number and the tags agree on the real Full Cleanup plan.
func TestPrivTag(t *testing.T) {
	for _, c := range []struct {
		priv bool
		verb string
		want string
	}{{false, "", ""}, {false, "v", ""}, {true, "", "sudo"}, {true, steps.VerbFullCleanup, "root"}} {
		if got := PrivTag(c.priv, c.verb); got != c.want {
			t.Errorf("PrivTag(%v, %q) = %q, want %q", c.priv, c.verb, got, c.want)
		}
	}
	// The Run page's rows carry the Verb, also when the plan changes.
	m := NewModel()
	m.Apply([]engine.Event{engine.QueueStarted{Action: "a", Steps: []engine.PlanStep{
		{ID: steps.IDTopgrade, NeedsPriv: true}, {ID: steps.IDPaccache, Verb: steps.VerbPaccache, NeedsPriv: true}, {ID: "x"}}}})
	var tags []string
	for _, r := range m.Rows {
		tags = append(tags, r.PrivTag())
	}
	if !slices.Equal(tags, []string{"sudo", "root", ""}) {
		t.Errorf("rows tagged %q", tags)
	}
	m.Apply([]engine.Event{engine.PlanChanged{Index: 2, Step: engine.PlanStep{ID: "x", Verb: "v", NeedsPriv: true}}})
	if got := m.Rows[2].PrivTag(); got != "root" {
		t.Errorf("after PlanChanged the row is tagged %q, want root", got)
	}
}
