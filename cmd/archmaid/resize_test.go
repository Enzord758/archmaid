package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ── output pane sizing ───────────────────────────────────────────────────────
//
// The running screen stacks a checklist (one line per queued step) above the
// output box, on top of the same fixed chrome the log viewer budgets for.
// resizeOutput must shrink the pane by the checklist's height on that screen,
// or the rendered frame overflows the terminal (see viewRunningNeverExceedsTerminalHeight).

// queueOf builds n pending stepStates, the shape startQueue leaves behind.
func queueOf(n int) []stepState {
	q := make([]stepState, n)
	for i := range q {
		q[i] = stepState{step: step{title: "step"}, status: statusPending}
	}
	return q
}

func TestResizeOutputShrinksForRunningQueue(t *testing.T) {
	m := fixedModel()
	m.termWidth, m.termHeight = 80, 24
	m.queue = queueOf(9) // worst case: Full Cleanup's step count

	m.screen = screenLog
	m.resizeOutput()
	logHeight := m.output.Height

	m.screen = screenRunning
	m.resizeOutput()
	runningHeight := m.output.Height

	if want := logHeight - 9; runningHeight != want {
		t.Fatalf("running screen height = %d, want %d (log height %d minus 9 checklist rows)",
			runningHeight, want, logHeight)
	}
}

func TestResizeOutputNeverGoesBelowOne(t *testing.T) {
	m := fixedModel()
	m.termWidth, m.termHeight = 80, 10 // short terminal
	m.queue = queueOf(9)
	m.screen = screenRunning

	m.resizeOutput()
	if m.output.Height < 1 {
		t.Fatalf("output height = %d, must never go below 1", m.output.Height)
	}
}

// viewRunningNeverExceedsTerminalHeight is a regression test for a real bug:
// the running screen's output pane was sized with the log viewer's fixed
// 8-row chrome budget, which doesn't account for the checklist. A full
// cleanup queue (9 steps) rendered 9 rows past the bottom of the terminal.
func TestViewRunningNeverExceedsTerminalHeight(t *testing.T) {
	m := fixedModel()
	m.termWidth, m.termHeight = 80, 24
	m.screen = screenRunning
	m.queue = queueOf(9)
	m.current = 0
	m.queue[0].status = statusRunning
	m.resizeOutput()

	out := m.View()
	lines := strings.Count(out, "\n") + 1
	if lines > m.termHeight {
		t.Fatalf("viewRunning rendered %d lines, terminal is only %d rows tall", lines, m.termHeight)
	}
}

// TestViewLogStaysWithinTerminalHeight pins the log viewer's own budget (the
// one resizeOutput's constant was originally tuned against) so a future
// change can't silently break it while fixing the running screen. The chrome
// fills to one row under the terminal height by construction; it must never
// exceed it.
func TestViewLogStaysWithinTerminalHeight(t *testing.T) {
	m := fixedModel()
	m.termWidth, m.termHeight = 80, 24
	m.screen = screenLog
	m.resizeOutput()

	out := m.View()
	lines := strings.Count(out, "\n") + 1
	if lines > m.termHeight {
		t.Fatalf("viewLog rendered %d lines, terminal is only %d rows tall", lines, m.termHeight)
	}
	if lines < m.termHeight-1 {
		t.Fatalf("viewLog rendered %d lines, want %d (tight fit against %d-row terminal)",
			lines, m.termHeight-1, m.termHeight)
	}
}

// ── askpass background tracking ──────────────────────────────────────────────
//
// Popups can stack: an askpass prompt can interrupt the abort-confirm dialog
// over a running step (the child hasn't been killed yet while the user
// decides). Two things must both hold when that happens:
//
//  1. rendering must be correct — the password popup shows over the abort
//     dialog, which shows over the checklist/output, not over the main menu
//     (the original bug: viewOf couldn't render a popup screen as a
//     background and silently fell back to it);
//  2. dismissing the password popup must restore the abort-confirm dialog,
//     not skip straight past it (a regression an earlier fix for #1
//     introduced by collapsing m.bg down to screenRunning, which silently
//     dropped the user's pending ctrl+c decision).

func TestAskpassOverAbortConfirmPreservesTheDialog(t *testing.T) {
	m := fixedModel()
	m.termWidth, m.termHeight = 80, 24 // a real size, or the popups render clipped to nothing
	m.screen = screenAbortConfirm
	m.bg = screenRunning // set by the ctrl+c handler, per the real flow
	m.queue = queueOf(1)
	m.current = 0
	m = sudoStepRunning(t, m) // the step the abort dialog is about is still running
	m.resizeOutput()

	c1, _ := pipeConn(t)
	got := update(m, helperAsks(c1))

	if got.screen != screenPassword {
		t.Fatalf("screen = %d, want screenPassword", got.screen)
	}
	if got.bg != screenAbortConfirm {
		t.Fatalf("bg = %d, want screenAbortConfirm — dismissing the password prompt must restore the pending abort dialog, not skip past it", got.bg)
	}
	if got.baseScreen(got.screen) != screenRunning {
		t.Fatalf("baseScreen(screen) = %d, want screenRunning — the checklist/output must still be the innermost layer", got.baseScreen(got.screen))
	}

	// Rendering the 3-layer stack (password over abort-confirm over running)
	// must terminate — the topmost popup legitimately obscures the dialog
	// directly behind it (same as any modal-over-modal stack), but the
	// running screen's own chrome, further out than either popup's box,
	// must still show through both.
	out := got.View()
	for _, want := range []string{"sudo access required", "ctrl+c abort", "step"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered view missing %q; nested popups aren't compositing correctly:\n%s", want, out)
		}
	}

	// esc cancels the askpass request and must land back on the abort
	// dialog, not the running screen underneath it.
	dismissed := update(got, key("esc"))
	if dismissed.screen != screenAbortConfirm {
		t.Fatalf("after dismissing askpass, screen = %d, want screenAbortConfirm restored", dismissed.screen)
	}
}

func TestAskpassOverPasswordScreenKeepsRealBackground(t *testing.T) {
	m := sudoStepRunning(t, fixedModel())
	m.screen = screenPassword
	m.bg = screenMenu

	c1, _ := pipeConn(t)
	req := helperAsks(c1)
	got := update(m, req)

	if got.screen != screenPassword || got.askpassReq != req.req {
		t.Fatal("the request didn't open the askpass prompt")
	}
	if got.bg != screenMenu {
		t.Fatalf("bg = %d, want screenMenu (resolved through the existing background, not the password screen itself)", got.bg)
	}
}

func TestBaseScreenResolvesNestedPopups(t *testing.T) {
	m := fixedModel()
	m.bg = screenAbortConfirm // as left by an askpass prompt over abort-confirm

	if got := m.baseScreen(screenPassword); got != screenRunning {
		t.Fatalf("baseScreen(screenPassword) = %d, want screenRunning (password -> abort-confirm -> running)", got)
	}
	if got := m.baseScreen(screenAbortConfirm); got != screenRunning {
		t.Fatalf("baseScreen(screenAbortConfirm) = %d, want screenRunning", got)
	}
	if got := m.baseScreen(screenMenu); got != screenMenu {
		t.Fatalf("baseScreen(screenMenu) = %d, want screenMenu unchanged (not a popup)", got)
	}
}

// TestResizeDuringPopupOverRunningStillBudgetsForChecklist is a regression
// test: resizeOutput used to compare m.termHeight's budget against
// m.screen == screenRunning directly, so a resize event that arrived while a
// popup (per-step confirm, abort-confirm, an askpass prompt) was showing
// over the running screen skipped the checklist subtraction entirely.
// Dismissing that popup back to screenRunning would then hit the exact
// overflow TestViewRunningNeverExceedsTerminalHeight guards against.
func TestResizeDuringPopupOverRunningStillBudgetsForChecklist(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.bg = screenRunning
	m.queue = queueOf(9) // worst case: Full Cleanup's step count

	got := update(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	got.screen = screenRunning // as if the user just dismissed the popup
	out := got.View()
	lines := strings.Count(out, "\n") + 1
	if lines > got.termHeight {
		t.Fatalf("viewRunning rendered %d lines after a resize-during-popup, terminal is only %d rows tall", lines, got.termHeight)
	}
}
