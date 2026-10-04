package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The output pane as the engine fills it: one OutputChunk per batch the
// Driver coalesced (internal/driver's outbox, which its own tests cover).

// streamingModel is a model sized like a terminal with one step running.
func streamingModel() model {
	m := update(fixedModel(), tea.WindowSizeMsg{Width: 120, Height: 40})
	return feed(m,
		engine.QueueStarted{Action: "x", Steps: []engine.PlanStep{{ID: "x", Title: "Streaming…", LogLabel: "Streamed", Argv: []string{"x"}}}},
		engine.StepStarted{ID: "x", Index: 0})
}

// chunk is lines as the running step's OutputChunk.
func chunk(lines []string) engine.OutputChunk {
	return engine.OutputChunk{ID: "x", Index: 0, Lines: lines}
}

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// TestScrollUpNotYankedDuringStream: the pane follows new output only while
// the user is at the bottom. Scrolled up, new chunks leave them where they
// are — even once the tail starts dropping old lines, the line they were
// reading stays on top — and scrolling back down resumes following.
func TestScrollUpNotYankedDuringStream(t *testing.T) {
	m := streamingModel()
	all := numbered(maxVisibleLines + 300)

	m = feed(m, chunk(all[:1800]))
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

	m = feed(m, chunk(all[1800:1900]))
	if m.output.YOffset != off || m.output.AtBottom() {
		t.Fatalf("new output yanked the pane: offset %d → %d", off, m.output.YOffset)
	}

	// Push past the cap: the tail drops lines from the front, and the
	// offset moves with the text so the same line stays on top.
	m = feed(m, chunk(all[1900:]))
	if n := len(m.tail.lines); n != maxVisibleLines {
		t.Fatalf("tail holds %d lines, want %d", n, maxVisibleLines)
	}
	if got := m.tail.lines[m.output.YOffset]; got != top {
		t.Fatalf("top line moved under the reader: %q, want %q", got, top)
	}

	m.output.GotoBottom()
	m = feed(m, chunk([]string{"fresh"}))
	if !m.output.AtBottom() || !strings.Contains(m.output.View(), "fresh") {
		t.Fatalf("back at the bottom the pane should follow again:\n%s", m.output.View())
	}
}

// TestStreamPaneKeepsTheNewest: a 20k-line step leaves the pane holding the
// newest maxVisibleLines, in order. (The log gets every line: the engine
// writes it, see internal/engine's TestSessionLogsByPolicy.)
func TestStreamPaneKeepsTheNewest(t *testing.T) {
	const n = 20000
	m := streamingModel()
	lines := numbered(n)
	for i := 0; i < n; i += proc.BatchMax {
		m = feed(m, chunk(lines[i:min(i+proc.BatchMax, n)]))
	}
	m = feed(m, engine.StepDone{ID: "x", Index: 0, Status: steps.OK})
	if m.queue[0].status != steps.OK {
		t.Fatalf("step status = %d, want OK", m.queue[0].status)
	}
	if len(m.tail.lines) != maxVisibleLines || m.tail.lines[0] != lines[n-maxVisibleLines] || m.tail.lines[maxVisibleLines-1] != lines[n-1] {
		t.Fatalf("pane should hold lines %d..%d, holds %d lines starting %q",
			n-maxVisibleLines, n-1, len(m.tail.lines), m.tail.lines[0])
	}
}

// benchStream measures what streaming n lines of output costs the UI thread:
// every chunk the Driver delivers goes through Update and is followed by a
// View, as Bubble Tea's renderer does. The chunks are proc.BatchMax lines,
// the most one pump read hands over: a fast producer (topgrade, pacman -Qk)
// that stays ahead of the UI, which is the case that used to stall it.
func benchStream(b *testing.B, n int) {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("(%5d/%d) \x1b[1mupgrading\x1b[0m package-%d  [###########------] 64%%", i+1, n, i)
	}
	var chunks []engineEventsMsg
	for i := 0; i < n; i += proc.BatchMax {
		chunks = append(chunks, engineEventsMsg{evs: []engine.Event{chunk(lines[i:min(i+proc.BatchMax, n)])}})
	}
	chunks = append(chunks, engineEventsMsg{evs: []engine.Event{engine.StepDone{ID: "x", Index: 0, Status: steps.OK}}})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m := streamingModel()
		b.StartTimer()

		for _, c := range chunks {
			next, _ := m.Update(c)
			m = next.(model)
			_ = m.View()
		}
	}
}

func BenchmarkStreamView5k(b *testing.B)  { benchStream(b, 5000) }
func BenchmarkStreamView20k(b *testing.B) { benchStream(b, 20000) }
