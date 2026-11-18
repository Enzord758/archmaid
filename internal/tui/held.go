package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Enzord758/archmaid/internal/aurhelper"
	"github.com/Enzord758/archmaid/internal/held"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The Held updates screen (#295): the AUR updates the timer's check holds
// back for the cooldown (updates-cache.json) and the packages an AUR audit
// holds (audit-state.json), as the GUI's Updates card lists them
// (internal/held). Read only. "Review and update" in the GUI is the Packages
// page's AUR install with the PKGBUILD review; the TUI never installs an AUR
// package, so for each held update it prints the command to run yourself.

// idHeld is the menu entry's ID (the TUI's own, like idHealth).
const idHeld = "held"

// Seams: tests give their own lists, helper and clock.
var (
	heldRead   = func() held.Set { return held.Read("") }
	heldHelper = func() string { return aurhelper.Detect(aurhelper.Deps{}).Name }
	heldNow    = time.Now
)

type heldState struct {
	set    held.Set
	helper string
	view   viewport.Model
}

// heldCount is what the menu says: the packages held now, the cooldown ones
// only while the cache is fresh (the GUI card's rule).
func heldCount(s held.Set, now time.Time) int {
	return len(s.CooldownAt(now)) + len(s.Audit)
}

// openHeld reads both files (small, bounded) and shows the screen.
func (m model) openHeld() (tea.Model, tea.Cmd) {
	m.held.set = heldRead()
	m.held.helper = heldHelper()
	m.screen = screenHeld
	m.heldLayout()
	m.held.view.GotoTop()
	return m, nil
}

func (m model) heldKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = screenMenu
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.held.view, cmd = m.held.view.Update(msg)
	return m, cmd
}

// heldBody is the screen's text.
func (m model) heldBody() string {
	w := m.healthWidth()
	wrap := func(st lipgloss.Style, indent int, s string) string {
		return st.Width(w).PaddingLeft(indent).Render(s)
	}
	set := m.held.set
	cool := set.CooldownAt(heldNow())
	var b strings.Builder
	b.WriteString(wrap(statusStyle, 0, "These AUR updates are held back on purpose. Updating one now skips that safety window, so review its PKGBUILD first.") + "\n")
	if len(cool) == 0 && len(set.Audit) == 0 {
		b.WriteString("\n" + stepOKStyle.Render("Nothing is held back now."))
		return b.String()
	}
	if len(cool) > 0 {
		b.WriteString("\n" + titleStyle.Render("Cooldown · newer than 72 h") + "\n")
		for _, p := range cool {
			b.WriteString(itemStyle.Render("  "+p.Name) + "  " + stepSkipStyle.Render(p.Tag()) + "\n")
			b.WriteString(wrap(statusStyle, 4, p.Detail()) + "\n")
		}
	}
	if len(set.Audit) > 0 {
		b.WriteString("\n" + titleStyle.Render("Held by the AUR audit") + "\n")
		for _, a := range set.Audit {
			b.WriteString(stepFailStyle.Render("  "+sanitize.Field(a.Pkg, 64)) + "\n")
			b.WriteString(wrap(statusStyle, 4, sanitize.Field(a.Reason, 160)) + "\n")
		}
		b.WriteString("\n" + wrap(statusStyle, 0, "Run \"Audit AUR packages\" from the menu and press a once you have checked them."))
		b.WriteString("\n")
	}
	if len(cool) > 0 {
		b.WriteString("\n" + titleStyle.Render("Review and update") + "\n")
		if m.held.helper == "" {
			b.WriteString(wrap(noteStyle, 0, "No AUR helper (paru or yay) was found, so there is nothing to run these with here.") + "\n")
		} else {
			b.WriteString(wrap(statusStyle, 0, "Read the PKGBUILD, then run it yourself in a terminal:") + "\n")
			for _, p := range cool {
				b.WriteString(itemStyle.Render(fmt.Sprintf("  %s -S %s", m.held.helper, p.Name)) + "\n")
			}
		}
		b.WriteString(wrap(statusStyle, 0, "The GUI's Packages page offers the PKGBUILD review and the update itself; the TUI never installs AUR packages."))
	}
	return b.String()
}

// heldLayout sizes the scrolling text to the terminal.
func (m *model) heldLayout() {
	if m.screen != screenHeld {
		return
	}
	h := 24
	if m.termHeight > 0 {
		h = m.termHeight
	}
	v := m.held.view
	v.Width, v.Height = m.healthWidth(), max(h-3-2-1, 3)
	v.SetContent(m.heldBody())
	m.held.view = v
}

func (m model) viewHeld() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	n := heldCount(m.held.set, heldNow())
	b.WriteString(titleStyle.Render("Held updates") + statusStyle.Render(fmt.Sprintf("  %d held", n)))
	b.WriteString("\n\n" + m.held.view.View() + "\n")
	b.WriteString(hintStyle.Render("↑/↓ scroll · esc back"))
	return b.String()
}
