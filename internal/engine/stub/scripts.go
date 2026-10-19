package stub

import (
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Catalog is a ready-made script for every menu action that runs, keyed by
// its action ID, for Options.Scripts: with it a front end can show each
// action and its declined paths. Each script's prompts carry the branches
// its own doc describes; answering no, or cancelling, takes the same one.
//
// The Driver picks a script by the Start's Action, so a second Full Cleanup
// needs a key of its own: the two ending in -uncached and -askpass are not
// menu actions but variants of IDFullCleanup (their Finished.Action is
// still "fullcleanup"), for a front end to Start by key when it wants to
// show the password prompts. CatalogUncached is the other way: the same
// Catalog with IDFullCleanup itself uncached.
func Catalog(start time.Time) map[string]Script {
	return map[string]Script{
		actions.IDFullCleanup: FullCleanupBranches(start),
		IDFullCleanupUncached: FullCleanupUncached(start),
		IDFullCleanupAskpass:  FullCleanupAskpass(start),
		actions.IDClearCaches: ClearCaches(start),
		actions.IDServices:    Services(start),
		actions.IDAURAudit:    AURAudit(start),
		actions.IDRepair:      RepairNone(start),
		IDClearCachesNone:     ClearCachesNone(start),
	}
}

// The Catalog keys of the Full Cleanup variants that ask for a password.
const (
	IDFullCleanupUncached = actions.IDFullCleanup + "-uncached"
	IDFullCleanupAskpass  = actions.IDFullCleanup + "-askpass"
	// IDClearCachesNone is Clear caches finding both caches empty.
	IDClearCachesNone = actions.IDClearCaches + "-none"
)

// CatalogUncached is Catalog with Full Cleanup run without a cached sudo
// ticket (FullCleanupUncached), so its first prompt is the password.
func CatalogUncached(start time.Time) map[string]Script {
	c := Catalog(start)
	c[actions.IDFullCleanup] = c[IDFullCleanupUncached]
	return c
}

// ClearCaches is "Clear caches", shaped like the Services script: the
// measuring discovery, then the pick (prompt 1) of ~/.cache (2.3 GiB) and
// the entire package cache (7.3 GiB, 1398 files, with its warning). A pick
// runs the one user-cache step whatever was ticked (the stub doesn't read
// the pick, and asks no password: ~/.cache needs none) and completes.
// Picking nothing, or cancelling, ends Finished{OutcomeCancelled} with no
// step run.
func ClearCaches(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	b.plan = []engine.PlanStep{{
		ID: steps.IDUserCache, Title: "Cleaning user cache…",
		Argv:      []string{"bash", "-c", `rm -rf "$HOME/.cache/"*`},
		AlwaysLog: true, Category: logstore.Cleanup,
	}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Measuring the caches…"})
	b.wait(400 * time.Millisecond)
	b.emit(engine.NeedsSelection{
		ID: 1, Title: "Clear caches — tick what to delete", Go: "clear",
		Items: []string{actions.PickUserCache, actions.PickPkgCache},
		Rows: []actions.PickRow{
			{Label: "~/.cache — 2.3 GiB"},
			{Label: "Entire package cache — 7.3 GiB (1398 files)", Note: "Can't reinstall or downgrade those packages without internet"},
		},
	})
	sb := b.branch()
	sb.finish(actions.IDClearCaches, engine.OutcomeCancelled)
	b.script[len(b.script)-1].OnNo = sb.script

	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: actions.IDClearCaches, Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	b.run(steps.IDUserCache, 600*time.Millisecond)
	b.log(logstore.Cleanup, "OK: User cache cleared")
	b.finish(actions.IDClearCaches, engine.OutcomeCompleted)
	return b.script
}

// Services is "Disable unused services", shaped like services_select2 and
// services_none_selected: discovery, then the pick list (prompt 1) of
// bluetooth, cups and docker. A pick runs the one step that disables
// bluetooth.service and docker.service whatever was picked (the stub
// doesn't read the pick), and completes. Picking none, or cancelling, ends
// Finished{OutcomeCancelled} with no step run.
func Services(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	b.plan = []engine.PlanStep{{
		ID: steps.IDDisableUnits, Verb: steps.VerbDisableUnit, Title: "Disabling services…",
		Argv:      []string{"sudo", "systemctl", "disable", "--now", "--", "bluetooth.service", "docker.service"},
		NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup,
	}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Finding unused services…"})
	b.wait(400 * time.Millisecond)
	b.emit(engine.NeedsSelection{
		ID: 1, Title: "Enabled but not running — select services to disable",
		Items: []string{"bluetooth.service", "cups.service", "docker.service"},
	})
	sb := b.branch()
	sb.finish(actions.IDServices, engine.OutcomeCancelled)
	b.script[len(b.script)-1].OnNo = sb.script

	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: actions.IDServices, Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	b.run(steps.IDDisableUnits, 500*time.Millisecond,
		"Removed '/etc/systemd/system/multi-user.target.wants/bluetooth.service'.",
		"Removed '/etc/systemd/system/multi-user.target.wants/docker.service'.")
	b.log(logstore.Cleanup, "OK: Services disabled")
	b.finish(actions.IDServices, engine.OutcomeCompleted)
	return b.script
}

// AURAudit is "Audit AUR packages", shaped like auraudit_takeover: an
// OutputReport (prompt 1) with two flagged packages that can be accepted.
// Yes publishes ReportAccepted{Saved: true} and ends
// Finished{OutcomeReport}; no or cancel dismisses the report and ends the
// same way without it.
func AURAudit(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking foreign packages against the AUR…"})
	b.wait(700 * time.Millisecond)
	b.log(logstore.Audit, "AUDIT: AUR audit: 2 checked, 2 flagged")
	b.emit(engine.OutputReport{
		ID: 1,
		Text: "2 foreign package(s) checked against the AUR\n\n" +
			"✖ 2 look closely  ⚠ 0 glance  ✔ 0 clean\n\n" +
			"✖ yay-bin  maintainer changed: jguer → mallory\n" +
			"✖ foo-git  adopted by eve (was orphaned)\n\n" +
			"Flagged maintainers stay flagged on every audit until you've checked them and press a to accept.\n" +
			"Recent = pushed within 72h, the same window as the yay cooldown hook.",
		Audit: &engine.AuditReport{
			Total: 2, CanAccept: true,
			Findings: []engine.AuditFinding{
				{Name: "yay-bin", High: true, Flags: []string{"maintainer changed"}, Reason: "maintainer changed: jguer → mallory"},
				{Name: "foo-git", High: true, Flags: []string{"adopted"}, Reason: "adopted by eve (was orphaned)"},
			},
		},
	})
	sb := b.branch()
	sb.finish(actions.IDAURAudit, engine.OutcomeReport)
	b.script[len(b.script)-1].OnNo = sb.script

	b.emit(engine.ReportAccepted{ID: 1, Saved: true, Text: "Maintainer changes accepted: they won't be flagged again."})
	b.finish(actions.IDAURAudit, engine.OutcomeReport)
	return b.script
}

// RepairNone is "Repair broken packages" finding nothing, shaped like
// repair_none: its DiscoveryResult (what was checked), the record's
// LogWritten, a Notice and Finished{OutcomeNotice}. It has no prompt. The
// stub writes nothing, so LogPath stays "".
func RepairNone(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Scanning for broken packages…"})
	b.wait(900 * time.Millisecond)
	const summary = "No broken packages found." // the GUI harness matches it; Details say what was checked
	b.emit(engine.DiscoveryResult{Action: actions.IDRepair, Outcome: engine.DiscoveryNothing, Summary: summary,
		Details:  []string{"1422 installed (pacman -Qq), checked with pacman -Qkq in 16 parallel chunks: nothing flagged"},
		Category: logstore.Repair, Started: start, Elapsed: 900 * time.Millisecond})
	b.emit(engine.LogWritten{Category: logstore.Repair, Header: "CHECK: " + summary})
	b.emit(engine.Notice{Text: summary})
	b.finish(actions.IDRepair, engine.OutcomeNotice)
	return b.script
}

// ClearCachesNone is "Clear caches" finding both caches empty (#207), shaped
// like the real discovery's end: DiscoveryResult{Nothing}, the record's
// LogWritten, the Notice (actions.NoCacheText) and Finished{OutcomeNotice}.
// No pick and no queue.
func ClearCachesNone(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Measuring the caches…"})
	b.wait(400 * time.Millisecond)
	const summary = actions.NoCacheText
	b.emit(engine.DiscoveryResult{Action: actions.IDClearCaches, Outcome: engine.DiscoveryNothing, Summary: summary,
		Details:  []string{"~/.cache: empty", "package cache: empty"},
		Category: logstore.PkgCache, Started: start, Elapsed: 400 * time.Millisecond})
	b.emit(engine.LogWritten{Category: logstore.PkgCache, Header: "CHECK: " + summary})
	b.emit(engine.Notice{Text: summary})
	b.finish(actions.IDClearCaches, engine.OutcomeNotice)
	return b.script
}
