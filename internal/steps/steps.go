// Package steps describes the units of work archmaid runs: a Step is one
// command or in-process check, a menu action expands into a queue of them,
// and a front end (the TUI today) runs that queue in order. The package only
// describes; running a step's command is internal/proc's job and the order,
// confirms and auth are the front end's. It is UI-free.
package steps

import (
	"context"
	"errors"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// Step is one unit of work. Exactly one of Argv and Run is set: Argv is a
// command to start, Run an in-process step.
type Step struct {
	// ID names the step for good (see the ID constants); tests, logs and the
	// root helper key on it, never on Title.
	ID string
	// Verb is the root helper's verb that does this step's work, where one
	// exists (see the Verb constants); empty for steps the helper doesn't run.
	Verb string

	Title    string   // shown in the checklist and spinner ("Cleaning package cache…")
	LogLabel string   // written to the log ("Old package cache cleaned")
	Argv     []string // the command line, e.g. {"sudo", "paccache", "-r"}
	// Env is what the step's child gets in its environment on top of
	// what every child gets ("NAME=value" each; proc.Spec.ExtraEnv), never
	// set in archmaid's own: topgrade's CLICOLOR_FORCE, so it colours its
	// output into the pipe. A sudo resets it for the command it runs as
	// root. Nil for most steps.
	Env []string

	// Confirm, if set, is asked before the step runs. Optional decides what a
	// "no" means: skip just this step (Optional) vs. abort the whole queue.
	Confirm  string
	Optional bool
	// OnAccept, if set, runs when Confirm is answered yes, before the step
	// launches (the audit's snapshot commit; see Result.Commit).
	OnAccept func() error
	// Items is what the step acts on, for a front end to list under its
	// Confirm (the orphans it removes, the downloads it deletes), raw: the
	// front end makes them safe to show. Cascade is, for the orphan removal,
	// everything `pacman -Rns` takes along with Items.
	Items, Cascade []string
	// Plan, set only on the orphan removal under polkit, is the root
	// helper's plan its confirm shows: Items and Cascade are its Direct and
	// Cascade, and the helper's remove-orphans gets only its Hash (see
	// OrphanPlan). Nil under sudo, where Argv names the packages.
	Plan *OrphanPlan
	// PkgPlan, set only on a Packages install or remove step under polkit
	// (Verb VerbPkgInstall or VerbPkgRemove), is the helper's plan its
	// confirm showed; Items are its Targets. See pkgplan.go.
	PkgPlan *PkgPlan
	// PkgLocal, set only on the AUR install step (Verb VerbPkgInstallLocal),
	// is the built files and their hash. See aurplan.go.
	PkgLocal *PkgLocal

	// Combine, set only under polkit, names the root helper's combined verb
	// this step is a part of (VerbFullCleanup). A front end runs consecutive
	// steps with the same Combine as ONE helper call, so one polkit dialog
	// covers them all: their confirms are asked first, then the call runs
	// and reports each part's start and end, so every step keeps its own
	// row (engine: combine.go). Empty under sudo.
	Combine string
	// Parts, set only on the step a front end builds for such a call (never
	// by an action), are the steps it covers, in order: the root helper's
	// arguments are built from them (helperclient.Argv).
	Parts []Step

	// NeedsPriv steps need root. The front end authenticates once before a
	// queue holding any of them runs, instead of prompting mid-run, and only
	// their children may reach the password prompt. topgrade is one: it runs
	// sudo itself.
	NeedsPriv bool

	// SudoByName marks a step that isn't root work itself but whose program
	// can start sudo by name (hyprpm, which elevates itself to install its
	// headers). In the TUI its child gets what a NeedsPriv child gets, the
	// askpass and the sudo wrapper first on its PATH, so a sudo it starts
	// asks through archmaid's popup and never on the terminal. Unlike
	// NeedsPriv it asks for no up-front password, shows no root tag, is not
	// refused when the password was given up on and may run after hyprpm.
	// Under polkit (the GUI) an action leaves it unset.
	SudoByName bool

	// Category buckets this step's log entries; the zero value is
	// logstore.Cleanup (see LogCategory).
	Category logstore.Category
	// AlwaysLog logs this step's outcome even on success in a real run
	// (normally only failures are logged), and a SKIP entry when its Confirm
	// is declined — for steps whose outcome must stay on record: anything that
	// removes or overwrites something, the system upgrade, and the AUR audit.
	AlwaysLog bool

	// Run, if set, makes this an in-process step: the front end runs it off
	// its UI thread and its Result finishes the step. In-process steps run
	// even in a dry run, so Run must be read-only apart from what it gates on
	// env.DryRun itself. There's no child to signal: an abort cancels ctx, so
	// Run must return promptly once it's done, and its late Result is dropped.
	Run func(ctx context.Context, env Env) Result

	// Prefetch names work the front end starts the moment the action is
	// chosen, so it runs while the password prompt is up; the step then awaits
	// it instead of running Run (see PrefetchAURAudit).
	Prefetch string
}

// Asks reports whether the step's child gets the askpass and the sudo
// wrapper: it is root work (NeedsPriv) or its program can start sudo by name
// (SudoByName).
func (s Step) Asks() bool { return s.NeedsPriv || s.SudoByName }

// OrphanPlan is the root helper's orphan-removal plan, as its unprivileged
// plan-orphans reported it: Direct are the orphans (pacman -Qdtq), Cascade
// everything `pacman -Rns` removes with them, as "name version" lines
// (pacman -Rs --print, Direct included), and Hash ("sha256:<64 hex>") the
// helper's commitment to both. remove-orphans takes only Hash: the helper
// recomputes the plan as root and removes nothing unless it still hashes to
// it, so what is removed is what the confirm listed.
type OrphanPlan struct {
	Hash            string
	Direct, Cascade []string
}

// Env is what a running step may know about the run it is part of.
type Env struct {
	DryRun bool // report what would happen; change nothing
}

// LogCategory is s.Category, defaulting to logstore.Cleanup when unset.
func (s Step) LogCategory() logstore.Category {
	if s.Category == "" {
		return logstore.Cleanup
	}
	return s.Category
}

// Command is the program s starts (Argv[0]), or "" for an in-process step.
func (s Step) Command() string {
	if len(s.Argv) == 0 {
		return ""
	}
	return s.Argv[0]
}

// Args is s's command line after the program.
func (s Step) Args() []string {
	if len(s.Argv) == 0 {
		return nil
	}
	return s.Argv[1:]
}

// Result is what an in-process step returns. Output is shown and logged like
// a command's output; Err fails the step (InsertNext is then ignored).
type Result struct {
	Output string
	Err    error
	// ConfirmNext, if set, puts a confirm in front of the step right after
	// this one and makes it optional, so declining skips just that step (the
	// audit gates topgrade this way on a high-severity finding).
	ConfirmNext string
	// Commit, if set, is deferred work that must wait for that answer: it
	// becomes the next step's OnAccept, so it runs only if the confirm is
	// accepted and is dropped on "no" or an abort. Without a ConfirmNext it
	// runs as soon as the result is taken, even when Err is set (for the
	// downloads delete it says the result was taken, so a partial failure
	// isn't also logged as an abort). (The audit's snapshot save.)
	Commit func() error
	// InsertNext, if set, is a step to run right after this one, inserted
	// into the queue when the result is taken (the orphan probe's removal);
	// it carries its own Confirm.
	InsertNext *Step
	// Audit, set only by the AUR audit step, is what its run report keeps
	// of the outcome (internal/report). The engine ignores it.
	Audit *AuditNote
}

// AuditNote is an audit's outcome as the run report keeps it: how many
// foreign packages were checked and which were flagged.
type AuditNote struct {
	Total    int
	Findings []AuditFinding
}

// AuditFinding is one flagged package; High marks the severe ones.
type AuditFinding struct {
	Name string
	High bool
}

// ErrResultTaken is the cause a front end cancels an in-process step's ctx
// with once it has taken the step's Result (context.WithCancelCause). Every
// other cancel is an abort, which is how a Run that keeps bookkeeping past
// its return (the downloads delete's abort record) tells the two apart.
var ErrResultTaken = errors.New("step result taken")

// ErrConfirmAndInsert is Check's verdict on a result that sets both
// ConfirmNext and InsertNext: "the next step" would then be the inserted one,
// and the confirm would gate it instead of the step it was meant for.
var ErrConfirmAndInsert = errors.New("steps: a result may not set both ConfirmNext and InsertNext")

// Check reports whether r is well formed. A front end fails the step on an
// error rather than guess which step a ConfirmNext was meant to gate.
func (r Result) Check() error {
	if r.ConfirmNext != "" && r.InsertNext != nil {
		return ErrConfirmAndInsert
	}
	return nil
}

// Status is a step's state in a running queue.
type Status int

const (
	Pending Status = iota
	Running        // launched, not finished
	OK             // finished without error
	Failed         // finished with an error
	Skipped        // declined at its confirm, or behind a declined required one
	// AuthRefused: not run, privilege not granted — sudo authentication
	// failed for good, or the root helper exited 127 before its start line.
	// Why (a wrong password, a dismissed dialog, a locked account) is not
	// part of the status: pkexec exits 127 for all of them.
	AuthRefused
	Aborted // stopped by the user while it ran
)

// Stable step IDs. Never reuse one for a different step.
const (
	IDAURAudit      = "aur-audit"
	IDTopgrade      = "topgrade"
	IDPaccache      = "paccache"
	IDPaccacheWipe  = "paccache-wipe"
	IDOrphansProbe  = "orphans-probe"
	IDOrphansRemove = "orphans-remove"
	IDYayCache      = "yay-cache"
	// IDPkgCacheUninstalled is, under polkit only, the root half of yay -Sc
	// (pacman -Sc), run as a part of the root helper's full-cleanup; its
	// Verb is VerbFullCleanup, as it has no verb of its own.
	IDPkgCacheUninstalled = "pkg-cache-uninstalled"
	IDTmpfiles            = "tmpfiles"
	IDJournal             = "journal"
	IDIntegrity           = "integrity"
	IDHyprpm              = "hyprpm"
	IDRepair              = "repair"
	IDRepairCheck         = "repair-check"
	IDDisableUnits        = "disable-units"
	IDDownloads           = "downloads"
	IDUserCache           = "user-cache"
)

// The root helper's verbs (cmd/archmaid-helper), for the steps it can run.
const (
	VerbPaccache      = "paccache"
	VerbPaccacheWipe  = "paccache-wipe"
	VerbPlanOrphans   = "plan-orphans"
	VerbRemoveOrphans = "remove-orphans"
	VerbCleanTmp      = "clean-tmp"
	VerbVacuumJournal = "vacuum-journal"
	VerbCheckFiles    = "check-files"
	VerbRepair        = "repair"
	VerbDisableUnit   = "disable-unit"
	// The Packages page's verbs (docs/design/packages-contract.md). The
	// plan-* ones run as the user without pkexec, like VerbPlanOrphans.
	VerbPlanInstall = "plan-install"
	VerbPkgInstall  = "pkg-install"
	VerbPlanRemove  = "plan-remove"
	VerbPkgRemove   = "pkg-remove"
	// VerbPkgInstallLocal installs the AUR packages the user built
	// (aurplan.go, helper pkglocal.go): root, admin every time.
	VerbPkgInstallLocal = "pkg-install-local"
	// VerbFullCleanup is the combined verb: Full Cleanup's root steps under
	// one polkit dialog (see Step.Combine).
	VerbFullCleanup = "full-cleanup"
)

// PrefetchAURAudit is the Prefetch of the AUR audit step: the front end
// starts the AUR lookup when the action is chosen.
const PrefetchAURAudit = "aur-audit"
