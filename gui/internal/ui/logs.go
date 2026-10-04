package ui

import (
	"errors"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// The Logs page: the run history (one expander per run directory, its
// category logs as rows) and the chosen log in a ListView. It only reads:
// nothing here deletes or rotates (that is a real run's business).

type logsPage struct {
	runs    *gtk.ListBox
	text    *gtk.StringList
	textLen uint
	title   *gtk.Label
	// shown is "run/category" of the log in the view, for the harness.
	shown string
	// rows are the category rows by "run/category", for the harness.
	rows map[string]*adw.ActionRow
}

func (w *Window) newLogs() gtk.Widgetter {
	p := &logsPage{rows: map[string]*adw.ActionRow{}}
	w.logs = p
	p.runs = gtk.NewListBox()
	p.runs.AddCSSClass("boxed-list")
	p.runs.SetSelectionMode(gtk.SelectionNone)
	p.runs.SetMarginTop(12)
	p.runs.SetMarginBottom(12)
	p.runs.SetMarginStart(12)
	p.runs.SetMarginEnd(12)
	left := gtk.NewScrolledWindow()
	left.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	left.SetChild(p.runs)
	left.SetSizeRequest(320, -1)

	p.text = gtk.NewStringList(nil)
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		l := gtk.NewLabel("")
		l.SetXAlign(0)
		l.SetSelectable(true)
		obj.Cast().(*gtk.ListItem).SetChild(l)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		item.Child().(*gtk.Label).SetText(item.Item().Cast().(*gtk.StringObject).String())
	})
	view := gtk.NewListView(gtk.NewNoSelection(p.text), &factory.ListItemFactory)
	view.AddCSSClass("monospace")
	right := gtk.NewScrolledWindow()
	right.SetChild(view)
	right.SetVExpand(true)
	right.SetHExpand(true)
	p.title = gtk.NewLabel("Pick a log")
	p.title.SetXAlign(0)
	p.title.AddCSSClass("heading")
	p.title.SetMarginStart(12)
	p.title.SetMarginTop(6)
	p.title.SetMarginBottom(6)
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.Append(p.title)
	box.Append(right)

	paned := gtk.NewPaned(gtk.OrientationHorizontal)
	paned.SetStartChild(left)
	paned.SetEndChild(box)
	paned.SetPosition(340)
	paned.SetShrinkStartChild(false)
	return paned
}

// showLogs shows the Logs page, rescanned.
func (w *Window) showLogs() {
	w.refreshLogs()
	w.stack.SetVisibleChildName("logs")
}

// refreshLogs rescans the run history (when the page is shown, and after a
// run).
func (w *Window) refreshLogs() {
	p := w.logs
	p.runs.RemoveAll()
	clear(p.rows)
	runs, err := logview.Runs(w.opts.StateDir)
	if err != nil {
		p.runs.Append(placeholder("The logs can't be read", err.Error()))
		return
	}
	if len(runs) == 0 {
		p.runs.Append(placeholder("No logs yet", "Each run writes its logs here."))
		return
	}
	for i, r := range runs {
		exp := adw.NewExpanderRow()
		exp.SetTitle(r.Title())
		exp.SetSubtitle(r.Subtitle())
		exp.SetExpanded(i == 0)
		for _, c := range r.Categories {
			row := adw.NewActionRow()
			row.SetUseMarkup(false)
			row.SetTitle(c.Label())
			row.SetActivatable(true)
			row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
			row.ConnectActivated(func() { w.openLog(r, c) })
			exp.AddRow(row)
			p.rows[r.Name+"/"+string(c)] = row
		}
		p.runs.Append(exp)
	}
}

func placeholder(title, sub string) gtk.Widgetter {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(title)
	row.SetSubtitle(sub)
	return row
}

// openLog shows one run's category log.
func (w *Window) openLog(r logview.Run, c logstore.Category) {
	p := w.logs
	lines, err := r.Read(c)
	switch {
	case errors.Is(err, logstore.ErrEmpty):
		lines = []string{"(empty)"}
	case err != nil:
		lines = []string{"Can't read this log: " + err.Error()}
	}
	p.text.Splice(0, p.textLen, lines)
	p.textLen = uint(len(lines))
	p.title.SetText(r.Title() + " · " + c.Label())
	p.shown = r.Name + "/" + string(c)
}
