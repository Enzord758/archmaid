package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── sudo -A on every sudo step ────────────────────────────────────────────────
//
// runStep must exec `sudo -A <args>` for sudo steps so any needed password is
// read via SUDO_ASKPASS (a TUI popup), never the tty. Asserted with a PATH
// shim standing in for the real binary, so nothing privileged runs.

// shimEchoArgs installs a fake `name` on PATH that prints its argv and exits 0.
func shimEchoArgs(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	// printf, not echo: echo would swallow a leading -n as its own flag.
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestSudoStepGetsDashA: a sudo step's argv is rewritten to `sudo -A <args>`.
func TestSudoStepGetsDashA(t *testing.T) {
	shimEchoArgs(t, "sudo")

	ch := make(chan streamMsg, 8)
	runStep(step{command: "sudo", args: []string{"paccache", "-r"}}, ch)
	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("shimmed sudo should exit 0, got %v", err)
	}
	if len(lines) != 1 || lines[0] != "-A paccache -r" {
		t.Fatalf("sudo argv = %v, want [-A paccache -r]", lines)
	}
}

// TestNonSudoStepUnchanged: a non-sudo command keeps its argv untouched.
func TestNonSudoStepUnchanged(t *testing.T) {
	shimEchoArgs(t, "topgrade")

	ch := make(chan streamMsg, 8)
	runStep(step{command: "topgrade", args: []string{"-y"}}, ch)
	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("shimmed topgrade should exit 0, got %v", err)
	}
	if len(lines) != 1 || lines[0] != "-y" {
		t.Fatalf("topgrade argv = %v, want [-y] (no -n injected)", lines)
	}
}

// TestTopgradeStepsNeedSudo: topgrade shells out to sudo itself, so both the
// standalone action and Full Cleanup's first step must be marked needsSudo to
// get priming + keep-alive.
func TestTopgradeStepsNeedSudo(t *testing.T) {
	if s := topgradeSteps()[0]; !s.needsSudo {
		t.Fatal("standalone topgrade step must be needsSudo")
	}
	shimPacman(t, "")
	steps := fullCleanupSteps()
	if i := findStep(steps, "topgrade"); i == -1 || !steps[i].needsSudo {
		t.Fatal("Full Cleanup's topgrade step must be needsSudo")
	}
}

// ── keep-alive loop lifecycle ─────────────────────────────────────────────────

// sudoQueueModel returns a model with a pending sudo step whose confirm pauses
// the queue before anything would exec.
func sudoQueueModel() model {
	m := initialModel()
	m.pending = []step{{title: "root job", logLabel: "root job",
		command: "sudo", args: []string{"true"}, needsSudo: true, confirm: "go?"}}
	return m
}

// TestKeepAliveStartsWithSudoQueue: a real-run sudo queue arms the keep-alive.
func TestKeepAliveStartsWithSudoQueue(t *testing.T) {
	m := sudoQueueModel()
	got := update(m, sudoCachedMsg{cached: true})
	if !got.sudoAlive {
		t.Fatal("sudoAlive should be armed for a sudo queue in a real run")
	}
	// The confirm pauses the queue: nothing has executed.
	if got.screen != screenConfirm {
		t.Fatalf("expected the step confirm, got screen=%d", got.screen)
	}
}

// TestKeepAliveNotStartedInDryRun: a dry run never arms the keep-alive (and
// never touches sudo).
func TestKeepAliveNotStartedInDryRun(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	got := update(sudoQueueModel(), sudoCachedMsg{cached: true})
	if got.sudoAlive {
		t.Fatal("dry run must not arm the sudo keep-alive")
	}
}

// TestKeepAliveReschedulesWhileAlive: a keep-alive msg while armed returns a
// follow-up cmd (refresh + next tick) and stays armed.
func TestKeepAliveReschedulesWhileAlive(t *testing.T) {
	m := initialModel()
	m.sudoAlive = true
	next, cmd := m.Update(sudoKeepAliveMsg{})
	if cmd == nil {
		t.Fatal("armed keep-alive should reschedule")
	}
	if !next.(model).sudoAlive {
		t.Fatal("keep-alive should stay armed")
	}
}

// TestKeepAliveNoopWhenCleared: once disarmed, a stale keep-alive msg does
// nothing — no refresh, no new tick.
func TestKeepAliveNoopWhenCleared(t *testing.T) {
	m := initialModel()
	m.sudoAlive = false
	_, cmd := m.Update(sudoKeepAliveMsg{})
	if cmd != nil {
		t.Fatal("disarmed keep-alive must not reschedule")
	}
}

// TestKeepAliveClearedOnSummary: finishing the queue disarms the keep-alive.
func TestKeepAliveClearedOnSummary(t *testing.T) {
	m := initialModel()
	m.screen = screenRunning
	m.sudoAlive = true
	m.queue = []stepState{{step: step{title: "x", logLabel: "x"}, status: statusRunning}}
	m.current = 0

	next, _ := m.handleStream(streamMsg{done: true})
	got := next.(model)
	if got.screen != screenSummary {
		t.Fatalf("expected summary, got screen=%d", got.screen)
	}
	if got.sudoAlive {
		t.Fatal("keep-alive must be disarmed on the summary")
	}
}

// TestKeepAliveClearedOnConfirmAbort: declining a non-optional confirm aborts
// the queue and disarms the keep-alive.
func TestKeepAliveClearedOnConfirmAbort(t *testing.T) {
	m := initialModel()
	m.screen = screenConfirm
	m.sudoAlive = true
	m.queue = []stepState{{step: step{title: "x", confirm: "?"}, status: statusPending}}
	m.current = 0
	m.confirmYes = false

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.screen != screenSummary || !got.aborted {
		t.Fatalf("expected aborted summary, got screen=%d aborted=%v", got.screen, got.aborted)
	}
	if got.sudoAlive {
		t.Fatal("keep-alive must be disarmed on a confirm abort")
	}
}

// TestKeepAliveClearedOnAbortRunning: aborting a running step disarms the
// keep-alive.
func TestKeepAliveClearedOnAbortRunning(t *testing.T) {
	m := initialModel()
	m.screen = screenRunning
	m.sudoAlive = true

	m.abortRunning() // nil proc (dry-run-like) is safe
	if m.sudoAlive {
		t.Fatal("keep-alive must be disarmed on abort")
	}
	if m.screen != screenMenu {
		t.Fatalf("abort should return to the menu, got screen=%d", m.screen)
	}
}

// ── pruneLogs ─────────────────────────────────────────────────────────────────

// TestPruneLogsDeletesOnlyStaleRunLogs: logs older than logRetention go; fresh
// logs, non-.log files, and directories stay.
func TestPruneLogsDeletesOnlyStaleRunLogs(t *testing.T) {
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	stale := filepath.Join(logDir, "20260101-0900.log")
	fresh := filepath.Join(logDir, "20260709-0900.log")
	other := filepath.Join(logDir, "notes.txt")
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(logDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-logRetention - 24*time.Hour)
	for _, p := range []string{stale, other} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	pruneLogs()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale run log should have been pruned")
	}
	for _, p := range []string{fresh, other, filepath.Join(logDir, "subdir")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should have survived pruning: %v", p, err)
		}
	}
}
