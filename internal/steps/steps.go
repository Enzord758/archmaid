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

	// NeedsPriv steps need root. The front end authenticates once before a
	// queue holding any of them runs, instead of prompting mid-run, and only
	// their children may reach the password prompt. topgrade is one: it runs
	// sudo itself.
	NeedsPriv bool

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
	// runs as soon as the result is taken. (The audit's snapshot save.)
	Commit func() error
	// InsertNext, if set, is a step to run right after this one, inserted
	// into the queue when the result is taken (the orphan probe's removal);
	// it carries its own Confirm.
	InsertNext *Step
}

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
	IDTmpfiles      = "tmpfiles"
	IDJournal       = "journal"
	IDIntegrity     = "integrity"
	IDHyprpm        = "hyprpm"
	IDRepair        = "repair"
	IDDisableUnits  = "disable-units"
	IDDownloads     = "downloads"
	IDUserCache     = "user-cache"
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
)

// PrefetchAURAudit is the Prefetch of the AUR audit step: the front end
// starts the AUR lookup when the action is chosen.
const PrefetchAURAudit = "aur-audit"
