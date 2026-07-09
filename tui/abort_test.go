package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// ── abort flow: ctrl+c while a step runs ────────────────────────────────────────

// TestAbortConfirmDoesNotQuit: ctrl+c on the running screen opens the abort
// confirmation instead of quitting the program.
func TestAbortConfirmShownNotQuit(t *testing.T) {
	m := fixedModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: step{title: "big update"}, status: statusRunning}}
	m.current = 0

	next, cmd := m.Update(key("ctrl+c"))
	got := next.(model)
	if got.screen != screenAbortConfirm {
		t.Fatalf("ctrl+c should open abort confirm, got screen=%d", got.screen)
	}
	if cmd != nil {
		t.Fatal("ctrl+c should not issue a command (esp. not Quit)")
	}
	if got.confirmYes {
		t.Fatal("abort confirm should default to No")
	}
}

// TestAbortDeclineResumes: answering "no" returns to the running screen and
// leaves the process handle intact.
func TestAbortDeclineResumes(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	// a harmless real process so we can prove it isn't killed
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	m.proc = cmd.Process

	got := update(m, key("n"))
	if got.screen != screenRunning {
		t.Fatalf("decline should resume running, got screen=%d", got.screen)
	}
	if got.proc == nil {
		t.Fatal("declining must not drop the process handle")
	}
	// process should still be alive (signal 0 = existence check)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process should still be running after decline: %v", err)
	}
}

// TestAbortAcceptSignalsAndMenus: answering "yes" signals the child and returns
// to the main menu.
func TestAbortAcceptSignalsAndMenus(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.confirmYes = true

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	m.proc = cmd.Process

	got := update(m, key("enter"))
	if got.screen != screenMenu {
		t.Fatalf("accept should return to menu, got screen=%d", got.screen)
	}
	if got.proc != nil {
		t.Fatal("accept should clear the process handle")
	}
	// the child should be terminated by the signal
	select {
	case <-done: // exited — good
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("child was not terminated by abort")
	}
}

// TestLateOutputDroppedAfterAbort: once we've left the run (screen == menu),
// stray stream messages from the dying child are dropped and don't drive state.
func TestLateOutputDroppedAfterAbort(t *testing.T) {
	m := fixedModel()
	m.screen = screenMenu // already aborted back to menu
	m.queue = []stepState{{step: step{title: "x", logLabel: "x"}, status: statusRunning}}
	m.current = 0

	// a late failure message from the killed process
	next, cmd := m.handleStream(streamMsg{done: true, err: exec.ErrNotFound})
	got := next.(model)
	if got.screen != screenMenu {
		t.Fatalf("late output must not change screen, got %d", got.screen)
	}
	if cmd != nil {
		t.Fatal("late output must stop pumping (nil cmd)")
	}
	if got.errs != 0 {
		t.Fatalf("late output must not tally errors, errs=%d", got.errs)
	}
}

// TestAbortNilProcSafe: aborting with no live process (e.g. dry run) is a no-op
// that still returns to the menu.
func TestAbortNilProcSafe(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.proc = nil

	got := update(m, key("y"))
	if got.screen != screenMenu {
		t.Fatalf("abort with nil proc should still go to menu, got %d", got.screen)
	}
}
