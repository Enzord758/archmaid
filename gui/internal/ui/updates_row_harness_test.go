package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The Updates card's place on the Overview (task #253): where the cell grid
// below has three columns, Last run spans the first two and the card is the
// third, on Last run's row, with the third cell's x and width; where the
// grid has fewer, the card sits under Last run at full width.

func init() {
	covOptions["cov-updates-row"] = func(o *Options, _ *report) {
		updEnv = newUpdFake("write")
		updEnv.writeCache(3, time.Now().Add(-20*time.Minute))
		o.StateDir = updEnv.dir
		o.RefreshUpdates = updEnv.refresh
	}
	covScenarios["cov-updates-row"] = covUpdatesRow
}

func TestGUICovUpdatesRow(t *testing.T) { covChild(t, "cov-updates-row") }

// rowWidths: from the screenshot sizes down to the narrow layout.
var rowWidths = []int{1280, 1278, 1276, 1274, 1272, 1270, 1268, 1266, 1264, 1262, 1260, 1080, 960, 800, 600, 380}

// envRowWidths ("1500,1400") replaces the list, for a bigger screen
// (HEADLESS_WIDTH) or screenshots.
const envRowWidths = "ARCHMAID_GUI_ROW_WIDTHS"

func covUpdatesRow(c *covEnv) {
	w := c.w
	c.quiet()
	root := func() gtk.Widgetter { return w.stack.VisibleChild() }
	bounds := func(x gtk.Widgetter) (float32, float32, float32, float32) {
		r, ok := gtk.BaseWidget(x).ComputeBounds(root())
		c.ok(ok, "no bounds")
		return r.X(), r.Y(), r.Width(), r.Height()
	}
	widths := rowWidths
	if v := os.Getenv(envRowWidths); v != "" {
		widths = nil
		for _, f := range strings.Split(v, ",") {
			n, _ := strconv.Atoi(f)
			widths = append(widths, n)
		}
	}
	var steps []func() bool
	steps = append(steps, func() bool { w.pickPage("overview"); return true })
	threeSeen, stackedSeen := 0, 0
	narrowest3 := 0
	for _, wd := range widths {
		steps = append(steps, func() bool { w.win.SetDefaultSize(wd, 800); return true })
		steps = append(steps, func() bool {
			if w.win.Width() != wd || w.topWide != w.cellsThreeWide() {
				return false
			}
			ux, uy, uw, uh := bounds(w.topUpd)
			lx, ly, lw, lh := bounds(w.topLast)
			cx, cy, cw, _ := bounds(w.cells.ChildAtIndex(2))
			_, _, _, _ = lx, lw, cy, uh
			sx, sy, sw, _ := bounds(w.cells.ChildAtIndex(0))
			_, _ = sx, sy
			if w.topWide {
				threeSeen++
				narrowest3 = wd
				c.ok(absf32(ux-cx) <= 1 && absf32(ux+uw-(cx+cw)) <= 1, "%d: Updates x=%.1f w=%.1f, third cell x=%.1f w=%.1f", wd, ux, uw, cx, cw)
				c.ok(absf32(uy-ly) <= 1 && absf32(uh-lh) <= 1, "%d: Updates y=%.1f h=%.1f, Last run y=%.1f h=%.1f", wd, uy, uh, ly, lh)
				c.ok(absf32(lx-sx) <= 1 && absf32(lx+lw+1-ux) <= 1.5, "%d: Last run x=%.1f w=%.1f, Updates x=%.1f", wd, lx, lw, ux)
				c.ok(absf32(lw-(sw*2+1)) <= 1.5, "%d: Last run is %.1f wide, two cells are %.1f", wd, lw, sw*2+1)
			} else {
				stackedSeen++
				c.ok(uy >= ly+lh && absf32(ux-lx) <= 1 && absf32(uw-lw) <= 1, "%d: stacked: Updates x=%.1f y=%.1f w=%.1f, Last run x=%.1f y=%.1f w=%.1f h=%.1f", wd, ux, uy, uw, lx, ly, lw, lh)
			}
			c.ok(w.win.Width() == wd, "window %d", w.win.Width())
			// The buttons fit the card: both inside it, whole.
			for _, b := range []*gtk.Button{w.upd.check, w.upd.topg} {
				bx, _, bw, _ := bounds(b)
				c.ok(bx >= ux-1 && bx+bw <= ux+uw+1, "%d: button %q at x=%.1f w=%.1f leaves the card x=%.1f w=%.1f", wd, b.Label(), bx, bw, ux, uw)
			}
			minW, _, _, _ := gtk.BaseWidget(root()).Measure(gtk.OrientationHorizontal, -1)
			c.ok(minW <= wd, "%d: the Overview needs %d", wd, minW)
			fmt.Fprintf(os.Stderr, "ROW width=%d three=%v card x=%.0f w=%.0f cell3 x=%.0f w=%.0f\n", wd, w.topWide, ux, uw, cx, cw)
			if w.topWide || wd == 1080 || wd == 380 {
				shoot(w, c.rep.Scenario, fmt.Sprintf("w%d", wd))
			}
			return true
		})
	}
	steps = append(steps, func() bool {
		c.ok(threeSeen >= 1 && stackedSeen >= 1, "three-column widths %d, stacked %d", threeSeen, stackedSeen)
		fmt.Fprintf(os.Stderr, "ROW narrowest three-column width in the list: %d\n", narrowest3)
		_ = os.RemoveAll(updEnv.dir)
		return true
	})
	c.waitSeq(60, steps...)
}

func absf32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
