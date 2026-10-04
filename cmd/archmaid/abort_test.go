package main

import (
	"os"
	"os/exec"
	"strings"
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

// TestLateOutputDroppedAfterAbort: once the run is aborted, stray stream
// messages from the dying child are dropped and don't drive state.
func TestLateOutputDroppedAfterAbort(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.queue = []stepState{{step: step{title: "x", logLabel: "x"}, status: statusRunning}}
	m.current = 0
	m.stepSeq = 1
	m = update(m, key("y")) // abort back to the menu

	// a late failure message from the killed process, stamped by its pump
	next, cmd := m.handleStream(streamBatchMsg{seq: 1, done: true, err: exec.ErrNotFound})
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

// TestAbortEscalatesToSigkill: a child that ignores SIGINT is force-killed
// after the grace period.
func TestAbortEscalatesToSigkill(t *testing.T) {
	old := sigkillGrace
	sigkillGrace = 200 * time.Millisecond
	defer func() { sigkillGrace = old }()

	// bash that traps (ignores) SIGINT, then sleeps
	cmd := exec.Command("bash", "-c", "trap '' INT; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	m := fixedModel()
	m.screen = screenAbortConfirm
	m.confirmYes = true
	m.proc = cmd.Process

	update(m, key("enter")) // SIGINT (ignored) → escalateKill → SIGKILL after grace

	select {
	case <-done: // killed — good
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		t.Fatal("SIGINT-ignoring child was not force-killed")
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

// TestStaleStreamAfterAbortAndRestart reproduces the abort/restart race
// deterministically: step A's pump has already taken a line and A's done off
// the channel (they sit in Bubble Tea's message queue) when ctrl+c aborts A,
// and a new queue (step B) starts before they are delivered. Neither may touch
// B: the line must not reach B's pane or log, the done must not finish B, and
// neither may re-pump (that would put a second reader on B's channel). B's own
// messages still go through.
func TestStaleStreamAfterAbortAndRestart(t *testing.T) {
	dryRun = true // the steps only report what they would run
	defer func() { dryRun = false }()
	useLogDir(t, t.TempDir())

	stepA := step{title: "Step A…", logLabel: "Step A", command: "echo", args: []string{"from-A"}}
	stepB := step{title: "Step B…", logLabel: "Step B", command: "echo", args: []string{"from-B"}}

	m := fixedModel()
	m.pending = []step{stepA}
	pumpA := m.beginPending()
	seqA := m.stepSeq

	// The pump delivers A's line and done — in one batch, or the done in a
	// second one that the abort releases and finds ready. Taken before the
	// abort so the test doesn't depend on which select case wins.
	late := pumpAll(t, m, pumpA)
	if got := late[0]; len(got.lines) != 1 || !strings.Contains(got.lines[0], "from-A") || got.seq != seqA {
		t.Fatalf("expected A's output line, got %#v", got)
	}

	m = update(m, key("ctrl+c"))
	m = update(m, key("y"))
	if m.screen != screenMenu {
		t.Fatalf("abort should return to the menu, got screen=%d", m.screen)
	}

	m.pending = []step{stepB}
	pumpB := m.beginPending()
	if m.screen != screenRunning || m.stepSeq == seqA {
		t.Fatalf("B should be running under a new seq: screen=%d seq=%d (A's %d)", m.screen, m.stepSeq, seqA)
	}

	for _, late := range late {
		next, cmd := m.Update(late)
		m = next.(model)
		if cmd != nil {
			t.Fatalf("stale %#v re-pumped the stream", late)
		}
		if strings.Contains(m.buf.String(), "from-A") {
			t.Fatalf("A's late line landed in B's pane:\n%s", m.buf.String())
		}
		if m.screen != screenRunning || m.current != 0 || m.queue[0].status != statusRunning || m.errs != 0 {
			t.Fatalf("stale %#v changed B: screen=%d current=%d status=%d errs=%d",
				late, m.screen, m.current, m.queue[0].status, m.errs)
		}
	}
	if _, err := os.Stat(logPath(logCategoryCleanup)); !os.IsNotExist(err) {
		t.Fatalf("stale messages wrote a log entry (stat err %v)", err)
	}

	// B's own line and done are applied as usual.
	for _, batch := range pumpAll(t, m, pumpB) {
		m = update(m, batch)
	}
	if !strings.Contains(m.buf.String(), "from-B") {
		t.Fatalf("B's line not applied:\n%s", m.buf.String())
	}
	if m.screen != screenSummary || m.queue[0].status != statusOK {
		t.Fatalf("B's done should finish it: screen=%d status=%d", m.screen, m.queue[0].status)
	}
	data, err := os.ReadFile(logPath(logCategoryCleanup))
	if err != nil {
		t.Fatalf("B's dry-run log not written: %v", err)
	}
	if log := string(data); !strings.Contains(log, "DRYRUN: Step B") || strings.Contains(log, "from-A") || strings.Contains(log, "Step A") {
		t.Fatalf("log should hold B only:\n%s", log)
	}
}
