package runview

import (
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Prompts is a stub script that opens every prompt kind but the confirm
// (FullCleanup has those): a selection (which services), the up-front
// sudo password standing in for the action's gate, and sudo's askpass
// mid-step. Its prompt IDs are 1, 2 and 3, in that order.
func Prompts(start time.Time) stub.Script {
	disable := engine.PlanStep{ID: steps.IDDisableUnits, Title: "Disabling 2 services…", Argv: []string{"sudo", "systemctl", "disable", "--now", "--", "cups.service", "avahi-daemon.service"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup}
	update := engine.PlanStep{ID: steps.IDTopgrade, Title: "Updating system with topgrade…", Argv: []string{"topgrade", "-y"}, NeedsPriv: true, AlwaysLog: true, Category: logstore.Topgrade}
	ms := time.Millisecond
	return stub.Script{
		{Event: engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Listing enabled services…"}},
		{After: 150 * ms, Event: engine.NeedsSelection{ID: 1, Title: "Disable which services?", Items: []string{"cups.service", "avahi-daemon.service", "bluetooth.service", "ModemManager.service"}}},
		{Event: engine.PhaseChanged{Phase: engine.PhaseProbing}},
		{After: 60 * ms, Event: engine.NeedsPassword{ID: 2, Kind: engine.PasswordUpFront, AttemptsLeft: 3, Gate: "Disable the selected services?"}},
		{Event: engine.AuthWaiting{Kind: engine.WaitPasswordCheck}},
		{After: 300 * ms, Event: engine.AuthResolved{Granted: true, AttemptsLeft: 3}},
		{Event: engine.PhaseChanged{Phase: engine.PhaseRunning}},
		{Event: engine.QueueStarted{Action: "services", Steps: []engine.PlanStep{disable, update}, Started: start}},
		{Event: engine.StepStarted{ID: disable.ID, Index: 0, At: start}},
		{After: 200 * ms, Event: engine.OutputChunk{ID: disable.ID, Index: 0, Lines: []string{"Removed \"/etc/systemd/system/multi-user.target.wants/cups.service\".", "Removed \"/etc/systemd/system/multi-user.target.wants/avahi-daemon.service\"."}}},
		{Event: engine.StepDone{ID: disable.ID, Index: 0, Status: steps.OK, Elapsed: 200 * ms}},
		{Event: engine.StepStarted{ID: update.ID, Index: 1, At: start.Add(200 * ms)}},
		{After: 300 * ms, Event: engine.OutputChunk{ID: update.ID, Index: 1, Lines: []string{"\x1b[1;34m::\x1b[0m \x1b[1mSearching AUR for updates...\x1b[0m", " -> foo-bin 1.2-1 -> 1.3-1"}}},
		{After: 200 * ms, Event: engine.NeedsPassword{ID: 3, Kind: engine.PasswordAskpass, Step: update.ID, Requester: "yay (pid 4242)", Prompt: "[sudo] password for enzord:", AttemptsLeft: 3}},
		{After: 400 * ms, Event: engine.OutputChunk{ID: update.ID, Index: 1, Lines: []string{"\x1b[1;32m==>\x1b[0m Finished making: foo-bin 1.3-1", "\x1b[1;34m::\x1b[0m Installing foo-bin..."}}},
		{Event: engine.StepDone{ID: update.ID, Index: 1, Status: steps.OK, Elapsed: 1100 * ms}},
		{Event: engine.Finished{Action: "services", Outcome: engine.OutcomeCompleted, Elapsed: 1300 * ms}},
		{Event: engine.PhaseChanged{Phase: engine.PhaseIdle}},
	}
}

// Audit is a stub script for the AUR audit's report: two findings, one
// high, with CanAccept. Its prompt is ID 1.
func Audit() stub.Script {
	return stub.Script{
		{Event: engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Auditing AUR packages…"}},
		{After: 400 * time.Millisecond, Event: engine.OutputReport{ID: 1, Text: "12 foreign package(s) checked against the AUR: 1 high, 1 glance, 10 clean", Audit: &engine.AuditReport{
			Total: 12,
			Findings: []engine.AuditFinding{
				{Name: "foo-bin", High: true, Flags: []string{"maintainer changed", "source URL changed"}, Reason: "maintainer alice → mallory"},
				{Name: "bar-git", Flags: []string{"new package (3 days old)"}},
			},
			CanAccept: true,
		}}},
		{Event: engine.ReportAccepted{ID: 1, Saved: true, Text: "Maintainer changes accepted: foo-bin"}},
		{Event: engine.Finished{Action: "auraudit", Outcome: engine.OutcomeReport, Elapsed: 400 * time.Millisecond}},
		{Event: engine.PhaseChanged{Phase: engine.PhaseIdle}},
	}
}
