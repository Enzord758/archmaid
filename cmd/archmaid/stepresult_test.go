package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestResultConfirmAndInsertFailsStep: a result that both gates "the next
// step" and inserts one is refused — the confirm would land on the inserted
// step, not the one it was meant for. The step fails, nothing is inserted,
// the following step stays ungated and the deferred commit never runs.
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
	if m.queue[1].step.Confirm != "" || m.screen == screenConfirm {
		t.Fatalf("the following step was gated: confirm=%q screen=%d", m.queue[1].step.Confirm, m.screen)
	}
	if committed {
		t.Fatal("the commit ran for a refused result")
	}
	data, _ := os.ReadFile(logPath(logCategoryCleanup))
	if got := string(data); !strings.Contains(got, "FAIL: probe") || !strings.Contains(got, "found work") ||
		!strings.Contains(got, steps.ErrConfirmAndInsert.Error()) {
		t.Fatalf("log = %q, want the failure with the step's output and the refusal", got)
	}
}
