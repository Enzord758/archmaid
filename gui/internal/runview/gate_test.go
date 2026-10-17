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
		"3 steps run as root, together, through one authorisation; topgrade gets root through its own sudo. If topgrade's sudo needs your password, archmaid asks for it in its own dialog.",
		"Step 5 asks before it deletes the entire package cache.",
		"Orphans are listed for you to confirm before anything is removed.",
		"If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.",
	}
	if got := GateNotes(full, true, false, "", false); !slices.Equal(got, want) {
		t.Errorf("notes\n%q\nwant\n%q", got, want)
	}
	// The real GUI: polkit with a kept password. The count is the root
	// tags, topgrade, tagged sudo, is named beside it (Dev 5's VM round b6:
	// "6 steps run as root" beside 7 root tags), and the root steps are one
	// authorisation, not each through sudo (round b8).
	want[0] = "3 steps run as root, together, through one authorisation; topgrade gets root through its own sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only."
	if got := GateNotes(full, true, true, "", false); !slices.Equal(got, want) {
		t.Errorf("polkit, kept: notes\n%q\nwant\n%q", got, want)
	}
	// Sudo mode with a kept password: each root step asks sudo.
	want[0] = "3 steps run as root, each through sudo; topgrade gets root through its own sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only."
	if got := GateNotes(full, false, true, "", false); !slices.Equal(got, want) {
		t.Errorf("sudo, kept: notes\n%q\nwant\n%q", got, want)
	}
	// Sudo mode, nothing kept.
	want[0] = "3 steps run as root, each through sudo; topgrade gets root through its own sudo. If sudo needs your password, archmaid asks for it in its own dialog."
	if got := GateNotes(full, false, false, "", false); !slices.Equal(got, want) {
		t.Errorf("sudo: notes\n%q\nwant\n%q", got, want)
	}
	// A dry run asks for no password, kept or not (Dev 5).
	want[0] = DryNoPassword
	for _, polkit := range []bool{false, true} {
		for _, kept := range []bool{false, true} {
			if got := GateNotes(full, polkit, kept, "", true); !slices.Equal(got, want) {
				t.Errorf("dry, polkit %v, kept %v: notes\n%q\nwant\n%q", polkit, kept, got, want)
			}
		}
	}
	if got := GateKicker(full); got != "7 steps, in this order" {
		t.Errorf("kicker %q", got)
	}

	// Run topgrade: no helper step; topgrade's sudo asks in our dialog.
	tg := []engine.PlanStep{{ID: steps.IDAURAudit}, {ID: steps.IDTopgrade, NeedsPriv: true}}
	if got := GateNotes(tg, true, true, "", false); !slices.Equal(got, []string{
		"You type your password once, the first time a step needs it; archmaid keeps it for this run only.",
		"If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.",
	}) {
		t.Errorf("topgrade: %q", got)
	}
	one := []engine.PlanStep{{ID: "x", Verb: "v", NeedsPriv: true, Confirm: true}}
	if got := GateNotes(one, true, false, "", false); !slices.Equal(got, []string{"1 step runs as root, through one authorisation.", "Step 1 asks before it runs."}) {
		t.Errorf("one: %q", got)
	}
	if got := GateNotes(one, false, true, "", false); !slices.Equal(got, []string{"1 step runs as root, through sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only.", "Step 1 asks before it runs."}) {
		t.Errorf("one, kept: %q", got)
	}
	// Steps other than topgrade that run sudo themselves are named by number.
	other := []engine.PlanStep{{ID: "a", NeedsPriv: true}, {ID: "b", Verb: "v", NeedsPriv: true}, {ID: "c", NeedsPriv: true}, {ID: "d", NeedsPriv: true}}
	if got := GateNotes(other, false, true, "", false); len(got) != 1 || !strings.HasPrefix(got[0], "1 step runs as root, through sudo; steps 1, 3 and 4 get root through their own sudo. ") {
		t.Errorf("other sudo steps: %q", got)
	}
	if got := GateNotes(other[:2], false, true, "", false); len(got) != 1 || !strings.HasPrefix(got[0], "1 step runs as root, through sudo; step 1 gets root through its own sudo. ") {
		t.Errorf("one other sudo step: %q", got)
	}
	if got := GateNotes(nil, true, false, "", true); got != nil {
		t.Errorf("no plan: %q", got)
	}
}

// TestGateNotesPolkitOnlyKept (#120): a synthetic gated polkit plan with
// root steps and no topgrade (none exists today): only polkit asks, so the
// kept note must not say "You type your password once, the first time a
// step needs it" (that is sudo's). With topgrade or a sudo step beside it,
// the one-password sentence stays.
func TestGateNotesPolkitOnlyKept(t *testing.T) {
	root := []engine.PlanStep{{ID: "x", Verb: "v", NeedsPriv: true}, {ID: "y", Verb: "w", NeedsPriv: true}}
	want := []string{"2 steps run as root, together, through one authorisation. polkit asks for your password; archmaid keeps it for this run only."}
	if got := GateNotes(root, true, true, "", false); !slices.Equal(got, want) {
		t.Errorf("polkit only, kept: %q, want %q", got, want)
	}
	if got := GateNotes(root, true, false, "", false); !slices.Equal(got, []string{"2 steps run as root, together, through one authorisation."}) {
		t.Errorf("polkit only, not kept: %q", got)
	}
	withTG := append(slices.Clone(root), engine.PlanStep{ID: steps.IDTopgrade, NeedsPriv: true})
	if got := GateNotes(withTG, true, true, "", false); len(got) != 1 || !strings.Contains(got[0], "You type your password once") {
		t.Errorf("polkit and topgrade, kept: %q", got)
	}
	withSudo := append(slices.Clone(root), engine.PlanStep{ID: "z", NeedsPriv: true})
	if got := GateNotes(withSudo, true, true, "", false); len(got) != 1 || !strings.Contains(got[0], "You type your password once") {
		t.Errorf("polkit and a sudo step, kept: %q", got)
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

// TestGateNotesKeepOff (#172, backlog #152): when the kept password's PATH
// guard would turn the password off, the gate does not promise "archmaid
// keeps it for this run only"; it says the password is not kept, with the
// dialog's reason, in every mode that kept it before.
func TestGateNotesKeepOff(t *testing.T) {
	const why = "the PATH check found something that could stand in for sudo"
	root := []engine.PlanStep{{ID: "x", Verb: "v", NeedsPriv: true}, {ID: "y", Verb: "w", NeedsPriv: true}}
	withTG := append(slices.Clone(root), engine.PlanStep{ID: steps.IDTopgrade, NeedsPriv: true})
	sudoOnly := []engine.PlanStep{{ID: "s", NeedsPriv: true}}
	for name, c := range map[string]struct {
		plan   []engine.PlanStep
		polkit bool
	}{"polkit only": {root, true}, "polkit and topgrade": {withTG, true}, "sudo": {root, false}, "sudo step only": {sudoOnly, false}} {
		got := GateNotes(c.plan, c.polkit, true, why, false)
		if len(got) != 1 {
			t.Fatalf("%s: %q", name, got)
		}
		if strings.Contains(got[0], "keeps it for this run only") {
			t.Errorf("%s: still promises to keep the password: %q", name, got[0])
		}
		if want := "archmaid is not keeping your password for this run (" + why + "), so every password prompt asks you."; !strings.HasSuffix(got[0], want) {
			t.Errorf("%s: %q, want it to end %q", name, got[0], want)
		}
		if normal := GateNotes(c.plan, c.polkit, true, "", false); !strings.Contains(normal[0], "keeps it for this run only") {
			t.Errorf("%s: kept wording lost: %q", name, normal)
		}
	}
	// Not kept at all: keepOff changes nothing.
	if a, b := GateNotes(root, true, false, why, false), GateNotes(root, true, false, "", false); !slices.Equal(a, b) {
		t.Errorf("keepOff without kept: %q vs %q", a, b)
	}
}
