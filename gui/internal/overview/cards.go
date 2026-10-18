package overview

import (
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Card is one action cell of the Workbench Overview (P8): the menu entry
// with a one-line description and whether the action needs root. View log
// isn't one: the Logs page covers it (the owner's IA decision). Entries
// keeps today's menu, View log included, until the window switches over.
type Card struct {
	Entry
	// Desc is the cell's one line ("Files untouched for 30 days"), at most
	// MaxDescLen characters; Full Cleanup's hero text is the exception.
	Desc string
	// Root: the action has a step that needs root (sudo or polkit).
	Root bool
	// Tag is the cell's privilege tag, runview.PrivTag's words: "root" when
	// the action has a helper step, "sudo" when its privileged steps all get
	// root through their own sudo (topgrade), "" when it needs neither.
	Tag string
}

// MaxDescLen is how long a cell's text may be (the owner's 2026-10-18
// decision: short cells; Full Cleanup's hero text is the one long text).
const MaxDescLen = 40

// cardInfo is each action's description and privilege tag. The tag is
// static on purpose: a discovery's steps exist only once it has run, and
// the cell must say before the click whether a password may follow.
var cardInfo = map[string]struct {
	desc string
	tag  string
}{
	actions.IDFullCleanup: {"Audits your AUR packages, updates the system with topgrade, then removes orphans (you confirm the list), " +
		"trims the package cache, clears old temp files, vacuums the journal, checks package integrity and cleans the AUR build cache.", "root"},
	actions.IDClearCaches: {"Pick ~/.cache, the package cache or both", "root"},
	actions.IDRepair:      {"Reinstalls packages with missing files", "root"},
	actions.IDDownloads:   {"Files untouched for 30 days", ""},
	actions.IDTopgrade:    {"AUR audit, then a full update", "sudo"},
	actions.IDServices:    {"Enabled services that aren't running", "root"},
	actions.IDAURAudit:    {"Read-only check against the AUR", ""},
}

// Cards is the Overview's action cells, in the menu's order: every entry
// that starts an action (Entries without View log).
func Cards() []Card {
	var out []Card
	for _, e := range Entries() {
		if e.Kind != KindRun {
			continue
		}
		info := cardInfo[e.ID]
		out = append(out, Card{Entry: e, Desc: info.desc, Root: info.tag != "", Tag: info.tag})
	}
	return out
}

// PlanItem is one step of an action's plan as the Overview previews it.
type PlanItem struct {
	Title string
	// Root: archmaid runs the step as root (the root helper's verb).
	// Sudo: it runs as the user and gets root through its own sudo
	// (topgrade). They are runview.PrivTag's "root" and "sudo", which the
	// gate and the Run page tag the step with. Asks: it has its own
	// confirm. Combined: it runs inside the one
	// root-helper call (polkit's one dialog per Full Cleanup).
	Root, Sudo, Asks, Combined bool
}

// Preview is an action's plan before it starts.
type Preview struct {
	Steps []PlanItem
	// Root counts the steps tagged root, Sudo those tagged sudo.
	Root, Sudo int
	// Orphans: the plan finds orphans after the update and, when there
	// are any, inserts their removal (a root step with its own confirm)
	// behind the probe. The preview can't list that step: it doesn't
	// exist until the probe has run.
	Orphans bool
}

// PlanPreview builds action id's queue the way the engine does at Start
// (actions.Action.Steps with env; pass the run's own Env, so under polkit
// it is the polkit plan) and returns it as a preview. Building a queue
// runs nothing (docs/HANDOVER.md, "Full Cleanup's order"): only
// env.LookPath is asked whether hyprpm is installed. ok is false for an
// action that discovers its steps (Repair, downloads, services, the AUR
// audit) and for one that isn't on the menu.
func PlanPreview(env actions.Env, id string) (p Preview, ok bool) {
	a, found := actions.ByID(id)
	if !found || a.Steps == nil {
		return Preview{}, false
	}
	for _, s := range a.Steps(env) {
		it := PlanItem{Title: s.Title, Root: s.NeedsPriv && s.Verb != "", Sudo: s.NeedsPriv && s.Verb == "",
			Asks: s.Confirm != "", Combined: s.Combine != ""}
		if it.Root {
			p.Root++
		}
		if it.Sudo {
			p.Sudo++
		}
		if s.ID == steps.IDOrphansProbe {
			p.Orphans = true
		}
		p.Steps = append(p.Steps, it)
	}
	return p, true
}
