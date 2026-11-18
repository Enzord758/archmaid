package ui

// The window's minimum height (#255): at every size the window can have,
// the content's minimum height fits it, so libadwaita's "AdwToastOverlay
// exceeds AdwApplicationWindow height" never logs, and a window asked for
// under the minimum gets the minimum.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	// Every page, since each can ask for its own height (#294), at a width
	// in the wide, the narrow and the narrowest forms.
	for _, pg := range navPages {
		for _, wd := range []int{1500, 960, 630, 400, 340} {
			for _, h := range []int{200, 340, 372, 481, 541, 700} {
				st = append(st,
					func() { w.stack.SetVisibleChildName(pg.name); w.win.SetDefaultSize(wd, h) },
					func() {},
					func() {
						got := w.win.Height()
						min, _, _, _ := w.toasts.Measure(gtk.OrientationVertical, w.win.Width())
						c.ok(got >= minWindowHeight, "%s asked for %dx%d, the window is %d high, under the minimum %d", pg.name, wd, h, got, minWindowHeight)
						c.ok(min <= got, "%s %dx%d: the content asks for %d px of height, the window has %d", pg.name, wd, got, min, got)
					})
			}
		}
	}
	c.seq(80, st...)
}

// TestGUIMinHeight: see covMinHeight. The child's stderr is the GTK log: a
// sweep that makes libadwaita log "AdwToastOverlay ... exceeds
// AdwApplicationWindow height" fails (#294), whatever the measured minimum
// says (that is taken once the layout has settled).
func TestGUIMinHeight(t *testing.T) {
	needDisplay(t)
	run := func(env ...string) {
		t.Helper()
		var log bytes.Buffer
		childCmd(t, "cov-minheight", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &log) }, env...)
		for _, l := range strings.Split(log.String(), "\n") {
			if strings.Contains(l, "exceeds") {
				t.Errorf("GTK logged during the resize sweep: %s", l)
			}
		}
	}
	run()
	run(minFontEnv+"=Noto Sans 13", minDPIEnv+"=144")
}
