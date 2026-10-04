package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// lastRunModel is a model mid-queue for action id, with the given step statuses.
func lastRunModel(id string, statuses ...steps.Status) model {
	m := fixedModel()
	m.runAction = id
	m.screen = screenRunning
	for _, s := range statuses {
		m.queue = append(m.queue, stepState{step: steps.Step{Title: "s", LogLabel: "s"}, status: s})
	}
	m.current = len(m.queue) - 1
	return m
}

func readLastRun(t *testing.T) logstore.RunRecord {
	t.Helper()
	r, err := logs().ReadLastRun()
	if err != nil {
		t.Fatalf("ReadLastRun: %v", err)
	}
	return r
}

func TestLastRunOK(t *testing.T) {
	dir := t.TempDir()
	useLogDir(t, dir)
	m := lastRunModel("usercache", steps.OK, steps.OK)
	m.advance() // queue exhausted → summary
	if m.screen != screenSummary {
		t.Fatalf("screen = %d, want summary", m.screen)
	}
	r := readLastRun(t)
	if r.V != 1 || r.Action != "usercache" || r.Outcome != "ok" || r.StepsFailed != 0 || r.DryRun {
		t.Fatalf("record = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "last-run.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLastRunFailedAndSkipped(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("fullcleanup", steps.OK, steps.Failed, steps.Skipped)
	m.errs = 1
	m.advance()
	r := readLastRun(t)
	if r.Outcome != "failed" || r.StepsFailed != 1 || r.StepsSkipped != 1 {
		t.Fatalf("record = %+v", r)
	}
}

func TestLastRunNothingRanIsCancelled(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("fullcleanup", steps.AuthRefused, steps.AuthRefused) // sudo gave up
	m.advance()
	if r := readLastRun(t); r.Outcome != "cancelled" || r.StepsSkipped != 2 {
		t.Fatalf("record = %+v", r)
	}
}

func TestLastRunAborted(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("topgrade", steps.Running)
	m.screen = screenAbortConfirm
	m = update(m, key("y"))
	if m.screen != screenMenu {
		t.Fatalf("screen = %d, want menu", m.screen)
	}
	if r := readLastRun(t); r.Outcome != "aborted" || r.Action != "topgrade" {
		t.Fatalf("record = %+v", r)
	}
}

func TestLastRunDeclinedRequiredStep(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("fullcleanup", steps.OK, steps.Pending)
	m.current = 1
	m.screen = screenConfirm
	m.confirmYes = false
	m = update(m, key("enter"))
	if !m.aborted {
		t.Fatal("expected the queue to abort")
	}
	if r := readLastRun(t); r.Outcome != "aborted" || r.StepsSkipped != 1 {
		t.Fatalf("record = %+v", r)
	}
}

func TestLastRunDeclinedGateIsCancelled(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("fullcleanup")
	m.gateActive = true
	m.screen = screenConfirm
	m.confirmYes = false
	m = update(m, key("enter"))
	if m.screen != screenMenu {
		t.Fatalf("screen = %d, want menu", m.screen)
	}
	if r := readLastRun(t); r.Outcome != "cancelled" {
		t.Fatalf("record = %+v", r)
	}
}

func TestLastRunDryRunLeavesRealRecordAlone(t *testing.T) {
	dir := t.TempDir()
	useLogDir(t, dir)
	if err := logs().WriteLastRun(logstore.RunRecord{Action: "real", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	dryRun = true
	defer func() { dryRun = false }()
	m := lastRunModel("usercache", steps.OK)
	m.advance()

	raw, err := os.ReadFile(filepath.Join(dir, "last-run.json"))
	if err != nil || !strings.Contains(string(raw), `"action":"real"`) {
		t.Fatalf("real last-run.json was touched: %s %v", raw, err)
	}
	r := readLastRun(t) // dry slot, since dryRun is on
	if !r.DryRun || r.Action != "usercache" {
		t.Fatalf("dry record = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "dryrun", "last-run.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLastRunWrittenOncePerRun(t *testing.T) {
	useLogDir(t, t.TempDir())
	m := lastRunModel("usercache", steps.OK)
	m.advance()
	first := readLastRun(t)
	// Further end-of-run signals for the same run must not rewrite it.
	m.recordRun("aborted")
	m.abortRunning()
	if again := readLastRun(t); again != first {
		t.Fatalf("record rewritten: %+v -> %+v", first, again)
	}
	// A new run re-arms it.
	m.resetRun()
	m.recordRun("cancelled")
	if r := readLastRun(t); r.Outcome != "cancelled" {
		t.Fatalf("new run not recorded: %+v", r)
	}
}

func TestLastRunAbsentBeforeAnyRun(t *testing.T) {
	useLogDir(t, t.TempDir())
	if _, err := logs().ReadLastRun(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
}
