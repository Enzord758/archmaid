package ui

// The window's minimum height (#255): at every size the window can have,
// the content's minimum height fits it, so libadwaita's "AdwToastOverlay
// exceeds AdwApplicationWindow height" never logs, and a window asked for
// under the minimum gets the minimum.

import (
	"os"
	"strconv"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const (
	minFontEnv = "ARCHMAID_GUI_MIN_FONT"
	minDPIEnv  = "ARCHMAID_GUI_MIN_DPI"
)

func init() { covScenarios["cov-minheight"] = covMinHeight }

func covMinHeight(c *covEnv) {
	w := c.w
	c.quiet()
	// A larger font and text scale make the sidebar's rows taller (the VM
	// check, #258: it asked for 588 px at every window height).
	if f := os.Getenv(minFontEnv); f != "" {
		dpi, _ := strconv.Atoi(os.Getenv(minDPIEnv))
		set := gtk.SettingsGetDefault()
		set.SetObjectProperty("gtk-font-name", f)
		set.SetObjectProperty("gtk-xft-dpi", dpi*1024)
	}
	// The stub's banner is a demo's, 40 px the real window doesn't have.
	if w.banner != nil {
		w.banner.SetVisible(false)
	}
	var st []func()
	for _, wd := range []int{1500, 960, 630, 400} {
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
	child(t, "cov-minheight", minFontEnv+"=Noto Sans 13", minDPIEnv+"=144")
}
