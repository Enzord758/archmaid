package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── abort flow: ctrl+c while a step runs ────────────────────────────────────────

// TestAbortConfirmDoesNotQuit: ctrl+c on the running screen opens the abort
// confirmation instead of quitting the program.
func TestAbortConfirmShownNotQuit(t *testing.T) {
	m := fixedModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: steps.Step{Title: "big update"}, status: steps.Running}}
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

// startSleep starts `sleep 30` the way launch starts a step, so the abort
// tests stop a real proc.Child. It is stopped and released on cleanup.
func startSleep(t *testing.T) (*proc.Child, chan proc.Msg, chan struct{}) {
	t.Helper()
	ch, quit := make(chan proc.Msg, proc.BatchMax), make(chan struct{})
	child := runStep(steps.Step{Argv: []string{"sleep", "30"}}, ch, quit)
	if child == nil {
		t.Fatal("sleep did not start")
	}
	t.Cleanup(func() {
		child.Stop()
		select {
		case <-quit:
		default:
			close(quit)
		}
	})
	return child, ch, quit
}

// alive reports whether pid is still a process (signal 0 = existence check).
// Start's goroutine reaps the child as soon as it exits, so a dead child's pid
// stops existing.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestAbortDeclineResumes: answering "no" returns to the running screen and
// leaves the process handle intact.
func TestAbortDeclineResumes(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	// a harmless real process so we can prove it isn't killed
	child, _, _ := startSleep(t)
	m.proc = child

	got := update(m, key("n"))
	if got.screen != screenRunning {
		t.Fatalf("decline should resume running, got screen=%d", got.screen)
	}
	if got.proc == nil {
		t.Fatal("declining must not drop the process handle")
	}
	time.Sleep(50 * time.Millisecond) // give a wrongly sent SIGINT time to land
	if !alive(child.Pid) {
		t.Fatal("process should still be running after decline")
	}
}

// TestAbortAcceptSignalsAndMenus: answering "yes" stops the child and returns
// to the main menu.
func TestAbortAcceptSignalsAndMenus(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.confirmYes = true
	m.proc, m.stream, m.quit = startSleep(t)
	pid := m.proc.Pid

	got := update(m, key("enter"))
	if got.screen != screenMenu {
		t.Fatalf("accept should return to menu, got screen=%d", got.screen)
	}
	if got.proc != nil {
		t.Fatal("accept should clear the process handle")
	}
	// the child should be terminated by the signal
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatal("child was not terminated by abort")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAbortDoesNotLeakGoroutines: aborting a step that is still producing
// output must let every goroutine behind it finish. Before the fix, nothing
// read m.stream after an abort, so once its buffer filled the scanner blocked
// on the send, the pipe copier blocked on the scanner and cmd.Wait on the
// copier: three goroutines (plus the pending pump) leaked per aborted step.
// internal/proc tests the release itself; this is the model's half of it:
// abortRunning closes quit and stops the child.
func TestAbortDoesNotLeakGoroutines(t *testing.T) {
	old := sigkillGrace
	sigkillGrace = 10 * time.Millisecond
	defer func() { sigkillGrace = old }()

	base := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		m := fixedModel()
		m.queue = []stepState{{step: steps.Step{Title: "spam", LogLabel: "spam", Argv: []string{"yes"}}}}
		m.current = -1
		m.screen = screenRunning
		pump := m.advance() // launches `yes`, which floods the stream

		// Take one line through Update like the runtime would, leaving the
		// next pump outstanding, exactly as it is when ctrl+c arrives.
		next, pump := m.Update(pump())
		m = next.(model)
		go pump()

		// Abort only once the stream buffer is full, so the child really is
		// blocked behind it (the case that leaked).
		for len(m.stream) < cap(m.stream) {
			time.Sleep(time.Millisecond)
		}
		m.abortRunning()
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines: %d before, %d after 5 aborted steps — leaked %d", base, n, n-base)
	}
}

// TestLateOutputDroppedAfterAbort: once the run is aborted, stray stream
// messages from the dying child are dropped and don't drive state.
func TestLateOutputDroppedAfterAbort(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.queue = []stepState{{step: steps.Step{Title: "x", LogLabel: "x"}, status: steps.Running}}
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

	stepA := steps.Step{Title: "Step A…", LogLabel: "Step A", Argv: []string{"echo", "from-A"}}
	stepB := steps.Step{Title: "Step B…", LogLabel: "Step B", Argv: []string{"echo", "from-B"}}

	m := fixedModel()
	m.pending = []steps.Step{stepA}
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

	m.pending = []steps.Step{stepB}
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
		if m.screen != screenRunning || m.current != 0 || m.queue[0].status != steps.Running || m.errs != 0 {
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
	if m.screen != screenSummary || m.queue[0].status != steps.OK {
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
