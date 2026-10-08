package runview

import (
	"fmt"
	"slices"

	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The gate's plan rail (P8 row 13, spec 1.3 and 1.11): the kicker and the
// notes under the numbered steps, computed from the plan, never fixed.

// GateKicker is the gate's kicker: "{N} steps, in this order".
func GateKicker(plan []engine.PlanStep) string {
	return plural.N(len(plan), "step", "steps") + ", in this order"
}

// GateNotes are the notes under the gate's plan: how the root steps are
// authorised (kept: the Driver keeps the run's password, one-password; in
// a dry run nothing is), each step that asks before it runs, by number,
// and the AUR gate before topgrade.
func GateNotes(plan []engine.PlanStep, kept, dry bool) []string {
	var notes []string
	helper, sudo := 0, false // root steps the helper runs (one polkit call); a step that runs sudo itself
	for _, s := range plan {
		switch {
		case s.NeedsPriv && s.Verb != "":
			helper++
		case s.NeedsPriv:
			sudo = true
		}
	}
	has := func(id string) bool {
		return slices.ContainsFunc(plan, func(s engine.PlanStep) bool { return s.ID == id })
	}
	switch {
	case dry && (helper > 0 || sudo):
		notes = append(notes, DryNoPassword)
	case helper > 0 && kept:
		notes = append(notes, rootSteps(helper)+". You type your password once, the first time a step needs it; archmaid keeps it for this run only.")
	case helper > 0:
		n := rootSteps(helper) + ", after one polkit authorisation"
		if has(steps.IDTopgrade) {
			n += " that comes after topgrade. If topgrade's sudo needs your password, archmaid asks for it in its own dialog."
		} else {
			n += "."
		}
		notes = append(notes, n)
	case sudo && kept:
		notes = append(notes, "You type your password once, the first time a step needs it; archmaid keeps it for this run only.")
	case sudo:
		notes = append(notes, "If sudo needs your password, archmaid asks for it in its own dialog.")
	}
	for i, s := range plan {
		if !s.Confirm {
			continue
		}
		what := "it runs"
		if s.ID == steps.IDPaccacheWipe {
			what = "it deletes the entire package cache"
		}
		notes = append(notes, fmt.Sprintf("Step %d asks before %s.", i+1, what))
	}
	if has(steps.IDOrphansProbe) {
		notes = append(notes, "Orphans are listed for you to confirm before anything is removed.")
	}
	if has(steps.IDAURAudit) && has(steps.IDTopgrade) {
		notes = append(notes, "If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update.")
	}
	return notes
}

// DryNoPassword is a dry run's gate note in place of the password note:
// a dry run runs nothing as root (Dev 5).
const DryNoPassword = "Nothing asks for a password or authorisation in a dry run."

// rootSteps is "1 step runs as root" or "{n} steps run as root, together".
func rootSteps(n int) string {
	if n == 1 {
		return "1 step runs as root"
	}
	return fmt.Sprintf("%d steps run as root, together", n)
}
