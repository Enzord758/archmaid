package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// ── Full Cleanup's AUR audit step + topgrade alwaysLog ────────────────────────
//
// The audit runs in-process as Full Cleanup's first step. These tests drive it
// with a shimmed pacman and a fake AUR (see audit_test.go helpers), and never
// let a real command run: the step after the audit is a harmless stand-in
// (`true`) wherever a gate failure would otherwise launch it.

// harmlessTopgrade has topgrade's logging shape but runs `true`, so a test that
// gets the gating wrong can't start a real system update.
func harmlessTopgrade() step {
	return step{
		title: "Updating system with topgrade…", logLabel: "Topgrade completed",
		command: "true", category: logCategoryTopgrade, alwaysLog: true,
	}
}

// startAudit builds a model running [audit, next...], executes the audit's
// Cmd synchronously, and feeds the result back through Update.
func startAudit(t *testing.T, next ...step) model {
	t.Helper()
	m := initialModel()
	m.pending = append([]step{auditStep()}, next...)
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

// takeoverFixture: one foreign package whose maintainer changed since the saved
// baseline (bob → mallory), plus one that didn't.
func takeoverFixture(t *testing.T) {
	t.Helper()
	auditEnv(t)
	shimPacmanForeign(t, "steady\nvictim\n")
	fakeAUR(t, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10},
		"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: time.Now().Add(-2 * time.Hour).Unix(), NumVotes: 10},
	})
	saveBaseline([]string{"steady", "victim"}, map[string]aurInfo{
		"steady": {Name: "steady", Maintainer: strp("alice")},
		"victim": {Name: "victim", Maintainer: strp("bob")},
	}, nil)
}

func TestFullCleanupAuditStepShape(t *testing.T) {
	shimPacman(t, "")
	s := fullCleanupSteps()[0]
	if s.run == nil || s.command != "" {
		t.Fatalf("audit step must be in-process, got %+v", s)
	}
	if s.category != logCategoryAudit || !s.alwaysLog {
		t.Fatalf("audit step must log every outcome to audit.log, got category=%q alwaysLog=%v", s.category, s.alwaysLog)
	}
	if s.needsSudo || s.confirm != "" {
		t.Fatalf("audit step needs no sudo and no confirm, got %+v", s)
	}
}

func TestTopgradeStepsAlwaysLog(t *testing.T) {
	shimPacman(t, "")
	full := fullCleanupSteps()
	i := findStep(full, "topgrade")
	for name, s := range map[string]step{"standalone": topgradeSteps()[0], "Full Cleanup": full[i]} {
		if !s.alwaysLog || s.category != logCategoryTopgrade {
			t.Errorf("%s topgrade: alwaysLog=%v category=%q, want true/topgrade", name, s.alwaysLog, s.category)
		}
	}
}

// TestRunAuditStepResults covers the step's own outcomes without the model.
func TestRunAuditStepResults(t *testing.T) {
	t.Run("high finding gates the next step", func(t *testing.T) {
		takeoverFixture(t)
		r := runAuditStep()
		if r.err != nil {
			t.Fatalf("err = %v", r.err)
		}
		if r.confirmNext != "victim changed maintainer since the last audit. Update anyway?" {
			t.Fatalf("confirmNext = %q", r.confirmNext)
		}
		if !strings.Contains(r.output, "[HIGH] victim") || !strings.Contains(r.output, "bob → mallory") {
			t.Fatalf("output:\n%s", r.output)
		}
		if strings.Contains(r.output, "\x1b[") {
			t.Fatal("step output is logged, so it must be plain text (no ANSI)")
		}
	})
	t.Run("nothing high does not gate", func(t *testing.T) {
		auditEnv(t)
		shimPacmanForeign(t, "steady\n")
		fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})
		r := runAuditStep()
		if r.err != nil || r.confirmNext != "" {
			t.Fatalf("err=%v confirmNext=%q, want neither", r.err, r.confirmNext)
		}
		if !strings.Contains(r.output, "First audit") {
			t.Fatalf("first audit should say it saved the baseline:\n%s", r.output)
		}
	})
	t.Run("no foreign packages is a clean pass", func(t *testing.T) {
		auditEnv(t)
		shimPacmanForeign(t, "")
		r := runAuditStep()
		if r.err != nil || r.confirmNext != "" || !strings.Contains(r.output, "nothing to audit") {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("unreachable AUR fails the step", func(t *testing.T) {
		auditEnv(t)
		shimPacmanForeign(t, "something\n") // aurRPCURL stays TestMain's closed port
		r := runAuditStep()
		if r.err == nil || r.confirmNext != "" || !strings.Contains(r.output, "Couldn't reach the AUR") {
			t.Fatalf("got %+v", r)
		}
	})
}

func TestHighNames(t *testing.T) {
	f := func(names ...string) []auditFinding {
		var out []auditFinding
		for _, n := range names {
			out = append(out, auditFinding{name: n, high: true})
		}
		return append(out, auditFinding{name: "glance-only"}) // never named
	}
	cases := map[string][]auditFinding{
		"":                         f(),
		"a":                        f("a"),
		"a and b":                  f("a", "b"),
		"3 AUR packages (a, b, …)": f("a", "b", "c"),
	}
	for want, in := range cases {
		if got := highNames(in); got != want {
			t.Errorf("highNames(%d high) = %q, want %q", len(in)-1, got, want)
		}
	}
}

// TestAuditHighFindingGatesTopgrade: a maintainer change puts a confirm in
// front of topgrade; the report is in the output pane and on record.
func TestAuditHighFindingGatesTopgrade(t *testing.T) {
	takeoverFixture(t)
	got := startAudit(t, harmlessTopgrade(), step{title: "after", logLabel: "after", command: "true"})

	if got.queue[0].status != statusOK {
		t.Fatalf("audit status = %d, want statusOK", got.queue[0].status)
	}
	if got.screen != screenConfirm || got.bg != screenRunning {
		t.Fatalf("expected the gate confirm over the running screen, got screen=%d bg=%d", got.screen, got.bg)
	}
	if !strings.Contains(got.confirmMsg, "changed maintainer") || got.confirmYes {
		t.Fatalf("confirm = %q (yes=%v), want the maintainer warning defaulting to No", got.confirmMsg, got.confirmYes)
	}
	if next := got.queue[1].step; !next.optional || next.confirm == "" {
		t.Fatalf("topgrade should now be an optional, confirmed step, got %+v", next)
	}
	if got.queue[1].status != statusPending {
		t.Fatal("topgrade must not have started before the confirm is answered")
	}
	if !strings.Contains(got.buf.String(), "[HIGH] victim") {
		t.Fatalf("report missing from the output pane:\n%s", got.buf.String())
	}
	log, _ := os.ReadFile(logPath(logCategoryAudit))
	if !strings.Contains(string(log), "OK: AUR audit") || !strings.Contains(string(log), "[HIGH] victim") {
		t.Fatalf("audit.log:\n%s", log)
	}
}

// TestDecliningGatedTopgradeSkipsOnlyTheUpdate: "no" skips topgrade (logged,
// since it's alwaysLog) and the cleanup carries on.
func TestDecliningGatedTopgradeSkipsOnlyTheUpdate(t *testing.T) {
	takeoverFixture(t)
	gated := startAudit(t, harmlessTopgrade())

	got := update(gated, key("n"))
	if got.queue[1].status != statusSkipped {
		t.Fatalf("topgrade status = %d, want statusSkipped", got.queue[1].status)
	}
	if got.aborted {
		t.Fatal("declining the update must not abort the rest of the cleanup")
	}
	if got.screen != screenSummary || got.skipped != 1 || got.errs != 0 {
		t.Fatalf("screen=%d skipped=%d errs=%d, want summary/1/0", got.screen, got.skipped, got.errs)
	}
	log, _ := os.ReadFile(logPath(logCategoryTopgrade))
	if !strings.Contains(string(log), "SKIP: Topgrade completed (declined)") {
		t.Fatalf("topgrade.log should record the declined update:\n%s", log)
	}
}

// TestAuditFailureDoesNotGate: an unreachable AUR fails the audit step (on
// record in audit.log) but doesn't put a confirm in front of topgrade.
func TestAuditFailureDoesNotGate(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "something\n")
	got := startAudit(t, harmlessTopgrade())

	if got.queue[0].status != statusFailed || got.errs != 1 {
		t.Fatalf("audit status=%d errs=%d, want failed/1", got.queue[0].status, got.errs)
	}
	if got.queue[1].step.confirm != "" {
		t.Fatalf("a failed audit must not gate topgrade, confirm=%q", got.queue[1].step.confirm)
	}
	if got.queue[1].status != statusRunning {
		t.Fatalf("topgrade should have launched, status=%d", got.queue[1].status)
	}
	log, _ := os.ReadFile(logPath(logCategoryAudit))
	if !strings.Contains(string(log), "FAIL: AUR audit") {
		t.Fatalf("audit.log:\n%s", log)
	}
}

// TestStaleStepResultIsDropped: a result from an earlier launch (the run was
// aborted and restarted) or one arriving after the run ended changes nothing.
func TestStaleStepResultIsDropped(t *testing.T) {
	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: auditStep(), status: statusRunning}}
	m.current = 0
	m.stepSeq = 2

	stale := stepResultMsg{seq: 1, stepResult: stepResult{err: errors.New("old run")}}
	if got := update(m, stale); got.queue[0].status != statusRunning || got.errs != 0 {
		t.Fatalf("stale result applied: status=%d errs=%d", got.queue[0].status, got.errs)
	}

	m.screen = screenMenu // aborted
	late := stepResultMsg{seq: 2, stepResult: stepResult{err: errors.New("late")}}
	if got := update(m, late); got.queue[0].status != statusRunning || got.errs != 0 {
		t.Fatalf("late result after abort applied: status=%d errs=%d", got.queue[0].status, got.errs)
	}
}

// TestAuditStepRunsInDryRunAndLogsOnce: the audit is read-only, so a dry run
// performs it (without advancing the snapshot) and logs it exactly once.
func TestAuditStepRunsInDryRunAndLogsOnce(t *testing.T) {
	takeoverFixture(t)
	before, _ := os.ReadFile(baselinePath())
	dryRun = true
	defer func() { dryRun = false }()

	got := startAudit(t)
	if got.queue[0].status != statusOK || got.screen != screenSummary {
		t.Fatalf("status=%d screen=%d", got.queue[0].status, got.screen)
	}
	after, _ := os.ReadFile(baselinePath())
	if string(before) != string(after) {
		t.Fatal("a dry run must not advance the maintainer snapshot")
	}
	log, _ := os.ReadFile(logPath(logCategoryAudit))
	// Exactly one entry: the step's own DRYRUN record, and no separate
	// menu-style "AUDIT:" entry on top of it.
	if n := strings.Count(string(log), "DRYRUN: AUR audit"); n != 1 || strings.Contains(string(log), "AUDIT:") {
		t.Fatalf("want exactly one DRYRUN entry and no AUDIT entry:\n%s", log)
	}
}

// ── when the snapshot advances ────────────────────────────────────────────────
//
// Saving the snapshot is what acknowledges a maintainer change, so it waits
// for the gate's answer: "yes" records it, "no" leaves the alarm armed.

// TestAuditStepDoesNotSaveByItself: the step only computes; the save is a
// commit the model runs (immediately here, since nothing is flagged high).
func TestAuditStepDoesNotSaveByItself(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "steady\n")
	fakeAUR(t, map[string]aurInfo{"steady": {Name: "steady", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 10}})

	if r := runAuditStep(); r.commit == nil {
		t.Fatal("a real run's audit should hand back a snapshot commit")
	}
	if _, state := loadBaseline(); state != baselineMissing {
		t.Fatal("running the step alone must not save the snapshot")
	}
	startAudit(t, harmlessTopgrade())
	if snap, state := loadBaseline(); state != baselineOK || snap["steady"] != "alice" {
		t.Fatalf("with nothing high the snapshot is saved at once, state=%d snapshot=%v", state, snap)
	}
}

// TestDecliningGateKeepsTheAlarm: "no" leaves the snapshot as it was, so the
// next Full Cleanup gates on the same change again.
func TestDecliningGateKeepsTheAlarm(t *testing.T) {
	takeoverFixture(t)
	update(startAudit(t, harmlessTopgrade()), key("n"))
	if snap, _ := loadBaseline(); snap["victim"] != "bob" {
		t.Fatalf("declining must not record the new maintainer, snapshot = %v", snap)
	}

	again := startAudit(t, harmlessTopgrade())
	if again.screen != screenConfirm || !strings.Contains(again.confirmMsg, "victim changed maintainer") {
		t.Fatalf("the next run should gate again, screen=%d confirm=%q", again.screen, again.confirmMsg)
	}
}

// TestAcceptingGateRecordsTheChange: "yes" saves the snapshot (on record in
// audit.log) as topgrade launches, and the next run doesn't gate.
func TestAcceptingGateRecordsTheChange(t *testing.T) {
	takeoverFixture(t)
	got := update(startAudit(t, harmlessTopgrade()), key("y"))
	if got.queue[1].status != statusRunning {
		t.Fatalf("topgrade should have launched, status=%d", got.queue[1].status)
	}
	if snap, _ := loadBaseline(); snap["victim"] != "mallory" {
		t.Fatalf("accepting should record the new maintainer, snapshot = %v", snap)
	}
	if log, _ := os.ReadFile(logPath(logCategoryAudit)); !strings.Contains(string(log), "ACCEPT: AUR maintainer changes accepted") {
		t.Fatalf("audit.log:\n%s", log)
	}

	again := startAudit(t, harmlessTopgrade())
	if again.queue[1].step.confirm != "" || again.queue[1].status != statusRunning {
		t.Fatalf("an accepted change must not gate again, confirm=%q status=%d", again.queue[1].step.confirm, again.queue[1].status)
	}
}

// TestAcceptingGateInDryRunSavesNothing: a dry run has no commit to run.
func TestAcceptingGateInDryRunSavesNothing(t *testing.T) {
	takeoverFixture(t)
	dryRun = true
	defer func() { dryRun = false }()
	update(startAudit(t, harmlessTopgrade()), key("y"))
	if snap, _ := loadBaseline(); snap["victim"] != "bob" {
		t.Fatalf("a dry run must not advance the snapshot, snapshot = %v", snap)
	}
}

// TestGateWithoutUpdateDropsTheCommit: a high finding with no step after the
// audit has nothing to gate, so nothing is accepted and nothing is saved.
func TestGateWithoutUpdateDropsTheCommit(t *testing.T) {
	takeoverFixture(t)
	startAudit(t)
	if snap, _ := loadBaseline(); snap["victim"] != "bob" {
		t.Fatalf("snapshot = %v, want it unchanged", snap)
	}
}
