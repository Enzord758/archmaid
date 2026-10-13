package runview

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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

// PrivTag is a step's privilege tag: "root" for a step archmaid runs as
// root (through the root helper, a Verb: Full Cleanup's run together under
// one polkit authorisation), "sudo" for one that runs as the user and gets
// root through its own sudo (topgrade: its yay and pacman call sudo, which
// asks through archmaid's askpass), "" for a step that needs neither. The
// gate's rail, the Overview's plan and the Run page's step headers tag a
// step with it, and GateNotes counts the root ones, so the note's count is
// the number of root tags (Dev 5's VM round b6: "6 steps run as root"
// beside 7 root tags, topgrade's among them).
func PrivTag(needsPriv bool, verb string) string {
	switch {
	case !needsPriv:
		return ""
	case verb == "":
		return "sudo"
	}
	return "root"
}

// PrivTag is the row's privilege tag (PrivTag).
func (r Row) PrivTag() string { return PrivTag(r.NeedsPriv, r.Verb) }

// GateNotes are the notes under the gate's plan: how the root steps are
// authorised, each step that asks before it runs, by number, and the AUR
// gate before topgrade. polkit is the auth mode the run really uses
// (engine.AuthPolkit: the root steps go in one helper call under one
// authorisation; else each asks sudo), kept that the Driver keeps the
// run's password (one password), whatever the mode (the real GUI is both;
// in a dry run nothing is asked). The root count is the steps tagged
// "root" (PrivTag); the steps tagged "sudo" are named beside it.
func GateNotes(plan []engine.PlanStep, polkit, kept, dry bool) []string {
	var notes []string
	helper := 0    // root steps the helper runs
	var sudo []int // the steps that run sudo themselves, by position
	for i, s := range plan {
		switch PrivTag(s.NeedsPriv, s.Verb) {
		case "root":
			helper++
		case "sudo":
			sudo = append(sudo, i)
		}
	}
	has := func(id string) bool {
		return slices.ContainsFunc(plan, func(s engine.PlanStep) bool { return s.ID == id })
	}
	switch {
	case dry && (helper > 0 || len(sudo) > 0):
		notes = append(notes, DryNoPassword)
	case helper > 0:
		n := rootStepsSudo(helper)
		if polkit {
			n = rootStepsPolkit(helper)
		}
		if len(sudo) > 0 {
			n += "; " + sudoSteps(plan, sudo)
		}
		switch {
		case kept:
			n += ". You type your password once, the first time a step needs it; archmaid keeps it for this run only."
		case polkit && has(steps.IDTopgrade):
			n += ". If topgrade's sudo needs your password, archmaid asks for it in its own dialog."
		case polkit:
			n += "."
		default:
			n += ". If sudo needs your password, archmaid asks for it in its own dialog."
		}
		notes = append(notes, n)
	case len(sudo) > 0 && kept:
		notes = append(notes, "You type your password once, the first time a step needs it; archmaid keeps it for this run only.")
	case len(sudo) > 0:
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

// rootStepsPolkit is the root count in polkit mode: "1 step runs as root,
// through one authorisation" or "{n} steps run as root, together, through
// one authorisation" (the Overview's kicker says "{n} root steps in one
// authorisation").
func rootStepsPolkit(n int) string {
	if n == 1 {
		return "1 step runs as root, through one authorisation"
	}
	return fmt.Sprintf("%d steps run as root, together, through one authorisation", n)
}

// rootStepsSudo is the root count in sudo mode: "1 step runs as root, through
// sudo" or "{n} steps run as root, each through sudo".
func rootStepsSudo(n int) string {
	if n == 1 {
		return "1 step runs as root, through sudo"
	}
	return fmt.Sprintf("%d steps run as root, each through sudo", n)
}

// sudoSteps names the steps that get root through their own sudo (idx,
// their positions in plan): "topgrade gets root through its own sudo" for
// topgrade alone, else by number ("step 4 gets…", "steps 2 and 5 get…").
func sudoSteps(plan []engine.PlanStep, idx []int) string {
	if len(idx) == 1 && plan[idx[0]].ID == steps.IDTopgrade {
		return "topgrade gets root through its own sudo"
	}
	if len(idx) == 1 {
		return fmt.Sprintf("step %d gets root through its own sudo", idx[0]+1)
	}
	nums := make([]string, len(idx))
	for i, j := range idx {
		nums[i] = strconv.Itoa(j + 1)
	}
	return "steps " + strings.Join(nums[:len(nums)-1], ", ") + " and " + nums[len(nums)-1] + " get root through their own sudo"
}
