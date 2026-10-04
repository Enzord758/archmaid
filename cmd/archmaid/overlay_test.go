package main

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestOverlayCentersAndPreservesBackground: the popup lands centered and the
// background shows around it.
func TestOverlayCentersAndPreservesBackground(t *testing.T) {
	bg := strings.Repeat("."+strings.Repeat(".", 10)+"\n", 7) // 7 lines of 11 dots
	bg = strings.TrimRight(bg, "\n")
	fg := "ABC\nDEF" // 2x3 popup

	got := overlay(bg, fg)
	lines := strings.Split(got, "\n")

	if len(lines) != 7 {
		t.Fatalf("overlay changed line count: got %d, want 7", len(lines))
	}
	// bg is 11 wide, fg 3 wide → startX = (11-3)/2 = 4; startY = (7-2)/2 = 2
	if !strings.Contains(lines[2], "ABC") || !strings.Contains(lines[3], "DEF") {
		t.Fatalf("popup not placed on the expected rows:\n%s", got)
	}
	// background still present to the left and right of the popup row
	if !strings.HasPrefix(lines[2], "....") || !strings.HasSuffix(lines[2], "....") {
		t.Fatalf("background not preserved around popup:\n%q", lines[2])
	}
	// rows outside the popup are untouched
	if lines[0] != "..........." {
		t.Fatalf("non-popup row altered: %q", lines[0])
	}
	// every row keeps the background width
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 11 {
			t.Fatalf("row %d width = %d, want 11: %q", i, w, l)
		}
	}
}

// TestOverlayIsANSIWidthSafe: styled background keeps its visible width under a
// popup (escape codes are not counted as columns).
func TestOverlayIsANSIWidthSafe(t *testing.T) {
	styled := "\x1b[31m" + strings.Repeat("x", 20) + "\x1b[0m" // 20 visible cols, red
	bg := strings.Join([]string{styled, styled, styled}, "\n")
	fg := "POPUP"

	got := overlay(bg, fg)
	for i, l := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(l); w != 20 {
			t.Fatalf("row %d visible width = %d, want 20", i, w)
		}
	}
	if !strings.Contains(got, "POPUP") {
		t.Fatal("popup text missing from composite")
	}
}

// TestAbortPopupFloatsOverRunningScreen: the abort confirm renders as a bordered
// popup composited over the running screen — the background stays visible.
func TestAbortPopupFloatsOverRunningScreen(t *testing.T) {
	m := fixedModel()
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 30}) // tall bg so bg shows around the popup
	m.screen = screenRunning
	m.queue = []stepState{
		{step: steps.Step{Title: "updating the system", LogLabel: "u"}, status: steps.Running},
	}
	m.current = 0

	m = update(m, key("ctrl+c"))
	if m.screen != screenAbortConfirm {
		t.Fatalf("ctrl+c should open abort confirm, got screen=%d", m.screen)
	}

	view := m.View()
	if !strings.Contains(view, "Abort the running step?") {
		t.Fatal("popup text missing")
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╯") {
		t.Fatal("popup border missing (not rendered as a box)")
	}
	// background still visible around the floating popup
	if !strings.Contains(view, "archmaid") {
		t.Fatalf("background running screen not visible behind popup:\n%s", view)
	}
	if !strings.Contains(view, "updating the system") {
		t.Fatal("running step (background) not visible behind popup")
	}
}
