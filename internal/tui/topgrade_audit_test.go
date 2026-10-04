package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// ── Run topgrade's AUR audit gate ─────────────────────────────────────────────
//
// The standalone action opens with the same audit step as Full Cleanup and is
// gated the same way. These tests build the real topgradeSteps queue, then swap
// its topgrade step for harmlessTopgrade so nothing real can run.

// standaloneTopgrade is the Run topgrade action's topgrade step.
func standaloneTopgrade() steps.Step {
	list := topgradeSteps()
	return list[findStep(list, "topgrade")]
}

// startTopgradeAction runs the Run topgrade queue on the engine, with
// topgrade itself replaced by a stand-in, up to its first prompt or end.
func startTopgradeAction(t *testing.T) auditQueue {
	t.Helper()
	list := topgradeSteps()
	if len(list) != 2 || findStep(list, "topgrade") != 1 {
		t.Fatalf("Run topgrade queue = %+v, want [audit, topgrade]", list)
	}
	return startQueue(t, list[0], harmlessTopgrade())
}

// TestTopgradeActionAuditsFirst: the action's first step is the in-process
// audit, directly in front of topgrade.
func TestTopgradeActionAuditsFirst(t *testing.T) {
	s := topgradeSteps()[0]
	if s.Run == nil || s.Command() != "" || s.Category != logCategoryAudit || !s.AlwaysLog {
		t.Fatalf("first step = %+v, want the in-process AUR audit", s)
	}
	if s.NeedsPriv || s.Confirm != "" {
		t.Fatalf("audit step needs no sudo and no confirm, got %+v", s)
	}
	if i := findStep(topgradeSteps(), "topgrade"); i != 1 {
		t.Fatalf("topgrade at index %d, want 1 (directly after the audit)", i)
	}
}

// TestTopgradeActionGatedOnHighFinding: a maintainer change puts a named
// confirm, defaulting to No, in front of topgrade before it starts.
func TestTopgradeActionGatedOnHighFinding(t *testing.T) {
	takeoverFixture(t)
	got := startTopgradeAction(t).m

	if got.screen != screenConfirm || got.bg != screenRunning {
		t.Fatalf("expected the gate confirm over the running screen, got screen=%d bg=%d", got.screen, got.bg)
	}
	if got.confirmMsg != "victim changed maintainer since the last audit. Update anyway?" || got.confirmYes {
		t.Fatalf("confirm = %q (yes=%v), want the named warning defaulting to No", got.confirmMsg, got.confirmYes)
	}
	if got.queue[1].status != steps.Pending || !got.queue[1].plan.Optional {
		t.Fatalf("topgrade must wait, optional, for the answer: %+v", got.queue[1])
	}
}

// TestTopgradeActionDeclineSkipsAndKeepsAlarm: "no" skips the update (on
// record), saves nothing, and the next run gates again.
func TestTopgradeActionDeclineSkipsAndKeepsAlarm(t *testing.T) {
	takeoverFixture(t)
	got := startTopgradeAction(t).answer("n").m

	if got.queue[1].status != steps.Skipped || got.screen != screenSummary || got.errs != 0 {
		t.Fatalf("topgrade status=%d screen=%d errs=%d, want skipped/summary/0", got.queue[1].status, got.screen, got.errs)
	}
	if log, _ := os.ReadFile(logPath(logCategoryTopgrade)); !strings.Contains(string(log), "SKIP: Topgrade completed (declined)") {
		t.Fatalf("topgrade.log should record the declined update:\n%s", log)
	}
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != "bob" {
		t.Fatalf("declining must not record the new maintainer, snapshot = %v", snap)
	}
	if again := startTopgradeAction(t).m; again.screen != screenConfirm {
		t.Fatalf("the next run should gate again, screen=%d", again.screen)
	}
}

// TestTopgradeActionAcceptRunsAndCommits: "yes" launches topgrade and saves
// the snapshot (logged ACCEPT), so the next run doesn't gate.
func TestTopgradeActionAcceptRunsAndCommits(t *testing.T) {
	takeoverFixture(t)
	got := startTopgradeAction(t).answer("y").m

	if got.queue[1].status != steps.OK {
		t.Fatalf("topgrade should have run, status=%d", got.queue[1].status)
	}
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != "mallory" {
		t.Fatalf("accepting should record the new maintainer, snapshot = %v", snap)
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); !strings.Contains(string(log), "ACCEPT: AUR maintainer changes accepted") {
		t.Fatalf("audit.log:\n%s", log)
	}
	if again := startTopgradeAction(t).m; again.queue[1].plan.Confirm || again.queue[1].status != steps.OK {
		t.Fatalf("an accepted change must not gate again, confirm=%v status=%d", again.queue[1].plan.Confirm, again.queue[1].status)
	}
}

// TestTopgradeActionAuditFailureDoesNotGate: an unreachable AUR fails the
// audit step but topgrade still runs, unconfirmed.
func TestTopgradeActionAuditFailureDoesNotGate(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "something\n") // aurRPCURL stays TestMain's closed port
	got := startTopgradeAction(t).m

	if got.queue[0].status != steps.Failed || got.errs != 1 {
		t.Fatalf("audit status=%d errs=%d, want failed/1", got.queue[0].status, got.errs)
	}
	if got.queue[1].plan.Confirm || got.queue[1].status != steps.OK {
		t.Fatalf("a failed audit must not gate topgrade, confirm=%v status=%d", got.queue[1].plan.Confirm, got.queue[1].status)
	}
}

// TestTopgradeActionDryRunNeverCommits: a dry run audits and gates, but
// accepting saves nothing.
func TestTopgradeActionDryRunNeverCommits(t *testing.T) {
	takeoverFixture(t)
	dryRun = true
	defer func() { dryRun = false }()

	gated := startTopgradeAction(t)
	if gated.m.screen != screenConfirm {
		t.Fatalf("a dry run should still gate, screen=%d", gated.m.screen)
	}
	gated.answer("y")
	if snap, _ := loadBaseline(); snap["victim"].Maintainer != "bob" {
		t.Fatalf("a dry run must not advance the snapshot, snapshot = %v", snap)
	}
}
