package ui

// The Overview's top row lines up with the cell grid below it (#260, the
// owner's "the buttons are not aligned at all"): the Last run and Updates
// cards' outer edges and the split between them are on the cells' edges and
// column gap, each card's button starts where its text starts, and the two
// cards' buttons are on one row. In the default look and in glass, at
// three-column widths.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

func init() { covScenarios["cov-overview-align"] = covOverviewAlign }

// alignBounds is wd's bounds in the window's coordinates.
func alignBounds(w *Window, wd gtk.Widgetter) (x, y, r, b float64, ok bool) {
	rect, has := gtk.BaseWidget(wd).ComputeBounds(w.win)
	if !has {
		return 0, 0, 0, 0, false
	}
	x, y = float64(rect.X()), float64(rect.Y())
	return x, y, x + float64(rect.Width()), y + float64(rect.Height()), true
}

func covOverviewAlign(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.5 }
	// The Last run's files and its button, which a run with logs shows.
	files, open := w.lastRunExtras()
	files.SetText("topgrade.log · cleanup.log")
	files.SetVisible(true)
	open.SetVisible(true)
	var st []func()
	for _, look := range []bool{false, true} {
		for _, wd := range []int{1280, 1440, 1920} {
			st = append(st,
				func() { w.win.SetDefaultSize(wd, 900); w.set.glass.SetActive(look) },
				func() {},
				func() {},
				func() {
					where := fmt.Sprintf("glass %v at %d px", look, wd)
					shoot(w, "align", fmt.Sprintf("glass-%v-%s-%d", look, os.Getenv(glassSchemeEnv), wd))
					c.ok(w.topWide, "%s: the Updates card is not beside Last run", where)
					cell := func(i int) (x, y, r, b float64) {
						x, y, r, b, _ = alignBounds(w, w.cells.ChildAtIndex(i).Child())
						return
					}
					lx, ly, lr, lb, ok1 := alignBounds(w, w.topLast)
					ux, uy, ur, ub, ok2 := alignBounds(w, w.topUpd)
					c.ok(ok1 && ok2, "%s: no card bounds", where)
					c0x, _, _, _ := cell(0)
					_, _, c1r, _ := cell(1)
					c2x, _, c2r, _ := cell(2)
					c.ok(near(lx, c0x), "%s: Last run's left edge %.1f, the cells' %.1f", where, lx, c0x)
					c.ok(near(lr, c1r), "%s: Last run's right edge %.1f, the second column's %.1f", where, lr, c1r)
					c.ok(near(ux, c2x), "%s: Updates' left edge %.1f, the third column's %.1f", where, ux, c2x)
					c.ok(near(ur, c2r), "%s: Updates' right edge %.1f, the cells' %.1f", where, ur, c2r)
					// The gap between the two cards is the gap between the cells, and
					// sits over the second column gap.
					c1x, _, _, _ := cell(1)
					_, _, c0r, _ := cell(0)
					c.ok(near(ux-lr, c2x-c1r) && near(ux-lr, c1x-c0r), "%s: the cards' gap is %.1f px, the cells' %.1f and %.1f", where, ux-lr, c1x-c0r, c2x-c1r)
					c.ok(near(ly, uy) && near(lb, ub), "%s: the cards' rows differ: %.1f..%.1f and %.1f..%.1f", where, ly, lb, uy, ub)
					// Each button starts where its card's text starts.
					kl := gtk.BaseWidget(w.topLast).FirstChild()
					ku := gtk.BaseWidget(w.topUpd).FirstChild()
					klx, _, _, _, _ := alignBounds(w, kl)
					kux, _, _, _, _ := alignBounds(w, ku)
					ox, oy, _, ob, _ := alignBounds(w, open)
					cx, cy, _, cb, _ := alignBounds(w, w.upd.check)
					c.ok(near(ox, klx), "%s: Open in Logs starts at %.1f, its card's text at %.1f", where, ox, klx)
					c.ok(near(cx, kux), "%s: Check for updates starts at %.1f, its card's text at %.1f", where, cx, kux)
					c.ok(near(oy, cy) && near(ob, cb), "%s: the buttons' rows differ: %.1f..%.1f and %.1f..%.1f", where, oy, ob, cy, cb)
					c.ok(open.HasCSSClass("flat") == w.upd.check.HasCSSClass("flat"), "%s: the two cards' buttons have different styles", where)
				})
		}
	}
	c.seq(150, st...)
}

// TestGUIOverviewAlign: see covOverviewAlign.
func TestGUIOverviewAlign(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"dark", "light"} {
		child(t, "cov-overview-align", "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"="+scheme)
	}
}
