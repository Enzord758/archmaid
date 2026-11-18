package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Enzord758/archmaid/internal/held"
)

var heldTestNow = time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC)

func heldFixture() held.Set {
	return held.Set{
		Checked: heldTestNow.Add(-5 * time.Minute),
		Cooldown: []held.Pkg{
			{Name: "spotify", From: "1.2.74-1", To: "1.2.76-1", Age: "2d4h", AgeKnown: true, Since: 52 * time.Hour},
			{Name: "noage", From: "1-1", To: "2-1"},
		},
		Audit: []held.AuditHold{{Pkg: "dux-search-bin", Reason: "new co-maintainer · not accepted yet"}},
	}
}

// heldSeams gives a test its own lists, helper and clock.
func heldSeams(t *testing.T, s held.Set, helper string) {
	t.Helper()
	or, oh, on := heldRead, heldHelper, heldNow
	heldRead = func() held.Set { return s }
	heldHelper = func() string { return helper }
	heldNow = func() time.Time { return heldTestNow }
	t.Cleanup(func() { heldRead, heldHelper, heldNow = or, oh, on })
}

func openHeldModel(t *testing.T, s held.Set, helper string) model {
	t.Helper()
	heldSeams(t, s, helper)
	m := sizedModel()
	next, _ := m.openHeld()
	return next.(model)
}

func TestHeldGolden(t *testing.T) {
	viewGolden(t, openHeldModel(t, heldFixture(), "paru"))
}

func TestHeldNothingGolden(t *testing.T) {
	viewGolden(t, openHeldModel(t, held.Set{}, "paru"))
}

// TestHeldPrintsTheCommandNeverRunsIt: each held update gets the helper's
// command to run yourself, the GUI's review is named, and nothing starts.
func TestHeldPrintsTheCommandNeverRunsIt(t *testing.T) {
	m := openHeldModel(t, heldFixture(), "paru")
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 60})
	v := strings.Join(strings.Fields(m.View()), " ") // wrapped lines read as one
	for _, want := range []string{"paru -S spotify", "paru -S noage", "Packages page", "never installs AUR packages",
		"1.2.74-1 → 1.2.76-1 · free in 20 h", "held 2d4h", "dux-search-bin", "3 held"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
	if strings.Contains(v, "paru -S dux-search-bin") {
		t.Error("an audit-held package got an install command: the audit's accept is the way")
	}
	for _, k := range []string{"enter", "y", "a", "i"} {
		if _, cmd := m.Update(key(k)); cmd != nil {
			t.Errorf("key %q started something", k)
		}
	}
}

func TestHeldNoHelper(t *testing.T) {
	m := openHeldModel(t, heldFixture(), "")
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 60})
	v := m.View()
	if !strings.Contains(v, "No AUR helper") || strings.Contains(v, "-S spotify") {
		t.Fatalf("no helper:\n%s", v)
	}
}

// TestHeldStaleCacheHoldsNothing: an old cache says nothing about what is
// held now, so only the audit's packages are listed.
func TestHeldStaleCacheHoldsNothing(t *testing.T) {
	s := heldFixture()
	s.Checked = heldTestNow.Add(-3 * 24 * time.Hour)
	m := openHeldModel(t, s, "paru")
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 60})
	v := m.View()
	if strings.Contains(v, "spotify") || !strings.Contains(v, "dux-search-bin") || !strings.Contains(v, "1 held") {
		t.Fatalf("stale cache:\n%s", v)
	}
}

func TestHeldAuditOnlyHasNoUpdateCommands(t *testing.T) {
	s := held.Set{Audit: heldFixture().Audit}
	m := openHeldModel(t, s, "paru")
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 60})
	if v := m.View(); strings.Contains(v, "Review and update") || !strings.Contains(v, "Audit AUR packages") {
		t.Fatalf("audit only:\n%s", v)
	}
}

func TestHeldKeysAndFit(t *testing.T) {
	m := openHeldModel(t, heldFixture(), "paru")
	m = update(m, tea.WindowSizeMsg{Width: 50, Height: 12})
	if h := lipgloss.Height(m.View()); h > 12 {
		t.Fatalf("frame is %d lines in 12", h)
	}
	m = update(m, key("down"))
	m = update(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if _, cmd := m.Update(key("ctrl+c")); cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	if got := update(m, key("esc")); got.screen != screenMenu {
		t.Fatalf("esc: screen %d", got.screen)
	}
	if got := update(m, key("q")); got.screen != screenMenu {
		t.Fatalf("q: screen %d", got.screen)
	}
}

func TestHeldHostileTextIsCleaned(t *testing.T) {
	s := held.Set{Audit: []held.AuditHold{{Pkg: "evil\x1b[2J", Reason: "bad \x1b]0;x\x07 text"}}}
	m := openHeldModel(t, s, "paru")
	if strings.ContainsRune(m.View(), 0x1b) {
		t.Fatalf("an escape got through: %q", m.View())
	}
}

func TestHeldMenuEntryOpens(t *testing.T) {
	heldSeams(t, heldFixture(), "yay")
	m := sizedModel()
	m.cursor = len(menu()) - 4
	if menu()[m.cursor].ID != idHeld {
		t.Fatalf("menu index drifted: %q", menu()[m.cursor].ID)
	}
	m = update(m, key("enter"))
	if m.screen != screenHeld || m.held.helper != "yay" {
		t.Fatalf("screen %d helper %q", m.screen, m.held.helper)
	}
}

func TestHeldCount(t *testing.T) {
	if n := heldCount(heldFixture(), heldTestNow); n != 3 {
		t.Errorf("count %d, want 3", n)
	}
	if n := heldCount(held.Set{}, heldTestNow); n != 0 {
		t.Errorf("empty: %d", n)
	}
}
