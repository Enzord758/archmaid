// Package actions is archmaid's menu as data: each Action either expands into
// a queue of steps (Steps) or first looks at the system (Discover) and says
// what it found as a typed Discovery. A front end shows the menu, runs the
// queues and turns each Discovery into its own screens; this package only
// describes and discovers. It is UI-free.
package actions

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Action is one menu entry. Steps and Discover are mutually exclusive; an
// action with neither (edit config, view log, exit) is the front end's own,
// dispatched on ID.
type Action struct {
	ID    string
	Label string // "Full Cleanup (Recommended)"
	Icon  string // a Nerd Font glyph, drawn before the label
	// Gate, if set, is a confirm shown before the queue runs — but only when
	// no password prompt is shown first: the prompt is the gate otherwise.
	Gate string
	// Busy is what the front end says while Discover runs.
	Busy string
	// Record is the log a Discover action's record goes to when the action
	// ends at its discovery (nothing found, or the look failed): a
	// discovery category (logstore.Category.Discovery), so such a run
	// never takes a real run's place in the history. "" for a Steps action.
	Record logstore.Category

	Steps    func(Env) []steps.Step
	Discover func(ctx context.Context, env Env) Discovery
}

// Env is what the actions need from the front end that runs them.
//
// The zero value is not usable: AuditStep, AuditReport, Downloads and
// DeleteDownloads are required (an action calls them unchecked, and a nil
// one panics on the goroutine that runs it, mid-action), and so is
// PlanOrphans under Polkit (a nil one fails the orphan probe closed). A
// front end checks its Env once with Validate when it starts. DryRun, LookPath and
// RepairWorkers are optional: their zero values mean a real run,
// exec.LookPath and DefaultRepairWorkers.
type Env struct {
	DryRun bool
	// Polkit is set when root comes from the root helper through pkexec
	// (the engine's AuthPolkit), not a sudo ticket: a discovery that needs
	// root then hands the work to a helper step instead of running sudo
	// (Repair's recheck). It changes nothing under sudo.
	Polkit bool
	// PlanOrphans (required under Polkit) runs the root helper's read-only
	// plan-orphans as the user, never through pkexec, and returns its plan
	// (internal/helperclient's PlanOrphans; that package imports this one,
	// so it is handed in). Full Cleanup's orphan probe builds the removal
	// from it under polkit; nil there fails the probe and removes nothing.
	// Unused under sudo.
	PlanOrphans func(ctx context.Context) (steps.OrphanPlan, error)
	// PlanPackages (Polkit only; nil fails a Packages run closed with a
	// notice, so it is not in Validate) runs the helper's plan-install or
	// plan-remove as the user (internal/helperclient's PlanPackages). It
	// returns a *steps.PkgRefusal when the helper refused.
	PlanPackages func(ctx context.Context, req steps.PkgRequest) (steps.PkgPlan, error)
	// PlanAUR (Polkit only; nil fails an AUR run closed with a notice, so it
	// is not in Validate) resolves, fetches and reviews AUR packages as the
	// user (internal/frontend's AURPlanner over internal/aurbuild). It
	// returns a *steps.PkgRefusal when it refused.
	PlanAUR func(ctx context.Context, names []string) (AURPrepared, error)
	// LookPath finds a program on PATH; nil means exec.LookPath. Full
	// Cleanup asks it for hyprpm.
	LookPath func(file string) (string, error)
	// RepairWorkers is how many `pacman -Qkq` Repair's user scan runs side by
	// side; 0 means DefaultRepairWorkers.
	RepairWorkers int

	// The AUR audit lives with the front end for now, so it is handed in:
	// AuditStep (required) is the in-process audit step that opens Full
	// Cleanup and Run topgrade, called when their queue is built;
	// AuditReport (required) the menu's audit (a Notice or a Report), called
	// by its Discover, off the front end's UI thread.
	AuditStep   func() steps.Step
	AuditReport func(ctx context.Context) Discovery

	// Downloads (required) is the downloads cleaner's options, called when
	// the discovery runs, off the front end's UI thread; DeleteDownloads
	// (required) is its delete step's Run for a plan, called by that
	// discovery (the front end keeps the abort bookkeeping around it).
	Downloads       func() downloads.Options
	DeleteDownloads func(downloads.Plan) func(context.Context, steps.Env) steps.Result
}

// Validate reports the required fields e leaves nil, all of them in one
// error; nil when e is complete.
func (e Env) Validate() error {
	var missing []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"AuditStep", e.AuditStep != nil},
		{"AuditReport", e.AuditReport != nil},
		{"Downloads", e.Downloads != nil},
		{"DeleteDownloads", e.DeleteDownloads != nil},
		{"PlanOrphans (under Polkit)", !e.Polkit || e.PlanOrphans != nil},
	} {
		if !f.set {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("actions.Env: required %s not set", strings.Join(missing, ", "))
	}
	return nil
}

func (e Env) lookPath(file string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath(file)
	}
	return exec.LookPath(file)
}

// DefaultRepairWorkers is Repair's scan parallelism. The check is stat-bound
// and split over every installed package, so it scales with cores up to about
// 16 (1422 packages: 850 ms in one process, 501 ms at 4, 369 ms at 8, 316 ms
// at 16); more only adds processes.
var DefaultRepairWorkers = min(runtime.NumCPU(), 16)

func (e Env) repairWorkers() int {
	if e.RepairWorkers > 0 {
		return e.RepairWorkers
	}
	return DefaultRepairWorkers
}

// Discovery is what an action's Discover found: one of Notice, Pending,
// NeedsSelection, Report, NeedsRoot, or a Packages run's PkgPreview and
// PkgRefused.
type Discovery interface{ discovery() }

// Notice ends the action at its discovery: nothing to do, or the look
// itself failed. Text is the one-line summary of what was checked ("Checked
// 23 stopped services: none are enabled. Skipped 6 (systemd-*, …)."), or of
// what failed ("Couldn't list services: …").
type Notice struct {
	Text string
	// Err is set when the look failed (a listing or scan that couldn't
	// run): the failure alone, one line. "" when the look worked.
	Err string
	// Details and Log say what was checked, as Checked's do.
	Details []string
	Log     string
}

// Checked is what a discovery looked at, for its record: the run's log
// and a front end's details. Summary is one line ("Checked 23 stopped
// services: 3 are enabled."); Details the full lists, one entry per line
// (each unit with its is-enabled state, each skipped unit with the rule
// that skipped it); Log what only the log gets on top: the commands run
// and their raw output. All of it is untrusted text, sanitized by the
// engine and the log store; none of it may carry a secret.
type Checked struct {
	Summary string
	Details []string
	Log     string
}

// Pending is a queue to run, authenticated first if any step needs root.
// Checked, if set, is what the discovery that built it found.
type Pending struct {
	Steps   []steps.Step
	Checked Checked
}

// NeedsSelection asks the user to pick from Items; Steps builds the queue
// for what was picked (nothing picked: nothing to run).
type NeedsSelection struct {
	// Items are the pick's keys: what Steps gets back, and what a front
	// end answers with. For a plain list (services) they are also the rows.
	Items []string
	// Rows, if set (one per item), says how each row reads and whether it
	// can be ticked; nil shows the Items as they are.
	Rows    []PickRow
	Steps   func(selected []string) []steps.Step
	Checked Checked
}

// PickRow is one row of a pick that needs more than its key: Label is the
// row ("Entire package cache — 7.3 GiB (1398 files)"), Note a line that
// stays visible under or beside it (a warning, or why the row is disabled).
// A Disabled row can't be ticked; a pick of it is ignored.
type PickRow struct {
	Label, Note string
	Disabled    bool
}

// Report is a read-only report to show (the AUR audit). Accept, if set,
// accepts what the report flags (the audit's high findings: it saves them
// into the snapshot, so the next audit no longer flags them); nil when there
// is nothing to accept.
type Report struct {
	Text   string
	Accept func() error
	// Audit is the audit's result as data, for a front end that draws its
	// own report; nil for a report that isn't an audit.
	Audit *audit.Result
}

// NeedsRoot is a discovery that can only finish as root: the front end
// authenticates, then runs Rescan, whose Pending needs no second probe.
// Items is what the unprivileged pass flagged; Busy is what the front end
// says while Rescan runs, and AuthFailed the notice when authentication
// fails for good.
type NeedsRoot struct {
	Items      []string
	Busy       string
	AuthFailed string
	Rescan     func(ctx context.Context, env Env) Discovery
	Checked    Checked
}

func (Notice) discovery()         {}
func (Pending) discovery()        {}
func (NeedsSelection) discovery() {}
func (Report) discovery()         {}
func (NeedsRoot) discovery()      {}

// Action IDs. Stable: a front end dispatches on them, and last-run.json
// records them (the DMS widget reads that), so never rename one.
const (
	IDFullCleanup = "fullcleanup"
	IDClearCaches = "clearcaches"
	IDRepair      = "repair"
	IDDownloads   = "downloads"
	IDTopgrade    = "topgrade"
	IDServices    = "services"
	IDAURAudit    = "auraudit"
	IDEditConfig  = "editconfig"
	IDViewLog     = "viewlog"
	IDExit        = "exit"
)

// DoneHeadlines is what a clean run of an action that is not an update says
// in both front ends (the GUI's DONE hero, the TUI's outcome line):
// "Everything is up to date" would claim something about updates the action
// never looked at. Actions not listed (Full Cleanup, topgrade, an unknown or
// empty id) are not in it and keep their own wording.
var DoneHeadlines = map[string]string{
	IDClearCaches: "Caches cleared",
	IDRepair:      "Broken packages repaired",
	IDDownloads:   "Old downloads deleted",
	IDServices:    "Unused services disabled",
	IDAURAudit:    "AUR audit finished",
}

// Menu is the main menu, in order.
var Menu = []Action{
	{ID: IDFullCleanup, Icon: "󰓦", Label: "Full Cleanup (Recommended)", Steps: fullCleanupSteps, Gate: "Proceed with full cleanup?"},
	{ID: IDClearCaches, Icon: "󰃨", Label: "Clear caches", Discover: discoverClearCaches, Busy: "Measuring the caches…", Record: logstore.PkgCache},
	{ID: IDRepair, Icon: "󰚰", Label: "Repair broken packages", Discover: discoverRepair, Busy: "Scanning for broken packages…", Record: logstore.Repair},
	{ID: IDDownloads, Icon: "󰋊", Label: "Delete old downloads", Discover: discoverDownloads, Busy: "Checking Downloads…", Record: logstore.Downloads},
	{ID: IDTopgrade, Icon: "󰏗", Label: "Run topgrade (system update)", Steps: topgradeSteps, Gate: "Run a system update with topgrade?"},
	{ID: IDServices, Icon: "󰒔", Label: "Disable unused services", Discover: discoverServices, Busy: "Finding unused services…", Record: logstore.Services},
	{ID: IDAURAudit, Icon: "󰶚", Label: "Audit AUR packages", Discover: discoverAURAudit, Busy: "Checking foreign packages against the AUR…", Record: logstore.AURAudit},
	{ID: IDEditConfig, Icon: "󰙪", Label: "Edit topgrade config"},
	{ID: IDViewLog, Icon: "󰑇", Label: "View log"},
	{ID: IDExit, Icon: "󰗼", Label: "Exit"},
}

// Legacy action IDs: "Clear user cache" and "Delete entire package cache"
// were two menu entries until they became "Clear caches" (IDClearCaches).
// Old last-run.json and history files still carry these, so they keep a
// label (LegacyLabel); they are not on the menu.
const (
	IDUserCache = "usercache"
	IDWipeCache = "wipecache"
)

var legacyLabels = map[string]string{
	IDUserCache: "Clear user cache",
	IDWipeCache: "Delete entire package cache",
}

// LegacyLabel is the label of a retired action ID, "" for any other.
func LegacyLabel(id string) string { return legacyLabels[id] }

// ByID is the menu action with id.
func ByID(id string) (Action, bool) {
	for _, a := range Menu {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

func discoverAURAudit(ctx context.Context, env Env) Discovery { return env.AuditReport(ctx) }
