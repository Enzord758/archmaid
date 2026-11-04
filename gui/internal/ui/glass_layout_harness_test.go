package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func init() { covScenarios["cov-glass-layout"] = covGlassLayout }

// covGlassLayout: the glass skin keeps the default look's layout. The
// Overview's cells, hero and Last run / Updates row ask for the same widths
// and heights, and at every window width the Updates card sits beside Last
// run exactly when it does in the default look (#258: glass padding and
// margins had moved the three-column breakpoint).
func covGlassLayout(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	type look struct {
		cellsMin, heroMin, topMin, cellsNat, cellNat [2]int
		topWide                                      bool
	}
	measure := func() look {
		var l look
		l.cellsMin[0], _, _, _ = w.cells.Measure(gtk.OrientationHorizontal, -1)
		l.cellsMin[1], _, _, _ = w.cells.Measure(gtk.OrientationVertical, 1200)
		_, l.cellsNat[0], _, _ = w.cells.Measure(gtk.OrientationHorizontal, -1)
		if c0 := w.cells.ChildAtIndex(0); c0 != nil {
			m, n, _, _ := gtk.BaseWidget(c0).Measure(gtk.OrientationHorizontal, -1)
			l.cellNat = [2]int{m, n}
		}
		l.topMin[0], _, _, _ = w.top.Measure(gtk.OrientationHorizontal, -1)
		l.topMin[1], _, _, _ = w.top.Measure(gtk.OrientationVertical, 1200)
		eachWidget(w.win, func(wd gtk.Widgetter) {
			if b := gtk.BaseWidget(wd); b.HasCSSClass("am-hero") && l.heroMin == [2]int{} {
				l.heroMin[0], _, _, _ = b.Measure(gtk.OrientationHorizontal, -1)
				l.heroMin[1], _, _, _ = b.Measure(gtk.OrientationVertical, 1200)
			}
		})
		l.topWide = w.topWide
		return l
	}
	var st []func()
	for wd := 1000; wd <= 1500; wd += 50 {
		var off, on look
		st = append(st,
			func() { w.win.SetDefaultSize(wd, 800); w.set.glass.SetActive(false) },
			func() {},
			func() { off = measure(); w.set.glass.SetActive(true) },
			func() {},
			func() {
				on = measure()
				c.ok(off == on, "%d px: the default look lays out %+v, glass %+v", wd, off, on)
			})
	}
	c.seq(150, st...)
}

// TestGUIGlassLayout: see covGlassLayout.
func TestGUIGlassLayout(t *testing.T) {
	needDisplay(t)
	child(t, "cov-glass-layout", "XDG_CONFIG_HOME="+t.TempDir(), glassSchemeEnv+"=light")
}
