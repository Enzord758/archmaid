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

	Steps    func(Env) []steps.Step
	Discover func(ctx context.Context, env Env) Discovery
}

// Env is what the actions need from the front end that runs them.
//
// The zero value is not usable: AuditStep, AuditReport, Downloads and
// DeleteDownloads are required (an action calls them unchecked, and a nil
// one panics on the goroutine that runs it, mid-action). A front end checks
// its Env once with Validate when it starts. DryRun, LookPath and
// RepairWorkers are optional: their zero values mean a real run,
// exec.LookPath and DefaultRepairWorkers.
type Env struct {
	DryRun bool
	// Polkit is set when root comes from the root helper through pkexec
	// (the engine's AuthPolkit), not a sudo ticket: a discovery that needs
	// root then hands the work to a helper step instead of running sudo
	// (Repair's recheck). It changes nothing under sudo.
	Polkit bool
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
// NeedsSelection, Report or NeedsRoot.
type Discovery interface{ discovery() }

// Notice is a one-line result with nothing to do ("No broken packages found.").
type Notice struct{ Text string }

// Pending is a queue to run, authenticated first if any step needs root.
type Pending struct{ Steps []steps.Step }

// NeedsSelection asks the user to pick from Items; Steps builds the queue
// for what was picked (nothing picked: nothing to run).
type NeedsSelection struct {
	Items []string
	Steps func(selected []string) []steps.Step
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
	IDUserCache   = "usercache"
	IDRepair      = "repair"
	IDDownloads   = "downloads"
	IDTopgrade    = "topgrade"
	IDServices    = "services"
	IDAURAudit    = "auraudit"
	IDEditConfig  = "editconfig"
	IDViewLog     = "viewlog"
	IDExit        = "exit"
)

// Menu is the main menu, in order.
var Menu = []Action{
	{ID: IDFullCleanup, Icon: "󰓦", Label: "Full Cleanup (Recommended)", Steps: fullCleanupSteps, Gate: "Proceed with full cleanup?"},
	{ID: IDUserCache, Icon: "󰃨", Label: "Clear user cache", Steps: userCacheSteps},
	{ID: IDRepair, Icon: "󰚰", Label: "Repair broken packages", Discover: discoverRepair, Busy: "Scanning for broken packages…"},
	{ID: IDDownloads, Icon: "󰋊", Label: "Delete old downloads", Discover: discoverDownloads, Busy: "Checking Downloads…"},
	{ID: IDTopgrade, Icon: "󰏗", Label: "Run topgrade (system update)", Steps: topgradeSteps},
	{ID: IDServices, Icon: "󰒔", Label: "Disable unused services", Discover: discoverServices, Busy: "Finding unused services…"},
	{ID: IDAURAudit, Icon: "󰶚", Label: "Audit AUR packages", Discover: discoverAURAudit, Busy: "Checking foreign packages against the AUR…"},
	{ID: IDEditConfig, Icon: "󰙪", Label: "Edit topgrade config"},
	{ID: IDViewLog, Icon: "󰑇", Label: "View log"},
	{ID: IDExit, Icon: "󰗼", Label: "Exit"},
}

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
