package ui

// The window's minimum height (#255): at every size the window can have,
// the content's minimum height fits it, so libadwaita's "AdwToastOverlay
// exceeds AdwApplicationWindow height" never logs, and a window asked for
// under the minimum gets the minimum.

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func init() { covScenarios["cov-minheight"] = covMinHeight }

func covMinHeight(c *covEnv) {
	w := c.w
	c.quiet()
	var st []func()
	for _, wd := range []int{960, 630, 400} {
		for _, h := range []int{200, 340, 372, 481, 500, 508, 539, 541, 700} {
			st = append(st,
				func() { w.win.SetDefaultSize(wd, h) },
				func() {},
				func() {
					got := w.win.Height()
					min, _, _, _ := w.toasts.Measure(gtk.OrientationVertical, -1)
					c.ok(got >= minWindowHeight, "asked for %dx%d, the window is %d high, under the minimum %d", wd, h, got, minWindowHeight)
					c.ok(min <= got, "%dx%d: the content asks for %d px of height, the window has %d", wd, got, min, got)
				})
		}
	}
	c.seq(200, st...)
}

// TestGUIMinHeight: see covMinHeight.
func TestGUIMinHeight(t *testing.T) {
	needDisplay(t)
	child(t, "cov-minheight")
}
