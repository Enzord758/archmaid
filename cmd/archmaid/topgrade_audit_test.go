package main

import (
	"os"
	"strings"
	"testing"
)

// ── Run topgrade's AUR audit gate ─────────────────────────────────────────────
//
// The standalone action opens with the same audit step as Full Cleanup and is
// gated the same way. These tests build the real topgradeSteps queue, then swap
// its topgrade step for harmlessTopgrade so nothing real can run.

// standaloneTopgrade is the Run topgrade action's topgrade step.
func standaloneTopgrade() step {
	steps := topgradeSteps()
	return steps[findStep(steps, "topgrade")]
}

// startTopgradeAction runs the Run topgrade queue's audit, with topgrade
// itself replaced by a stand-in, and feeds the result back through Update.
func startTopgradeAction(t *testing.T) model {
	t.Helper()
	steps := topgradeSteps()
	if len(steps) != 2 || findStep(steps, "topgrade") != 1 {
		t.Fatalf("Run topgrade queue = %+v, want [audit, topgrade]", steps)
	}
	m := initialModel()
	m.pending = []step{steps[0], harmlessTopgrade()}
	cmd := m.startQueue()
	if m.queue[0].status != statusRunning || cmd == nil {
		t.Fatalf("audit step should be running with a Cmd, status=%d", m.queue[0].status)
	}
	msg, ok := cmd().(stepResultMsg)
	if !ok {
		t.Fatalf("audit Cmd returned %T, want stepResultMsg", msg)
	}
	return update(m, msg)
}

// TestTopgradeActionAuditsFirst: the action's first step is the in-process
// audit, directly in front of topgrade.
func TestTopgradeActionAuditsFirst(t *testing.T) {
	s := topgradeSteps()[0]
	if s.run == nil || s.command != "" || s.category != logCategoryAudit || !s.alwaysLog {
		t.Fatalf("first step = %+v, want the in-process AUR audit", s)
	}
	if s.needsSudo || s.confirm != "" {
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
	got := startTopgradeAction(t)

	if got.screen != screenConfirm || got.bg != screenRunning {
		t.Fatalf("expected the gate confirm over the running screen, got screen=%d bg=%d", got.screen, got.bg)
	}
	if got.confirmMsg != "victim changed maintainer since the last audit. Update anyway?" || got.confirmYes {
		t.Fatalf("confirm = %q (yes=%v), want the named warning defaulting to No", got.confirmMsg, got.confirmYes)
	}
	if got.queue[1].status != statusPending || !got.queue[1].step.optional {
		t.Fatalf("topgrade must wait, optional, for the answer: %+v", got.queue[1])
	}
}

// TestTopgradeActionDeclineSkipsAndKeepsAlarm: "no" skips the update (on
// record), saves nothing, and the next run gates again.
func TestTopgradeActionDeclineSkipsAndKeepsAlarm(t *testing.T) {
	takeoverFixture(t)
	got := update(startTopgradeAction(t), key("n"))

	if got.queue[1].status != statusSkipped || got.screen != screenSummary || got.errs != 0 {
		t.Fatalf("topgrade status=%d screen=%d errs=%d, want skipped/summary/0", got.queue[1].status, got.screen, got.errs)
	}
	if log, _ := os.ReadFile(logPath(logCategoryTopgrade)); !strings.Contains(string(log), "SKIP: Topgrade completed (declined)") {
		t.Fatalf("topgrade.log should record the declined update:\n%s", log)
	}
	if snap, _ := loadBaseline(); snap["victim"].maintainer != "bob" {
		t.Fatalf("declining must not record the new maintainer, snapshot = %v", snap)
	}
	if again := startTopgradeAction(t); again.screen != screenConfirm {
		t.Fatalf("the next run should gate again, screen=%d", again.screen)
	}
}

// TestTopgradeActionAcceptRunsAndCommits: "yes" launches topgrade and saves
// the snapshot (logged ACCEPT), so the next run doesn't gate.
func TestTopgradeActionAcceptRunsAndCommits(t *testing.T) {
	takeoverFixture(t)
	got := update(startTopgradeAction(t), key("y"))

	if got.queue[1].status != statusRunning {
		t.Fatalf("topgrade should have launched, status=%d", got.queue[1].status)
	}
	if snap, _ := loadBaseline(); snap["victim"].maintainer != "mallory" {
		t.Fatalf("accepting should record the new maintainer, snapshot = %v", snap)
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); !strings.Contains(string(log), "ACCEPT: AUR maintainer changes accepted") {
		t.Fatalf("audit.log:\n%s", log)
	}
	if again := startTopgradeAction(t); again.queue[1].step.confirm != "" || again.queue[1].status != statusRunning {
		t.Fatalf("an accepted change must not gate again, confirm=%q status=%d", again.queue[1].step.confirm, again.queue[1].status)
	}
}

// TestTopgradeActionAuditFailureDoesNotGate: an unreachable AUR fails the
// audit step but topgrade still runs, unconfirmed.
func TestTopgradeActionAuditFailureDoesNotGate(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "something\n") // aurRPCURL stays TestMain's closed port
	got := startTopgradeAction(t)

	if got.queue[0].status != statusFailed || got.errs != 1 {
		t.Fatalf("audit status=%d errs=%d, want failed/1", got.queue[0].status, got.errs)
	}
	if got.queue[1].step.confirm != "" || got.queue[1].status != statusRunning {
		t.Fatalf("a failed audit must not gate topgrade, confirm=%q status=%d", got.queue[1].step.confirm, got.queue[1].status)
	}
}

// TestTopgradeActionDryRunNeverCommits: a dry run audits and gates, but
// accepting saves nothing.
func TestTopgradeActionDryRunNeverCommits(t *testing.T) {
	takeoverFixture(t)
	dryRun = true
	defer func() { dryRun = false }()

	gated := startTopgradeAction(t)
	if gated.screen != screenConfirm {
		t.Fatalf("a dry run should still gate, screen=%d", gated.screen)
	}
	update(gated, key("y"))
	if snap, _ := loadBaseline(); snap["victim"].maintainer != "bob" {
		t.Fatalf("a dry run must not advance the snapshot, snapshot = %v", snap)
	}
}
