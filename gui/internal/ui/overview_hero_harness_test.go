package ui

// The Full Cleanup hero lines up with the blocks below it (#261): its
// outer left and right edges are the cells' and the Last run / Updates
// cards' outer edges, in both looks, wide and narrow. In the default look
// the blocks are drawn inside a 1 px grid border, whose outer edge is the
// edge (the hero's own border is on it); in glass the cells and cards are
// drawn on their own edges.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

func init() { covScenarios["cov-overview-hero"] = covOverviewHero }

func covOverviewHero(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.5 }
	var hero gtk.Widgetter
	eachWidget(w.win, func(wd gtk.Widgetter) {
		if b := gtk.BaseWidget(wd); hero == nil && b.HasCSSClass("am-hero") {
			hero = wd
		}
	})
	c.ok(hero != nil, "no hero")
	var st []func()
	for _, look := range []bool{false, true} {
		for _, wd := range []int{1920, 1440, 1280, 900, 700, 500} {
			st = append(st,
				func() { w.win.SetDefaultSize(wd, 900); w.set.glass.SetActive(look) },
				func() {},
				func() {},
				func() {
					where := fmt.Sprintf("glass %v at %d px (narrow %v)", look, wd, w.narrow)
					hx, _, hr, _, ok := alignBounds(w, hero)
					c.ok(ok, "%s: no hero bounds", where)
					// What the hero must match: the cells' and the top
					// cards' outer edges (the grids' outer edges, default).
					var cells, top [2]float64
					if look {
						cells = [2]float64{1e9, -1e9}
						for i := 0; w.cells.ChildAtIndex(i) != nil; i++ {
							x, _, r, _, _ := alignBounds(w, w.cells.ChildAtIndex(i).Child())
							cells = [2]float64{math.Min(cells[0], x), math.Max(cells[1], r)}
						}
						lx, _, lr, _, _ := alignBounds(w, w.topLast)
						ux, _, ur, _, _ := alignBounds(w, w.topUpd)
						top = [2]float64{math.Min(lx, ux), math.Max(lr, ur)}
					} else {
						cx, _, cr, _, _ := alignBounds(w, w.cells)
						cells = [2]float64{cx, cr}
						box := gtk.Widgetter(w.top)
						if !w.topWide {
							box = w.topCol
						}
						tx, _, tr, _, _ := alignBounds(w, box)
						top = [2]float64{tx, tr}
					}
					c.ok(near(hx, cells[0]) && near(hr, cells[1]), "%s: the hero spans %.1f..%.1f, the cells %.1f..%.1f", where, hx, hr, cells[0], cells[1])
					c.ok(near(hx, top[0]) && near(hr, top[1]), "%s: the hero spans %.1f..%.1f, Last run and Updates %.1f..%.1f", where, hx, hr, top[0], top[1])
					shoot(w, "hero", fmt.Sprintf("glass-%v-%s-%d", look, os.Getenv(glassSchemeEnv), wd))
				})
		}
	}
	c.seq(150, st...)
}

// TestGUIOverviewHero: see covOverviewHero.
func TestGUIOverviewHero(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"dark", "light"} {
		child(t, "cov-overview-hero", "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"="+s)
	}
}
