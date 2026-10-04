package main

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"
)

// runCmdQuick runs cmd and returns its msg, failing if it takes as long as a
// tick would (a one-shot refresh returns at once; a loop tick sleeps 4 s).
func runCmdQuick(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a status refresh cmd, got nil")
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("refresh cmd blocked like a tick: it would start another status loop")
		return nil
	}
}

// TestStatusLoopDoesNotMultiply: the refreshes after a finished queue and
// after the editor are one-off. They used to return refreshStatus(), and every
// statusMsg reschedules itself, so each one started another permanent 4 s loop
// (N+1 loops, each forking df/free/du, after N actions).
func TestStatusLoopDoesNotMultiply(t *testing.T) {
	m := feed(fixedModel(),
		engine.QueueStarted{Action: "x", Steps: []engine.PlanStep{{ID: "x", Title: "x", LogLabel: "x", Argv: []string{"x"}}}},
		engine.StepStarted{ID: "x", Index: 0},
		engine.StepDone{ID: "x", Index: 0, Status: steps.OK})
	next, cmd := m.Update(engineEventsMsg{evs: []engine.Event{engine.Finished{Outcome: engine.OutcomeCompleted}}})
	if next.(model).screen != screenSummary {
		t.Fatalf("expected the summary, got screen=%d", next.(model).screen)
	}
	afterRun := runCmdQuick(t, cmd)

	_, cmd = fixedModel().Update(editorDoneMsg{})
	afterEditor := runCmdQuick(t, cmd)

	for name, msg := range map[string]tea.Msg{"after a run": afterRun, "after the editor": afterEditor} {
		if _, ok := msg.(statusNowMsg); !ok {
			t.Fatalf("%s: refresh produced %T, want a one-off statusNowMsg", name, msg)
		}
		got, follow := fixedModel().Update(msg)
		if follow != nil {
			t.Fatalf("%s: a one-off refresh must not schedule another tick", name)
		}
		if got.(model).status != string(msg.(statusNowMsg)) {
			t.Fatalf("%s: header not updated", name)
		}
	}
}
