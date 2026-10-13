package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/steps"
)

// BenchmarkViewRunning is the cost of one frame of the Running screen
// mid-queue: header, a four-step checklist (one done, one running, two
// pending) and an output pane holding a full capped tail of pacman-style
// lines. The frame is redrawn on every output batch and spinner tick, so it
// must stay far under a frame budget; TestPerfGuard fixes it at 600 µs.
func BenchmarkViewRunning(b *testing.B) {
	m := update(fixedModel(), tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenRunning
	m.queue = goldenQueue(steps.OK, steps.Running, steps.Pending, steps.Pending)
	m.current = 1
	m.resizeOutput()
	lines := make([]string, maxVisibleLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("(%4d/%d) upgrading package-%d  [###########------] 64%%", i+1, len(lines), i)
	}
	m.writeOutput(0, false, lines)
	b.ReportAllocs()
	for b.Loop() {
		if m.viewRunning() == "" {
			b.Fatal("empty frame")
		}
	}
}
