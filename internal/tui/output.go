package tui

import "strings"

// ── output pane ──────────────────────────────────────────────────────────────
//
// The pane keeps only the newest maxVisibleLines of the running step's output
// (m.tail); the whole of it is the engine's, which logs it. The viewport
// re-splits its whole content on every SetContent, so feeding it everything
// made each batch cost as much as everything streamed so far; the tail keeps
// that cost flat however long the step runs.

// maxVisibleLines is how much of a step's output the pane keeps scrollable.
const maxVisibleLines = 2000

// lineTail holds the last max lines appended to it.
type lineTail struct {
	lines []string
	max   int
}

func newLineTail(max int) *lineTail { return &lineTail{max: max} }

// push appends lines and drops the oldest beyond max, returning how many
// were dropped from the front.
func (t *lineTail) push(lines []string) (dropped int) {
	t.lines = append(t.lines, lines...)
	if over := len(t.lines) - t.max; over > 0 {
		// Reslicing leaves the dropped heads behind in the backing array
		// only until append next reallocates, which copies just the live
		// tail — amortised O(1) per line.
		clear(t.lines[:over])
		t.lines = t.lines[over:]
		return over
	}
	return 0
}

func (t *lineTail) reset() {
	clear(t.lines)
	t.lines = t.lines[:0]
}

// String is the tail as the pane shows it: every line newline-terminated.
func (t *lineTail) String() string {
	n := 0
	for _, l := range t.lines {
		n += len(l) + 1
	}
	var b strings.Builder
	b.Grow(n)
	for _, l := range t.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// resetOutput empties the pane for a new step.
func (m *model) resetOutput() {
	m.tail.reset()
	m.output.SetContent("")
	m.output.GotoTop()
}

// appendOutput adds already-sanitized lines to the active step's output. The
// pane follows the newest line only if it was already showing the bottom: a
// user who scrolled up to read something keeps reading it, and when the tail
// drops lines from the front, their position moves up with the text instead
// of the text sliding under them.
func (m *model) appendOutput(lines []string) {
	follow := m.output.AtBottom()
	dropped := m.tail.push(lines)
	m.output.SetContent(m.tail.String())
	switch {
	case follow:
		m.output.GotoBottom()
	case dropped > 0:
		m.output.SetYOffset(m.output.YOffset - dropped)
	}
}
