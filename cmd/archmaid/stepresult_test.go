package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestResultConfirmAndInsertFailsStep: a result that both gates "the next
// step" and inserts one is refused — the confirm would land on the inserted
// step, not the one it was meant for. It fails closed: the step fails,
// nothing is inserted and the deferred commit never runs, yet the gate still
// stands on the step it was meant for, so that step can't run unconfirmed;
// "yes" runs it (still without the commit).
func TestResultConfirmAndInsertFailsStep(t *testing.T) {
	oldLogDir := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = oldLogDir })

	m := initialModel()
	m.pending = []steps.Step{
		{Title: "probe", LogLabel: "probe", Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }},
		{Title: "after", LogLabel: "after", Argv: []string{"true"}},
	}
	m.startQueue()
	if m.queue[0].status != steps.Running {
		t.Fatalf("probe status = %d, want running", m.queue[0].status)
	}
	committed := false
	msg := stepResultMsg{seq: m.stepSeq, Result: steps.Result{
		Output:      "found work",
		ConfirmNext: "Update anyway?",
		Commit:      func() error { committed = true; return nil },
		InsertNext:  &steps.Step{Title: "inserted", LogLabel: "inserted", Argv: []string{"true"}},
	}}
	m = update(m, msg)
	defer m.abortRunning()

	if len(m.queue) != 2 || m.queue[1].step.Title != "after" {
		t.Fatalf("queue = %d steps (second %q), want nothing inserted", len(m.queue), m.queue[1].step.Title)
	}
	if m.queue[0].status != steps.Failed || m.errs != 1 {
		t.Fatalf("probe status=%d errs=%d, want failed", m.queue[0].status, m.errs)
	}
	next := m.queue[1]
	if next.step.Confirm != "Update anyway?" || !next.step.Optional || next.step.OnAccept != nil ||
		m.screen != screenConfirm || m.current != 1 || next.status != steps.Pending {
		t.Fatalf("the following step isn't gated: confirm=%q optional=%v onAccept=%v screen=%d current=%d status=%d",
			next.step.Confirm, next.step.Optional, next.step.OnAccept != nil, m.screen, m.current, next.status)
	}
	if committed {
		t.Fatal("the commit ran for a refused result")
	}
	data, _ := os.ReadFile(logPath(logCategoryCleanup))
	if got := string(data); !strings.Contains(got, "FAIL: probe") || !strings.Contains(got, "found work") ||
		!strings.Contains(got, steps.ErrConfirmAndInsert.Error()) {
		t.Fatalf("log = %q, want the failure with the step's output and the refusal", got)
	}

	// "yes" runs the gated step; the refused result's commit stays unrun.
	m = update(m, key("y"))
	if m.queue[1].status != steps.Running || committed {
		t.Fatalf("after yes: status=%d committed=%v, want the step running without the commit",
			m.queue[1].status, committed)
	}
}

// TestResultConfirmAndInsertDeclined: "no" at the gate a refused result left
// skips just that step, like any optional confirm.
func TestResultConfirmAndInsertDeclined(t *testing.T) {
	oldLogDir := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = oldLogDir })

	m := initialModel()
	m.pending = []steps.Step{
		{Title: "probe", LogLabel: "probe", Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }},
		{Title: "gated", LogLabel: "gated", Argv: []string{"true"}},
		{Title: "after", LogLabel: "after", Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }},
	}
	m.startQueue()
	m = update(m, stepResultMsg{seq: m.stepSeq, Result: steps.Result{
		ConfirmNext: "Run it anyway?",
		InsertNext:  &steps.Step{Title: "inserted", LogLabel: "inserted", Argv: []string{"true"}},
	}})
	if m.screen != screenConfirm || m.current != 1 {
		t.Fatalf("screen=%d current=%d, want the gate on step 2", m.screen, m.current)
	}
	m = update(m, key("n"))
	defer m.abortRunning()
	if m.queue[1].status != steps.Skipped || m.queue[2].status != steps.Running || m.aborted {
		t.Fatalf("statuses %d, %d (aborted %v), want the gated step skipped and the next one running",
			m.queue[1].status, m.queue[2].status, m.aborted)
	}
}

// TestDownloadsRefusedResultLogsNoAbort: a delete step whose result is
// refused never has its commit run, and the commit used to be the only thing
// that disarmed the abort record. Releasing the step's ctx must not then
// write an "aborted" entry for a run nobody aborted: cleanup.log holds the
// failure, nothing else.
func TestDownloadsRefusedResultLogsNoAbort(t *testing.T) {
	m, cmd, _ := startDownloadsDelete(t)
	msg, ok := cmd().(stepResultMsg) // the whole delete runs; its AfterFunc is armed
	if !ok {
		t.Fatalf("delete Cmd returned %T, want stepResultMsg", msg)
	}
	msg.ConfirmNext = "Next?" // forged: the downloads step never sets these
	msg.InsertNext = &steps.Step{Title: "inserted", LogLabel: "inserted", Argv: []string{"true"}}
	m = update(m, msg)
	if m.queue[0].status != steps.Failed {
		t.Fatalf("status = %d, want the refused result to fail the step", m.queue[0].status)
	}
	waitForDownloads(io.Discard) // the AfterFunc has run (or was disarmed)
	log, _ := os.ReadFile(logPath(logCategoryCleanup))
	if strings.Contains(string(log), "ABORT") || !strings.Contains(string(log), "FAIL: Old downloads deleted") ||
		strings.Count(string(log), ": Old downloads deleted") != 1 {
		t.Fatalf("cleanup.log should hold just the failure, no abort record:\n%s", log)
	}
}
