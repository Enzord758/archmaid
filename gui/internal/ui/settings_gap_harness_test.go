package ui

// The Settings cards (#301): two to a line, the cards beside a taller one
// were drawn as tall as it, with a large empty gap under their last widget
// (the flow box is homogeneous, which makes its children one size). Each
// card now takes the height its own content asks for.

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func init() { covScenarios["cov-settings-gap"] = covSettingsGap }

func covSettingsGap(c *covEnv) {
	w := c.w
	c.quiet()
	if w.banner != nil {
		w.banner.SetVisible(false)
	}
	var cards []*gtk.Widget
	var walk func(wd gtk.Widgetter)
	walk = func(wd gtk.Widgetter) {
		b := gtk.BaseWidget(wd)
		if b.HasCSSClass("card") {
			cards = append(cards, b)
			return
		}
		for ch := b.FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
			walk(ch)
		}
	}
	// gaps checks the card below each card in its column: the row spacing
	// (18 px) and nothing more. A homogeneous flow box made every row as
	// tall as the tallest card, so a short card was followed by a big gap.
	gaps := func(where string) {
		for _, a := range cards {
			ax, ay, _ := a.TranslateCoordinates(w.set.scroll, 0, 0)
			for _, b := range cards {
				bx, by, _ := b.TranslateCoordinates(w.set.scroll, 0, 0)
				if b != a && bx > ax-5 && bx < ax+5 && by > ay {
					gap := by - (ay + float64(a.Height()))
					c.ok(gap <= 20, "%s: %.0f px between a card and the next one in its column, want the 18 px row spacing", where, gap)
					break
				}
			}
		}
	}
	c.seq(250,
		func() { w.stack.SetVisibleChildName("settings"); w.win.SetDefaultSize(1500, 900) },
		func() {},
		func() { cards = nil; walk(w.set.scroll); gaps("two columns") },
		func() { w.win.SetDefaultSize(700, 900) },
		func() {},
		func() { cards = nil; walk(w.set.scroll); gaps("one column") },
		func() { w.win.SetDefaultSize(1500, 900) },
		func() {},
		func() {
			cards = nil
			walk(w.set.scroll)
			c.ok(len(cards) >= 4, "found %d cards on the Settings page, want at least 4", len(cards))
			heights := map[int]bool{}
			for _, cd := range cards {
				_, nat, _, _ := cd.Measure(gtk.OrientationVertical, cd.Width())
				heights[cd.Height()] = true
				c.ok(cd.Height() <= nat+1, "a card (%d px wide) is %d px high, its content asks for %d: an empty gap under it", cd.Width(), cd.Height(), nat)
			}
			c.ok(len(heights) > 1, "every card is %v high: the cards no longer differ, so the check proves nothing", heights)
		})
}

// TestGUISettingsCardGap: see covSettingsGap.
func TestGUISettingsCardGap(t *testing.T) {
	needDisplay(t)
	home, cfg, state := t.TempDir(), t.TempDir(), t.TempDir()
	child(t, "cov-settings-gap", "HOME="+home, "XDG_CONFIG_HOME="+cfg, "ARCHMAID_GUI_STATE="+state)
}
