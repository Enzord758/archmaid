package ui

import (
	_ "embed"
	"os"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

// The glass look (task #254, the owner's "Clear" skin): an opt-in second
// stylesheet, glass.css, over style.css. Off is the default look and
// nothing of it is loaded.
//
// What changes with the Settings page's "Glass look" switch, live:
//   - the provider holding glass.css is added to, or removed from, the
//     display (GTK restyles every widget at once);
//   - the window and every dialog carry am-glass (and am-glass-fb in the
//     fallback), which glass.css's selectors start with.
//
// Blur behind the window is the compositor's: GTK only supplies a
// translucent window. With no blur (settings.Settings.BlurBehind false)
// the same skin is near-opaque, the "fallback". A third piece is the
// content blur under an open dialog (below).

//go:embed glass.css
var glassCSS string

//go:embed glass_dark.css
var glassDarkCSS string

const (
	glassClass    = "am-glass"
	glassFBClass  = "am-glass-fb"
	contentClass  = "am-content"
	underClass    = "am-under-dialog"
	textSurfClass = "am-text-surface"
	solidClass    = "am-sheet-solid"
)

// glassMode is which skin is on.
type glassMode int

const (
	glassOff      glassMode = iota // the default look
	glassBlur                      // translucent: the compositor blurs behind
	glassFallback                  // the same skin, near-opaque
)

// glassModeFor is the skin s asks for, with getenv standing for the
// process's environment and advertised for the compositor's registry
// (settings.Settings.BlurBehind's Auto rule).
func glassModeFor(s settings.Settings, getenv func(string) string, advertised func() (has, known bool)) glassMode {
	switch {
	case !s.Glass:
		return glassOff
	case s.BlurBehind(getenv, advertised):
		return glassBlur
	}
	return glassFallback
}

// glassNow is the skin in force; every dialog made while it is on gets
// its classes (scoped, newWideDialog). Main thread only.
var glassNow = glassOff

// glassProvider is glass.css's provider while it is on the display. It
// sits one above the light state colours (glassPriority) and still under
// the user's gtk.css, where DMS defines the named colours the tokens in it
// are derived from.
var glassProvider *gtk.CSSProvider

// setGlassProvider loads glass.css for the default display, or removes it.
func setGlassProvider(on bool) {
	setProvider(&glassProvider, "glass.css", glassCSS, glassPriority, on)
}

// glassClasses puts widget's glass classes in line with mode.
func glassClasses(widget interface {
	AddCSSClass(string)
	RemoveCSSClass(string)
}, mode glassMode) {
	if mode == glassOff {
		widget.RemoveCSSClass(glassClass)
	} else {
		widget.AddCSSClass(glassClass)
	}
	if mode == glassFallback {
		widget.AddCSSClass(glassFBClass)
	} else {
		widget.RemoveCSSClass(glassFBClass)
	}
}

// applyGlass switches the skin to the one the settings ask for, live:
// the provider, the window's classes and any dialog that is open.
func (w *Window) applyGlass() {
	mode := glassModeFor(w.settings, os.Getenv, registryHas)
	glassNow = mode
	setGlassProvider(mode != glassOff)
	syncScheme() // the dark tokens go with the skin
	glassClasses(w.win, mode)
	for d := range w.openDialogs {
		glassClasses(d, mode)
	}
	w.syncSheets()
	if mode != glassBlur {
		w.syncContentBlur()
	}
}

// Content blur: while an AdwDialog is open, the page under it is blurred
// (am-under-dialog on am-content, glass.css: filter: blur(5px)). It is a
// GTK filter on the widget, a GPU pass of the whole page on every frame,
// so it is skipped on the software renderer (cairo, which archmaid-gui
// defaults to: DefaultRenderer) and in the fallback.

// rendererName is the name of the GSK renderer w draws with ("GskCairoRenderer",
// "GskNglRenderer", "GskVulkanRenderer"), "" when it has none yet. A
// variable so a test on the headless display can stand for a GPU one.
var rendererName = func(w *Window) string {
	r := w.win.Renderer()
	if r == nil {
		return ""
	}
	return glib.InternObject(r).TypeFromInstance().Name()
}

// contentBlurOK says whether the page may be blurred behind a dialog.
func contentBlurOK(mode glassMode, renderer string) bool {
	return mode == glassBlur && renderer != "" && renderer != "GskCairoRenderer"
}

// dialogOpened and dialogClosed keep the set of open dialogs and the
// content blur with it.
func (w *Window) dialogOpened(d *amDialog) {
	if w.openDialogs == nil {
		w.openDialogs = map[*amDialog]bool{}
	}
	if w.openDialogs[d] {
		return
	}
	w.openDialogs[d] = true
	glassClasses(d, glassNow)
	w.syncSheets()
	w.syncContentBlur()
	d.ConnectClosed(func() {
		delete(w.openDialogs, d)
		w.syncContentBlur()
	})
}

// syncSheets makes the open dialogs' sheets near-opaque (am-sheet-solid)
// unless the page under them is blurred: a translucent sheet over an
// unblurred page shows its text through (the compositor's blur is only
// of what is behind the window).
func (w *Window) syncSheets() {
	solid := !contentBlurOK(glassNow, rendererName(w))
	for d := range w.openDialogs {
		if solid {
			d.AddCSSClass(solidClass)
		} else {
			d.RemoveCSSClass(solidClass)
		}
	}
}

// syncContentBlur adds am-under-dialog to the content while any dialog is
// open and the blur is allowed, else removes it.
func (w *Window) syncContentBlur() {
	if w.closed || w.shell == nil {
		return
	}
	on := len(w.openDialogs) > 0 && contentBlurOK(glassNow, rendererName(w))
	if on {
		w.shell.split.AddCSSClass(underClass)
	} else {
		w.shell.split.RemoveCSSClass(underClass)
	}
}
