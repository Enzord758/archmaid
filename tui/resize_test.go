package main

import (
	"net"
	"strings"
	"testing"
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
// m.bg must always name a screen viewOf can render. askpassMsg used to copy
// m.screen into m.bg verbatim (special-casing only screenPassword), so an
// askpass prompt interrupting the abort-confirm dialog recorded
// screenAbortConfirm as the background — a popup screen viewOf doesn't know
// how to draw, silently falling back to the main menu.

func TestAskpassOverAbortConfirmKeepsRunningAsBackground(t *testing.T) {
	m := fixedModel()
	m.screen = screenAbortConfirm
	m.bg = screenRunning // set by the ctrl+c handler, per the real flow
	m.queue = queueOf(1)
	m.current = 0

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	got := update(m, askpassMsg{prompt: "[sudo] password:", conn: c1})

	if got.bg != screenRunning {
		t.Fatalf("bg = %d, want screenRunning — an askpass prompt must not record a popup screen as its background", got.bg)
	}
	if got.screen != screenPassword {
		t.Fatalf("screen = %d, want screenPassword", got.screen)
	}
}

func TestAskpassOverPasswordScreenKeepsRealBackground(t *testing.T) {
	m := fixedModel()
	m.screen = screenPassword
	m.bg = screenMenu

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	got := update(m, askpassMsg{prompt: "[sudo] password:", conn: c1})

	if got.bg != screenMenu {
		t.Fatalf("bg = %d, want screenMenu (resolved through the existing background, not the password screen itself)", got.bg)
	}
}
