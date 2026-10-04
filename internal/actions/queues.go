package actions

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The fixed queues. Optional or destructive steps carry their own confirm, so
// a front end drives every queue the same way.

func fullCleanupSteps(env Env) []steps.Step {
	queue := []steps.Step{
		// The AUR audit runs first, directly in front of topgrade, so a
		// maintainer change can gate the update before yay builds anything
		// (see Env.AuditStep). Read-only; one network call.
		env.AuditStep(),
		// Then topgrade: update the system, then clean up the result. It's
		// also non-destructive and abortable, so it doubles as a soft buffer
		// before the destructive steps begin. NeedsPriv so credentials are
		// primed + kept alive — topgrade shells out to sudo pacman itself.
		// AlwaysLog: it changes more than any other step (a full system
		// upgrade), so its output is kept even when it succeeds.
		//
		// hyprpm is excluded and run as the LAST step instead: hyprpm drops the
		// sudo ticket on exit (NSys::root::dropSudo → sudo -k), which would kill
		// the primed credentials mid-queue and force a re-prompt for every
		// later sudo step.
		{
			ID:    steps.IDTopgrade,
			Title: "Updating system with topgrade…", LogLabel: "Topgrade completed",
			Argv: []string{"topgrade", "-y", "--disable", "hyprpm"}, NeedsPriv: true,
			Category: logstore.Topgrade, AlwaysLog: true,
		},
		{
			ID: steps.IDPaccache, Verb: steps.VerbPaccache,
			Title: "Cleaning package cache…", LogLabel: "Old package cache cleaned",
			Argv: []string{"sudo", "paccache", "-r"}, NeedsPriv: true,
		},
		{
			ID: steps.IDPaccacheWipe, Verb: steps.VerbPaccacheWipe,
			Title: "Deleting entire package cache…", LogLabel: "Entire package cache removed",
			Argv: []string{"sudo", "paccache", "-rk0"}, NeedsPriv: true,
			Confirm:   "Delete ENTIRE package cache? (can't reinstall without internet)",
			Optional:  true,
			AlwaysLog: true,
		},
		// Orphans are looked for here, after the update, not when the queue
		// is built: an upgrade can orphan packages of its own, and the list
		// the removal acts on must be the one its confirm showed. The probe
		// puts the removal step right after itself when it finds any (see
		// findOrphans), so building this queue runs nothing.
		orphanProbeStep(),
		{
			ID:    steps.IDYayCache,
			Title: "Cleaning AUR cache…", LogLabel: "AUR cache cleaned",
			Argv: []string{"yay", "-Sc", "--noconfirm"},
		},
		// Temp files are aged out by the system's own tmpfiles.d rules (on
		// Arch: /tmp after 10 days, /var/tmp after 30), not a hand-rolled
		// find/rm. The rules know what must survive — the sockets and lock
		// dirs of running sessions, and their x/X exclusions — where a blanket
		// `rm -rf` of anything two days old pulled those out from under live
		// programs.
		{
			ID: steps.IDTmpfiles, Verb: steps.VerbCleanTmp,
			Title: "Cleaning old temp files…", LogLabel: "Temp files cleaned per tmpfiles.d rules",
			Argv: []string{"sudo", "systemd-tmpfiles", "--clean"}, NeedsPriv: true,
			AlwaysLog: true,
		},
		{
			ID: steps.IDJournal, Verb: steps.VerbVacuumJournal,
			Title: "Vacuuming logs…", LogLabel: "Old logs removed",
			Argv: []string{"sudo", "journalctl", "--vacuum-time=2weeks"}, NeedsPriv: true,
			AlwaysLog: true,
		},
		{
			ID: steps.IDIntegrity, Verb: steps.VerbCheckFiles,
			Title: "Checking package integrity…", LogLabel: "Package integrity check completed",
			Argv: []string{"sudo", "pacman", "-Qkq"}, NeedsPriv: true,
		},
	}

	// Hyprland plugins go last: hyprpm drops the sudo ticket on exit, so after
	// this step nothing must need sudo anymore (see the topgrade note above).
	if _, err := env.lookPath("hyprpm"); err == nil {
		queue = append(queue, steps.Step{
			ID:    steps.IDHyprpm,
			Title: "Updating Hyprland plugins…", LogLabel: "Hyprland plugins updated",
			Argv: []string{"hyprpm", "update"},
		})
	}

	return queue
}

func topgradeSteps(env Env) []steps.Step {
	return []steps.Step{
		// The same AUR audit gate as Full Cleanup: topgrade runs yay, so a
		// maintainer change must be able to stop the update (see Env.AuditStep).
		env.AuditStep(),
		// NeedsPriv: topgrade shells out to sudo internally, so prime +
		// keep-alive credentials (otherwise its sudo prompt lands raw on the
		// terminal).
		{
			ID:    steps.IDTopgrade,
			Title: "Updating system with topgrade…", LogLabel: "Topgrade completed",
			Argv: []string{"topgrade", "-y"}, NeedsPriv: true,
			Category: logstore.Topgrade, AlwaysLog: true,
		},
	}
}

func userCacheSteps(Env) []steps.Step {
	return []steps.Step{{
		ID:    steps.IDUserCache,
		Title: "Cleaning user cache…", LogLabel: "User cache cleared",
		Argv:      []string{"bash", "-c", "rm -rf \"$HOME/.cache/\"*"},
		Confirm:   "Clear all user cache?",
		AlwaysLog: true,
	}}
}

// orphanProbeStep is Full Cleanup's orphan check: an in-process step that runs
// after the update and, if it finds orphans, puts their removal right after
// itself (see findOrphans). It only reads, so it runs in a dry run too.
func orphanProbeStep() steps.Step {
	return steps.Step{
		ID: steps.IDOrphansProbe, Verb: steps.VerbPlanOrphans,
		Title: "Finding orphaned packages…", LogLabel: "Orphaned packages checked",
		Category: logstore.Orphans,
		Run:      findOrphans,
	}
}

// findOrphans lists the orphans as they are now — after topgrade, which can
// orphan packages itself — and previews what `pacman -Rns` would take with
// them. With any found, the result inserts the removal step after the probe,
// with exactly these names and a confirm that shows both counts; with none,
// nothing follows. A failing query fails the step and inserts nothing: no
// removal is offered without its preview.
func findOrphans(ctx context.Context, _ steps.Env) steps.Result {
	orphans, err := orphanPackages(ctx)
	if err != nil {
		return steps.Result{Output: "Couldn't list orphaned packages: " + err.Error(), Err: err}
	}
	if len(orphans) == 0 {
		return steps.Result{Output: "No orphaned packages."}
	}
	cascade, err := orphanRemovalPreview(ctx, orphans)
	if err != nil {
		return steps.Result{Output: "Couldn't preview the orphan removal: " + err.Error(), Err: err}
	}
	msg := fmt.Sprintf("Remove %d orphaned package(s)?", len(orphans))
	if total := len(cascade); total > len(orphans) {
		msg = fmt.Sprintf("Remove %d orphaned package(s) — %d total once dependencies cascade?",
			len(orphans), total)
	}
	out := fmt.Sprintf("%d orphaned package(s): %s\npacman -Rns would remove %d package(s)",
		len(orphans), strings.Join(orphans, " "), len(cascade))
	if len(cascade) > 0 {
		out += ":\n  " + strings.Join(cascade, "\n  ")
	}
	return steps.Result{Output: out, InsertNext: &steps.Step{
		ID: steps.IDOrphansRemove, Verb: steps.VerbRemoveOrphans,
		Title: "Removing orphaned packages…", LogLabel: "Orphaned packages removed",
		Argv:      append([]string{"sudo", "pacman", "-Rns", "--noconfirm", "--"}, orphans...),
		NeedsPriv: true, Optional: true,
		Confirm:   msg,
		Category:  logstore.Orphans,
		AlwaysLog: true,
	}}
}

// orphanPackages lists packages installed as dependencies that nothing needs
// anymore (pacman -Qdtq). pacman reports "none" by exiting 1 without a word,
// so that is an empty list; any other failure is an error.
func orphanPackages(ctx context.Context) ([]string, error) {
	out, err := proc.Query(ctx, "pacman", "-Qdtq")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && len(exit.Stderr) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// orphanRemovalPreview lists what `pacman -Rns <orphans>` will actually
// remove: -s recursively drops each target's own dependencies too, once
// nothing else needs them, so the true total can run well past len(orphans)
// (a real run confirmed 23 and removed 46). --print performs no changes and
// needs no root; it conflicts with -n/--nosave, so this probe uses -Rs, not
// -Rns — the dropped -n only affects whether removed configs are kept as
// .pacsave, not what gets selected.
func orphanRemovalPreview(ctx context.Context, orphans []string) ([]string, error) {
	out, err := proc.Query(ctx, "pacman", append([]string{"-Rs", "--print", "--"}, orphans...)...)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
