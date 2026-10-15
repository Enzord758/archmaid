package tui

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"
)

// ── output pane sizing ───────────────────────────────────────────────────────
//
// The running screen stacks a checklist (one line per queued step) above the
// output box, on top of the same fixed chrome the log viewer budgets for.
// resizeOutput must shrink the pane by the checklist's height on that screen,
// or the rendered frame overflows the terminal (see viewRunningNeverExceedsTerminalHeight).

// queueOf builds n pending stepStates, the shape QueueStarted leaves behind.
func queueOf(n int) []stepState {
	q := make([]stepState, n)
	for i := range q {
		q[i] = stepState{step: steps.Step{Title: "step"}, status: steps.Pending}
	}
	return q
}

// askpassAsks is the engine asking for the running step's sudo password.
func askpassAsks() engine.NeedsPassword {
	return engine.NeedsPassword{ID: 9, Kind: engine.PasswordAskpass, Requester: "/usr/bin/yay (pid 5000150)",
		Prompt: "[sudo] password for u: ", AttemptsLeft: 3}
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
	m.queue[0].status = steps.Running
	m.resizeOutput()

	out := m.View()
	lines := strings.Count(out, "\n") + 1
	if lines > m.termHeight {
		t.Fatalf("viewRunning rendered %d lines, terminal is only %d rows tall", lines, m.termHeight)
	}
}

// TestRunNoteOnTheRunningScreen: a right password that comes with a note
// for the run (AuthResolved{Granted, Detail}: sudo keeps no ticket) shows
// it on the running screen, above the checklist, wrapped to the terminal;
// the output pane makes room for it at every width (the frame is as tall
// as without it); an askpass prompt's own AuthResolved doesn't
// replace it; the run's end clears it.
func TestRunNoteOnTheRunningScreen(t *testing.T) {
	const note = "sudo won't keep your password between prompts here, so archmaid asks for it in its own window each time sudo needs it."
	for _, width := range []int{40, 80, 120} {
		run := func(evs ...engine.Event) model {
			m, _ := engineModel()
			m = update(m, tea.WindowSizeMsg{Width: width, Height: 24})
			return feed(m, append(evs,
				engine.QueueStarted{Steps: []engine.PlanStep{{ID: "a", Title: "Step A"}, {ID: "b", Title: "Step B"}}},
				engine.StepStarted{ID: "a", Index: 0})...)
		}
		plain := strings.Count(run(engine.AuthResolved{Granted: true}).View(), "\n") + 1
		m := run(engine.AuthResolved{Granted: true, Detail: note})
		out := m.View()
		flat := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(flat, "Note: "+note) {
			t.Fatalf("width %d: the note isn't on the running screen:\n%s", width, out)
		}
		if strings.Index(out, "Note: ") > strings.Index(out, "Step A") {
			t.Fatalf("width %d: the note isn't above the checklist:\n%s", width, out)
		}
		if lines := strings.Count(out, "\n") + 1; lines != plain || lines > 24 {
			t.Fatalf("width %d: the running screen is %d lines with the note, %d without (24 rows):\n%s", width, lines, plain, out)
		}
		m = feed(m, engine.AuthResolved{Granted: true}) // an askpass prompt answered
		if m.runNote != note {
			t.Fatalf("width %d: an askpass answer changed the note to %q", width, m.runNote)
		}
		m = feed(m, engine.Finished{Outcome: engine.OutcomeCompleted})
		if m.runNote != "" {
			t.Fatalf("width %d: the note outlived the run: %q", width, m.runNote)
		}
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
	m.queue[0].status = steps.Running // the step the abort dialog is about is still running
	m.resizeOutput()

	got := feed(m, askpassAsks())

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
	m := fixedModel()
	m.queue = queueOf(1)
	m.current = 0
	m.queue[0].status = steps.Running
	m.screen = screenPassword
	m.bg = screenMenu

	got := feed(m, askpassAsks())

	if got.screen != screenPassword || !got.eng.askpassOpen() {
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
