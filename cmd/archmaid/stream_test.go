package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// pumpAll runs a step's pump to its done the way the runtime would, starting
// with first and re-pumping m's channel, and returns every batch it
// delivered. It never touches the model, so the batches can be fed later.
func pumpAll(t *testing.T, m model, first tea.Cmd) []streamBatchMsg {
	t.Helper()
	var out []streamBatchMsg
	for cmd := first; ; cmd = waitForStream(m.stream, m.quit, m.stepSeq) {
		b, ok := cmd().(streamBatchMsg)
		if !ok {
			t.Fatalf("pump delivered %#v, want a streamBatchMsg", b)
		}
		out = append(out, b)
		if b.done {
			return out
		}
	}
}

// streamingModel is a model sized like a terminal with one step running and
// its channel ready to be filled; feed it with the returned pump.
func streamingModel(s steps.Step, chanCap int) (model, tea.Cmd) {
	m := update(fixedModel(), tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenRunning
	m.queue = []stepState{{step: s, status: steps.Running}}
	m.current = 0
	m.resizeOutput()
	m.stepSeq = 1
	m.stream = make(chan proc.Msg, chanCap)
	m.quit = make(chan struct{})
	return m, waitForStream(m.stream, m.quit, m.stepSeq)
}

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// TestStreamPumpStampsSeq: the pump (proc.Next as a tea.Cmd) stamps every
// batch with its launch's seq and carries the lines and the done through;
// proc's own tests cover the batching.
func TestStreamPumpStampsSeq(t *testing.T) {
	ch := make(chan proc.Msg, 4)
	ch <- proc.Msg{Line: "one"}
	ch <- proc.Msg{Line: "two"}
	ch <- proc.Msg{Done: true, Err: os.ErrClosed}
	b := waitForStream(ch, nil, 7)().(streamBatchMsg)
	if b.seq != 7 || strings.Join(b.lines, ",") != "one,two" || !b.done || b.err != os.ErrClosed {
		t.Fatalf("got %#v, want seq 7, both lines and the done", b)
	}
}

// TestStreamPumpReleasedByAbort: a pump waiting on an empty channel returns
// nil once quit closes, so an abort leaves no goroutine behind.
func TestStreamPumpReleasedByAbort(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	if msg := waitForStream(make(chan proc.Msg), quit, 1)(); msg != nil {
		t.Fatalf("aborted pump returned %#v, want nil", msg)
	}
}

// TestScrollUpNotYankedDuringStream: the pane follows new output only while
// the user is at the bottom. Scrolled up, new batches leave them where they
// are — even once the tail starts dropping old lines, the line they were
// reading stays on top — and scrolling back down resumes following.
func TestScrollUpNotYankedDuringStream(t *testing.T) {
	m, _ := streamingModel(steps.Step{Title: "x", LogLabel: "x"}, 1)
	feed := func(lines []string) {
		t.Helper()
		next, cmd := m.Update(streamBatchMsg{seq: m.stepSeq, lines: lines})
		m = next.(model)
		if cmd == nil {
			t.Fatal("a batch without done must keep pumping")
		}
	}
	all := numbered(maxVisibleLines + 300)

	feed(all[:1800])
	if !m.output.AtBottom() {
		t.Fatalf("at the start the pane follows output: offset %d", m.output.YOffset)
	}

	for range 10 {
		m = update(m, key("up"))
	}
	if m.output.AtBottom() {
		t.Fatal("scrolling up should leave the bottom")
	}
	off := m.output.YOffset
	top := m.tail.lines[off]

	feed(all[1800:1900])
	if m.output.YOffset != off || m.output.AtBottom() {
		t.Fatalf("new output yanked the pane: offset %d → %d", off, m.output.YOffset)
	}

	// Push past the cap: the tail drops lines from the front, and the
	// offset moves with the text so the same line stays on top.
	feed(all[1900:])
	if n := len(m.tail.lines); n != maxVisibleLines {
		t.Fatalf("tail holds %d lines, want %d", n, maxVisibleLines)
	}
	if got := m.tail.lines[m.output.YOffset]; got != top {
		t.Fatalf("top line moved under the reader: %q, want %q", got, top)
	}

	m.output.GotoBottom()
	feed([]string{"fresh"})
	if !m.output.AtBottom() || !strings.Contains(m.output.View(), "fresh") {
		t.Fatalf("back at the bottom the pane should follow again:\n%s", m.output.View())
	}
}

// TestStreamAlwaysLogKeepsFullOutput: a 20k-line alwaysLog step logs every
// line in order, while the pane keeps only the newest maxVisibleLines.
func TestStreamAlwaysLogKeepsFullOutput(t *testing.T) {
	useLogDir(t, t.TempDir())
	const n = 20000
	m, pump := streamingModel(steps.Step{Title: "Updating…", LogLabel: "Updated",
		Category: logCategoryTopgrade, AlwaysLog: true}, n+1)
	lines := numbered(n)
	for _, l := range lines {
		m.stream <- proc.Msg{Line: l}
	}
	m.stream <- proc.Msg{Done: true}

	updates := 0
	for m.queue[0].status == steps.Running {
		next, cmd := m.Update(pump())
		m = next.(model)
		pump = cmd
		updates++
	}
	if m.queue[0].status != steps.OK {
		t.Fatalf("step status = %d, want OK", m.queue[0].status)
	}
	if want := (n + proc.BatchMax - 1) / proc.BatchMax; updates != want {
		t.Fatalf("%d lines took %d updates, want %d batches", n, updates, want)
	}

	if len(m.tail.lines) != maxVisibleLines || m.tail.lines[0] != lines[n-maxVisibleLines] || m.tail.lines[maxVisibleLines-1] != lines[n-1] {
		t.Fatalf("pane should hold lines %d..%d, holds %d lines starting %q",
			n-maxVisibleLines, n-1, len(m.tail.lines), m.tail.lines[0])
	}

	data, err := os.ReadFile(logPath(logCategoryTopgrade))
	if err != nil {
		t.Fatalf("alwaysLog step not logged: %v", err)
	}
	log := string(data)
	want := "--- Updated output ---\n" + strings.Join(lines, "\n") + "\n--- end output ---\n"
	if !strings.Contains(log, want) {
		t.Fatalf("log should hold all %d lines in order; got %d bytes", n, len(log))
	}
}

// benchStream measures what streaming n lines of output costs the UI thread:
// every message the pump delivers goes through Update and is followed by a
// View, as Bubble Tea's renderer does. The whole output is already buffered on
// the step's channel when the pump starts — a fast producer (topgrade, pacman
// -Qk) that stays ahead of the UI, which is the case that used to stall it.
func benchStream(b *testing.B, n int) {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("(%5d/%d) \x1b[1mupgrading\x1b[0m package-%d  [###########------] 64%%", i+1, n, i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m, pump := streamingModel(steps.Step{Title: "Streaming…", LogLabel: "Streamed"}, n+1)
		for _, l := range lines {
			m.stream <- proc.Msg{Line: l}
		}
		m.stream <- proc.Msg{Done: true}
		b.StartTimer()

		for m.queue[0].status == steps.Running {
			next, cmd := m.Update(pump())
			m = next.(model)
			_ = m.View()
			pump = cmd
		}
	}
}

func BenchmarkStreamView5k(b *testing.B)  { benchStream(b, 5000) }
func BenchmarkStreamView20k(b *testing.B) { benchStream(b, 20000) }
