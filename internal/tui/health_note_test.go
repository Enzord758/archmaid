package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
)

// TestHealthNoteNeverPushesTheScreenOff (#300): a notice under the list
// ("Command, copied…", "Ignored 2 items.") must leave the frame within the
// terminal at any height, with the selected row, the notice and the key help
// all on it.
func TestHealthNoteNeverPushesTheScreenOff(t *testing.T) {
	healthTmpStore(t)
	for _, h := range []int{8, 10, 12, 14, 18, 24, 30} {
		for _, w := range []int{40, 80, 120} {
			m := healthModel(t, healthFixture())
			m = update(m, tea.WindowSizeMsg{Width: w, Height: h})
			for i := range healthRows(healthFixture()) {
				for _, k := range []string{"c", "o", "i", "u", "down"} {
					m = update(m, key(k))
					v := m.View()
					if got := lipgloss.Height(v); got > h {
						t.Fatalf("%dx%d row %d after %q: frame is %d lines\n%s", w, h, i, k, got, v)
					}
					if !strings.Contains(v[strings.LastIndex(v, "\n↑")+1:], "↑/↓ move") {
						t.Fatalf("%dx%d row %d after %q: the key help is off the screen\n%s", w, h, i, k, v)
					}
					if !strings.Contains(v, "➤") {
						t.Fatalf("%dx%d row %d after %q: the selected row is off the screen\n%s", w, h, i, k, v)
					}
					if m.health.note != "" && !strings.Contains(v, strings.Fields(m.health.note)[0]) {
						t.Fatalf("%dx%d row %d after %q: the note is off the screen\n%s", w, h, i, k, v)
					}
				}
			}
		}
	}
}

// TestHealthSmallHeightGolden: a notice at a small terminal height keeps the
// list scrolled to the selected row, the notice and the key help.
func TestHealthSmallHeightGolden(t *testing.T) {
	m := healthModel(t, healthFixture())
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 14})
	m = update(m, key("down"))
	m = update(m, key("down"))
	m = update(m, key("o"))
	for _, k := range []string{"down", "down", "down", "down"} {
		m = update(m, key(k))
	}
	m = update(m, key("c"))
	teatest.RequireEqualOutput(t, []byte(m.View()))
}
