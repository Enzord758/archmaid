package ui

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
)

// styleCSS is archmaid's stylesheet; style.css says what it may do.
//
//go:embed style.css
var styleCSS string

// styleLightCSS is the light theme's state colours; loaded only while the
// theme is light (syncScheme).
//
//go:embed style_light.css
var styleLightCSS string

// scopeClass is the CSS class on archmaid's window and every one of its
// dialogs: every selector in style.css starts with it.
const scopeClass = "archmaid"

// styleErrors collects the stylesheet's parse errors (tests read them).
var (
	styleOnce   sync.Once
	styleMu     sync.Mutex
	styleErrors []string
)

// loadStyle adds style.css for the default display, once, at the
// APPLICATION priority: under the user's gtk.css, where DMS defines the
// named colours, so DMS colours every surface (3.1). Call it once GTK is
// up (the app's activate).
func loadStyle() {
	styleOnce.Do(func() {
		display := gdk.DisplayGetDefault()
		if display == nil {
			return
		}
		p := gtk.NewCSSProvider()
		p.ConnectParsingError(func(_ *gtk.CSSSection, err error) {
			styleMu.Lock()
			styleErrors = append(styleErrors, err.Error())
			styleMu.Unlock()
		})
		p.LoadFromString(styleCSS)
		gtk.StyleContextAddProviderForDisplay(display, p, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
		// The light/dark sets are providers swapped on the style manager's
		// dark flag. GTK resolves a @define-color inside @media
		// (prefers-color-scheme) when the sheet is parsed and does not redo
		// it when the scheme flips, so a live switch (a desktop's colour
		// scheme, AdwStyleManager's) left the light set in a dark window.
		sm := adw.StyleManagerGetDefault()
		sm.NotifyProperty("dark", syncScheme)
		syncScheme()
	})
}

// Priorities of the swapped providers, over style.css (APPLICATION) and
// under the user's gtk.css: the light state colours, glass.css, and the
// dark glass tokens over it.
const (
	lightPriority     = gtk.STYLE_PROVIDER_PRIORITY_APPLICATION + 1
	glassPriority     = gtk.STYLE_PROVIDER_PRIORITY_APPLICATION + 2
	glassDarkPriority = gtk.STYLE_PROVIDER_PRIORITY_APPLICATION + 3
)

var lightProvider, glassDarkProvider *gtk.CSSProvider

// setProvider adds the provider in *slot with css at priority, or removes
// it, so that it is on the display exactly when on is true. Parse errors go
// to styleErrors with name.
func setProvider(slot **gtk.CSSProvider, name, css string, priority uint, on bool) {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	if !on {
		if *slot != nil {
			gtk.StyleContextRemoveProviderForDisplay(display, *slot)
			*slot = nil
		}
		return
	}
	if *slot != nil {
		return
	}
	p := gtk.NewCSSProvider()
	p.ConnectParsingError(func(_ *gtk.CSSSection, err error) {
		styleMu.Lock()
		styleErrors = append(styleErrors, name+": "+err.Error())
		styleMu.Unlock()
	})
	p.LoadFromString(css)
	gtk.StyleContextAddProviderForDisplay(display, p, priority)
	*slot = p
}

// syncScheme puts the colour sets in line with the theme: the light state
// colours while it is light, the dark glass tokens while it is dark and the
// glass skin is on.
func syncScheme() {
	dark := adw.StyleManagerGetDefault().Dark()
	setProvider(&lightProvider, "style_light.css", styleLightCSS, lightPriority, !dark)
	setProvider(&glassDarkProvider, "glass_dark.css", glassDarkCSS, glassDarkPriority, dark && glassNow != glassOff)
}

// scoped gives a dialog the archmaid class: one that doesn't fit the
// window is shown as a window of its own, outside the main window's tree.
func scoped(d *adw.AlertDialog) *adw.AlertDialog {
	d.AddCSSClass(scopeClass)
	d.AddCSSClass("am-dialog") // style.css: the dialogs' look (P8 row 13)
	return d
}

// stateClasses are the am-state-* classes, one per runview.RowState plus
// waiting (polkit's dialog is up for the step).
var stateClasses = func() []string {
	var cs []string
	for s := runview.RowPending; s <= runview.RowNotRun; s++ {
		cs = append(cs, stateClass(s))
	}
	return append(cs, "am-state-waiting")
}()

// stateClass is the CSS class for a row state: am-state-<name>, spaces as
// dashes ("auth refused" is am-state-auth-refused).
func stateClass(s runview.RowState) string {
	return "am-state-" + strings.ReplaceAll(s.String(), " ", "-")
}

// setStateClass gives widget exactly one am-state-* class: the row's.
func setStateClass(widget *gtk.Widget, r runview.Row) {
	want := stateClass(r.State)
	if r.Waiting {
		want = "am-state-waiting"
	}
	for _, c := range stateClasses {
		if c != want {
			widget.RemoveCSSClass(c)
		}
	}
	widget.AddCSSClass(want)
}
