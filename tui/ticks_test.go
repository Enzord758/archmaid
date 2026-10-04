package main

import (
	"testing"
	"time"

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
	m := fixedModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: step{title: "x", logLabel: "x"}, status: statusRunning}}
	m.current = 0
	next, cmd := m.handleStream(streamMsg{done: true}) // last step → summary
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

// TestKeepAliveSingleChain: a queue that ends (here: aborted) leaves its
// keep-alive tick pending for up to a minute. If another sudo queue starts
// meanwhile, that stale tick must die instead of running a second loop next
// to the new queue's own.
func TestKeepAliveSingleChain(t *testing.T) {
	m := update(sudoQueueModel(), sudoCachedMsg{cached: true}) // queue 1 arms gen 1
	if !m.sudoAlive {
		t.Fatal("first sudo queue should arm the keep-alive")
	}
	first := m.sudoGen

	m.abortRunning()
	m.pending = sudoQueueModel().pending
	m = update(m, sudoCachedMsg{cached: true}) // queue 2, within the minute
	if !m.sudoAlive || m.sudoGen == first {
		t.Fatalf("second queue should arm a new generation (alive=%v gen=%d, first=%d)", m.sudoAlive, m.sudoGen, first)
	}

	if _, cmd := m.Update(sudoKeepAliveMsg{gen: first}); cmd != nil {
		t.Fatal("the first queue's stale tick must not reschedule")
	}
	if _, cmd := m.Update(sudoKeepAliveMsg{gen: m.sudoGen}); cmd == nil {
		t.Fatal("the live queue's tick must reschedule")
	}
}
