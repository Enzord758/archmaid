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
	})
}

// scoped gives a dialog the archmaid class: one that doesn't fit the
// window is shown as a window of its own, outside the main window's tree.
func scoped(d *adw.AlertDialog) *adw.AlertDialog {
	d.AddCSSClass(scopeClass)
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
