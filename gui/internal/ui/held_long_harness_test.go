package ui

// Held updates with long versions (#303): a row's second line, "1.323.0_40840-1
// → 1.327.0_43047-1 · free in 21 h", was cut with an ellipsis in the sheet's
// width, so the "free in" part was lost. Neither the versions nor the time
// may be cut: the line wraps.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const heldLongJSON = `[
 {"name":"curseforge","from":"1.323.0_40840-1","to":"1.327.0_43047-1","src":"aur","held":true,"age":"2d3h"},
 {"name":"some-package-with-a-very-long-name-bin","from":"20261119.r123.gabcdef0123-1","to":"20261120.r456.g0123456789abcdef-2","src":"aur","held":true,"age":"1d9h"}]`

func init() {
	covScenarios["cov-held-long"] = covHeldLong
	covOptions["cov-held-long"] = func(o *Options, _ *report) {
		dir, err := os.MkdirTemp("", "archmaid-held-")
		if err != nil {
			panic(err)
		}
		heldEnv = &heldEnvT{dir: dir}
		heldEnv.writeCache(2, heldLongJSON, time.Now())
		o.StateDir = dir
		o.RefreshUpdates = heldEnv.refresh
	}
}

// covHeldLong opens the sheet at the narrowest window the sheet is checked at
// (960 px) and reads every detail line: whole, not ellipsized, and with its
// "free in" part.
func covHeldLong(c *covEnv) {
	w := c.w
	c.quiet()
	c.seq(300,
		func() { w.win.SetDefaultSize(960, 700) },
		func() { w.upd.held.Emit("clicked") },
		func() {
			c.ok(w.heldSheet != nil, "no sheet")
			n := 0
			eachWidget(&w.heldSheet.Widget, func(wd gtk.Widgetter) {
				l, ok := wd.(*gtk.Label)
				if !ok || !l.HasCSSClass("am-health-detail") {
					return
				}
				n++
				c.ok(!l.Layout().IsEllipsized(), "the line %q is cut with an ellipsis", l.Text())
				c.ok(strings.Contains(l.Text(), " · free in "), "the line %q has no \"free in\" part", l.Text())
			})
			c.ok(n == 2, "found %d detail lines, want 2", n)
			c.press(w.heldSheet, "close")
		},
	)
}

// TestGUIHeldLong: see covHeldLong.
func TestGUIHeldLong(t *testing.T) {
	needDisplay(t)
	child(t, "cov-held-long", "XDG_CONFIG_HOME="+t.TempDir())
}
