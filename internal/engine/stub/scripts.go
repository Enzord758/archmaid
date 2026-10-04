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
		actions.IDUserCache:   UserCache(start),
		actions.IDServices:    Services(start),
		actions.IDAURAudit:    AURAudit(start),
		actions.IDRepair:      RepairNone(start),
	}
}

// The Catalog keys of the Full Cleanup variants that ask for a password.
const (
	IDFullCleanupUncached = actions.IDFullCleanup + "-uncached"
	IDFullCleanupAskpass  = actions.IDFullCleanup + "-askpass"
)

// CatalogUncached is Catalog with Full Cleanup run without a cached sudo
// ticket (FullCleanupUncached), so its first prompt is the password.
func CatalogUncached(start time.Time) map[string]Script {
	c := Catalog(start)
	c[actions.IDFullCleanup] = c[IDFullCleanupUncached]
	return c
}

// UserCache is "Clear user cache", shaped like the clearcache transcripts:
// the plan of one step with its confirm (prompt 1). Yes runs the step and
// completes. No or cancel skips it and ends Finished{OutcomeAborted,
// Skipped: 1}, as a declined required step does.
func UserCache(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}, branches: true}
	b.plan = []engine.PlanStep{{
		ID: steps.IDUserCache, Title: "Cleaning user cache…",
		Argv:    []string{"bash", "-c", `rm -rf "$HOME/.cache/"*`},
		Confirm: true, AlwaysLog: true, Category: logstore.Cleanup,
	}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseRunning})
	b.emit(engine.QueueStarted{Action: actions.IDUserCache, Steps: append([]engine.PlanStep(nil), b.plan...), Started: b.now})
	const q = "Clear all user cache?"
	b.emit(engine.NeedsConfirm{
		ID: 1, Kind: engine.ConfirmStep, Step: steps.IDUserCache,
		Title: q, Msg: q, Destructive: true, YesLabel: "Clear",
	})
	sb := b.branch()
	sb.skipped++
	sb.emit(engine.StepDone{ID: steps.IDUserCache, Index: 0, Status: steps.Skipped})
	sb.finish(actions.IDUserCache, engine.OutcomeAborted)
	b.script[len(b.script)-1].OnNo = sb.script

	b.run(steps.IDUserCache, 600*time.Millisecond)
	b.log(logstore.Cleanup, "OK: User cache cleared")
	b.finish(actions.IDUserCache, engine.OutcomeCompleted)
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
// repair_none: a Notice and Finished{OutcomeNotice}. It has no prompt.
func RepairNone(start time.Time) Script {
	b := &builder{now: start, started: start, at: map[string]time.Time{}}
	b.emit(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Scanning for broken packages…"})
	b.wait(900 * time.Millisecond)
	b.emit(engine.Notice{Text: "No broken packages found."})
	b.finish(actions.IDRepair, engine.OutcomeNotice)
	return b.script
}
