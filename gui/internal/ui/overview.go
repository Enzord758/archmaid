package ui

import (
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/engine"
)

// The Overview page: the status header, the last run (last-run.json, read
// only) and the menu. Choosing an action sends Start and shows the Run
// page; View log shows the Logs page.

type menuRow struct {
	entry overview.Entry
	row   *adw.ActionRow
	spin  *adw.Spinner
}

func (w *Window) newOverview() gtk.Widgetter {
	page := adw.NewPreferencesPage()

	w.statLabel = gtk.NewLabel("")
	w.statLabel.AddCSSClass("dim-label")
	w.statLabel.SetVisible(false)
	head := adw.NewPreferencesGroup()
	head.Add(w.statLabel)
	page.Add(head)

	last := adw.NewPreferencesGroup()
	last.SetTitle("Last Run")
	w.lastRow = adw.NewActionRow()
	w.lastRow.SetUseMarkup(false)
	w.lastIcon = gtk.NewImage()
	w.lastRow.AddPrefix(w.lastIcon)
	last.Add(w.lastRow)
	page.Add(last)

	menu := adw.NewPreferencesGroup()
	menu.SetTitle("Actions")
	w.menu = map[string]*menuRow{}
	for _, e := range overview.Entries() {
		r := &menuRow{entry: e, row: adw.NewActionRow(), spin: adw.NewSpinner()}
		r.row.SetUseMarkup(false)
		r.row.SetTitle(e.Label)
		r.row.AddPrefix(gtk.NewImageFromIconName(e.Icon))
		r.spin.SetVisible(false)
		r.row.AddSuffix(r.spin)
		r.row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
		r.row.SetActivatable(true)
		r.row.ConnectActivated(func() { w.choose(e) })
		menu.Add(r.row)
		w.menu[e.ID] = r
	}
	page.Add(menu)
	w.refreshLastRun()
	return page
}

// choose is a menu row: start its action, or show the Logs page.
func (w *Window) choose(e overview.Entry) {
	switch e.Kind {
	case overview.KindLogs:
		w.showLogs()
	default:
		w.startAction(e.ID)
	}
}

// refreshLastRun rereads last-run.json (at start and after each run).
func (w *Window) refreshLastRun() {
	lr := overview.ReadLastRun(w.opts.StateDir, time.Now())
	w.lastRun = lr
	w.lastRow.SetTitle(lr.Title)
	w.lastRow.SetSubtitle(strings.Join(lr.Issues, "\n"))
	icon := "emblem-ok-symbolic"
	w.lastIcon.RemoveCSSClass("error")
	w.lastIcon.RemoveCSSClass("warning")
	switch lr.Level {
	case overview.LevelNone:
		icon = "content-loading-symbolic"
	case overview.LevelWarn:
		icon = "dialog-warning-symbolic"
		w.lastIcon.AddCSSClass("warning")
	case overview.LevelError:
		icon = "dialog-error-symbolic"
		w.lastIcon.AddCSSClass("error")
	}
	w.lastIcon.SetFromIconName(icon)
}

// overviewState is what the Overview's widgets show, so a flush that
// changes none of it (an output flood) costs a compare, not ~30 widget
// calls.
type overviewState struct {
	stat, current, busyText string
	busy                    bool
	phase                   engine.Phase
}

// updateOverview redraws the header and the menu rows, when what they show
// changed.
func (w *Window) updateOverview() {
	st := overviewState{overview.StatusLine(w.model.Stat.Status), w.current, w.model.Busy, w.busy, w.model.Phase}
	if w.shownOverview != nil && *w.shownOverview == st {
		return
	}
	w.shownOverview = &st
	line := st.stat
	w.statLabel.SetText(line)
	w.statLabel.SetVisible(line != "")
	for id, r := range w.menu {
		running := w.busy && id == w.current
		r.spin.SetVisible(running)
		sub := ""
		if running && w.model.Phase == engine.PhaseDiscovering {
			sub = w.model.Busy
			if sub == "" {
				sub = r.entry.Busy
			}
		} else if running {
			sub = "Running"
		}
		r.row.SetSubtitle(sub)
		// One action at a time; the Logs page stays reachable.
		r.row.SetSensitive(!w.busy || r.entry.Kind == overview.KindLogs)
	}
}
