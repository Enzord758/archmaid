package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// The Overview page (P8 row 10; spec 1.2): the Full Cleanup hero (its
// plan, Run and Dry run first), the last run (last-run.json, read only),
// and the other actions as cells. A cell starts its action and shows the
// Run page. View log is gone: the Logs page covers it.

// menuRow is one action on the Overview: its entry, the button that
// starts it (the hero's Run for Full Cleanup), the line under it that
// says it runs, and its spinner.
type menuRow struct {
	entry overview.Entry
	cell  *gtk.Button
	sub   *gtk.Label
	spin  *adw.Spinner
}

// start activates the action as a click on its cell does.
func (r *menuRow) start() { r.cell.Emit("clicked") }

func (w *Window) newOverview() gtk.Widgetter {
	w.menu = map[string]*menuRow{}
	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.AddCSSClass("am-overview")
	box.Append(w.newHero())
	// The last run spans the width under the hero, outside the grid: in it,
	// a seventh cell left an empty slot at the end of the last line, drawn
	// in the grid's hairline grey (Dev 5's VM round r8). The grid holds the
	// six actions, and at most three to a line divides them evenly.
	last := gtk.NewBox(gtk.OrientationVertical, 0)
	last.AddCSSClass("am-grid")
	last.Append(w.newLastRun())
	box.Append(last)

	w.cells = gtk.NewFlowBox()
	w.cells.SetSelectionMode(gtk.SelectionNone)
	w.cells.SetHomogeneous(true)
	w.cells.SetMinChildrenPerLine(1)
	w.cells.SetMaxChildrenPerLine(3)
	w.cells.SetColumnSpacing(1)
	w.cells.SetRowSpacing(1)
	w.cells.AddCSSClass("am-grid")
	for _, c := range overview.Cards() {
		if c.ID == actions.IDFullCleanup {
			continue // the hero
		}
		w.cells.Append(w.newCell(c))
	}
	box.Append(w.cells)

	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(box)
	w.refreshLastRun()
	return scroll
}

// newHero is Full Cleanup's cell: kicker, title, description, its plan as
// numbered steps, Run and Dry run first.
func (w *Window) newHero() gtk.Widgetter {
	card := cardByID(actions.IDFullCleanup)
	hero := gtk.NewBox(gtk.OrientationVertical, 10)
	hero.AddCSSClass("am-hero")
	hero.AddCSSClass("am-blueprint")

	preview, ok := w.planPreview(actions.IDFullCleanup)
	kicker := gtk.NewLabel(heroKicker(preview, ok))
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	title := gtk.NewLabel("Full Cleanup")
	title.SetXAlign(0)
	title.AddCSSClass("am-hero-title")
	desc := gtk.NewLabel(card.Desc)
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.AddCSSClass("am-hero-desc")
	hero.Append(kicker)
	hero.Append(title)
	hero.Append(desc)

	w.heroSteps = gtk.NewFlowBox()
	w.heroSteps.SetSelectionMode(gtk.SelectionNone)
	w.heroSteps.SetMinChildrenPerLine(1)
	w.heroSteps.SetMaxChildrenPerLine(2)
	w.heroSteps.SetHomogeneous(true)
	w.heroSteps.SetFocusable(false)
	if ok {
		for i, s := range preview.Steps {
			line := gtk.NewBox(gtk.OrientationHorizontal, 8)
			n := gtk.NewLabel(fmt.Sprintf("%02d", i+1))
			n.AddCSSClass("am-step-num")
			t := gtk.NewLabel(s.Title)
			t.SetXAlign(0)
			t.SetEllipsize(3) // pango.EllipsizeEnd: a step title in a narrow column
			t.SetHExpand(true)
			line.Append(n)
			line.Append(t)
			if s.Asks {
				line.Append(tag("asks", "am-tag-asks"))
			}
			if s.Root {
				line.Append(tag("root", "am-tag-root"))
			}
			w.heroSteps.Append(line)
		}
	}
	w.heroSteps.SetVisible(ok)
	hero.Append(w.heroSteps)

	run := gtk.NewButtonWithLabel("Run Full Cleanup")
	run.AddCSSClass("suggested-action")
	run.AddCSSClass("am-blueprint")
	run.ConnectClicked(func() { w.startAction(actions.IDFullCleanup) })
	dry := gtk.NewButtonWithLabel("Dry run first")
	dry.SetTooltipText("Runs it as a dry run this once: nothing on the system changes. Settings stay as they are.")
	dry.ConnectClicked(func() { w.startDry(actions.IDFullCleanup) })
	w.dryFirst = dry
	spin := adw.NewSpinner()
	spin.SetVisible(false)
	sub := gtk.NewLabel("")
	sub.AddCSSClass("am-cell-sub")
	// A FlowBox, not a Box: the row wraps when the window is narrow, so
	// the page's minimum width is its widest button, not the whole row
	// (a Box made the Overview 371 px wide at least, and the toast overlay
	// logged "exceeds AdwApplicationWindow width" in a 360 px window).
	buttons := gtk.NewFlowBox()
	buttons.SetSelectionMode(gtk.SelectionNone)
	buttons.SetFocusable(false)
	buttons.SetMaxChildrenPerLine(4)
	buttons.SetColumnSpacing(8)
	buttons.SetRowSpacing(8)
	buttons.SetHAlign(gtk.AlignStart)
	for _, c := range []gtk.Widgetter{run, dry, spin, sub} {
		buttons.Append(c)
	}
	for c := buttons.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		gtk.BaseWidget(c).SetFocusable(false)
	}
	hero.Append(buttons)
	w.menu[actions.IDFullCleanup] = &menuRow{entry: card.Entry, cell: run, sub: sub, spin: spin}
	return hero
}

// heroKicker is the hero's kicker (spec 1.11).
func heroKicker(p overview.Preview, ok bool) string {
	if !ok {
		return "Recommended"
	}
	return "Recommended · " + plural.N(len(p.Steps), "step", "steps") + " · root steps in one authorisation"
}

// cardByID is action id's Overview card (its menu entry when it has none).
func cardByID(id string) overview.Card {
	for _, c := range overview.Cards() {
		if c.ID == id {
			return c
		}
	}
	return overview.Card{Entry: overview.Entry{ID: id, Label: overview.Label(id)}}
}

// planPreview is action id's plan under the window's own Env (the polkit
// plan on the real Driver), or ok false when there is none to show: an
// action that discovers its steps, or an Env without the actions' wiring
// (the stub's).
func (w *Window) planPreview(id string) (overview.Preview, bool) {
	env := w.opts.Engine.Env
	if env.Validate() != nil {
		return overview.Preview{}, false
	}
	return overview.PlanPreview(env, id)
}

// tag is a small label tag ("root", "asks").
func tag(text, class string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("am-tag")
	l.AddCSSClass(class)
	return l
}

// newCell is an action's cell: a button holding its icon, the root tag,
// the title, the description and the running line.
func (w *Window) newCell(c overview.Card) gtk.Widgetter {
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	head.Append(gtk.NewImageFromIconName(c.Icon))
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	head.Append(spacer)
	spin := adw.NewSpinner()
	spin.SetVisible(false)
	head.Append(spin)
	if c.Root {
		head.Append(tag("root", "am-tag-root"))
	}
	title := gtk.NewLabel(c.Label)
	title.SetXAlign(0)
	title.SetWrap(true)
	title.AddCSSClass("am-cell-title")
	desc := gtk.NewLabel(c.Desc)
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.AddCSSClass("am-cell-desc")
	// Wrapped labels ask for their whole text's width; a modest natural
	// width lets the grid put two or more cells on a line.
	title.SetMaxWidthChars(24)
	desc.SetMaxWidthChars(30)
	sub := gtk.NewLabel("")
	sub.SetXAlign(0)
	sub.AddCSSClass("am-cell-sub")
	sub.SetVisible(false)
	inner := gtk.NewBox(gtk.OrientationVertical, 6)
	inner.Append(head)
	inner.Append(title)
	inner.Append(desc)
	inner.Append(sub)
	b := gtk.NewButton()
	b.SetChild(inner)
	b.AddCSSClass("am-cell")
	b.AddCSSClass("flat")
	id := c.ID
	b.ConnectClicked(func() { w.startAction(id) })
	w.menu[c.ID] = &menuRow{entry: c.Entry, cell: b, sub: sub, spin: spin}
	return b
}

// newLastRun is the last run's cell (spec 1.2): kicker, action, outcome
// tag, when, its issues, its run's log files and Open in Logs. The files
// label and the button follow w.lastIssues in the cell (lastRunExtras).
func (w *Window) newLastRun() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("am-cell")
	box.AddCSSClass("am-last-run")
	kicker := gtk.NewLabel("LAST RUN")
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	w.lastIcon = gtk.NewImage()
	w.lastTitle = gtk.NewLabel("")
	w.lastTitle.SetXAlign(0)
	w.lastTitle.SetWrap(true)
	w.lastTitle.SetHExpand(true)
	w.lastTitle.AddCSSClass("am-cell-title")
	w.lastTag = tag("", "am-tag-ok")
	head.Append(w.lastIcon)
	head.Append(w.lastTitle)
	head.Append(w.lastTag)
	w.lastIssues = gtk.NewLabel("")
	w.lastIssues.SetXAlign(0)
	w.lastIssues.SetWrap(true)
	w.lastIssues.AddCSSClass("am-cell-desc")
	w.lastTitle.SetMaxWidthChars(24)
	w.lastIssues.SetMaxWidthChars(30)
	files := gtk.NewLabel("")
	files.SetXAlign(0)
	files.SetWrap(true)
	files.AddCSSClass("am-cell-desc")
	files.AddCSSClass("monospace")
	files.SetVisible(false)
	open := gtk.NewButtonWithLabel("Open in Logs")
	open.SetHAlign(gtk.AlignStart)
	open.AddCSSClass("flat")
	open.SetVisible(false)
	open.ConnectClicked(w.openLastRunLogs)
	box.Append(kicker)
	box.Append(head)
	box.Append(w.lastIssues)
	box.Append(files)
	box.Append(open)
	return box
}

// lastRunExtras are the last run cell's log files label and Open in Logs
// button: the two widgets after w.lastIssues (newLastRun), found there so
// the Window needs no fields for them.
func (w *Window) lastRunExtras() (*gtk.Label, *gtk.Button) {
	files, _ := gtk.BaseWidget(w.lastIssues).NextSibling().(*gtk.Label)
	var open *gtk.Button
	if files != nil {
		open, _ = gtk.BaseWidget(files).NextSibling().(*gtk.Button)
	}
	return files, open
}

// lastRunFolder is the last run's run directory (row 12's runFolder, by
// the record's start), if the record and the folder are there.
func (w *Window) lastRunFolder() (logview.Run, bool) {
	r := w.lastRun.Record
	if r == nil || lastCancelled(w.lastRun) {
		return logview.Run{}, false
	}
	runs, err := logview.Runs(w.opts.StateDir)
	if err != nil {
		return logview.Run{}, false
	}
	return recordFolder(runs, *r)
}

// nothingRan is the last run cell's line, in place of the log files, for
// a cancelled run.
const nothingRan = "Nothing ran, so there are no logs"

// lastCancelled: the last run was cancelled before any step ran (a
// declined gate, a cancelled password): it wrote no run folder.
func lastCancelled(lr overview.LastRun) bool {
	return lr.Record != nil && lr.Record.Outcome == logstore.OutcomeCancelled
}

// openLastRunLogs is Open in Logs: the Logs page at the last run's folder.
func (w *Window) openLastRunLogs() {
	run, ok := w.lastRunFolder()
	w.refreshLogs()
	if ok {
		for _, r := range w.logs.runList {
			if r.Name == run.Name {
				w.showRun(r, "")
			}
		}
	}
	w.stack.SetVisibleChildName("logs")
}

// lastTone is how the last run's outcome is drawn: its icon, tag text and
// tone class. A cancelled run is neutral (grey), as the DMS widget draws
// it: nothing ran, nothing to warn about.
func lastTone(lr overview.LastRun) (icon, text, class string) {
	if lr.Record != nil && lr.Record.Outcome == logstore.OutcomeCancelled {
		if overview.NotAuthorised(*lr.Record) {
			return "dialog-warning-symbolic", "NOT AUTHORISED", "am-tag-warn"
		}
		return "content-loading-symbolic", "CANCELLED", "am-tag-neutral"
	}
	switch lr.Level {
	case overview.LevelNone:
		return "content-loading-symbolic", "", "am-tag-neutral"
	case overview.LevelWarn:
		t := "WARN"
		if lr.Record != nil {
			t = strings.ReplaceAll(strings.ToUpper(lr.Record.Outcome), "-", " ")
		}
		return "dialog-warning-symbolic", t, "am-tag-warn"
	case overview.LevelError:
		t := "FAILED"
		if lr.Record != nil {
			t = strings.ReplaceAll(strings.ToUpper(lr.Record.Outcome), "-", " ")
		}
		return "dialog-error-symbolic", t, "am-tag-danger"
	}
	if lr.Record != nil && lr.Record.Outcome == logstore.OutcomeNothingFound {
		// The action looked and found nothing to do: fine, and said so.
		return "emblem-ok-symbolic", "NOTHING FOUND", "am-tag-ok"
	}
	return "emblem-ok-symbolic", "OK", "am-tag-ok"
}

// refreshLastRun rereads last-run.json (at start and after each run).
func (w *Window) refreshLastRun() {
	lr := overview.ReadLastRun(w.opts.StateDir, time.Now())
	w.lastRun = lr
	w.lastTitle.SetText(lr.Title)
	w.lastIssues.SetText(strings.Join(lr.Issues, "\n"))
	w.lastIssues.SetVisible(len(lr.Issues) > 0)
	icon, text, class := lastTone(lr)
	w.lastIcon.SetFromIconName(icon)
	for _, c := range []string{"am-tag-ok", "am-tag-warn", "am-tag-danger", "am-tag-neutral"} {
		w.lastIcon.RemoveCSSClass(c)
		w.lastTag.RemoveCSSClass(c)
	}
	w.lastIcon.AddCSSClass(class)
	w.lastTag.AddCSSClass(class)
	w.lastTag.SetText(text)
	w.lastTag.SetVisible(text != "")

	files, open := w.lastRunExtras()
	if files == nil || open == nil {
		return
	}
	if lastCancelled(lr) {
		// Nothing ran, so it has no run folder: the folder lastRunFolder
		// would fall back to is another run's (Dev 5).
		files.SetText(nothingRan)
		files.SetVisible(true)
		open.SetVisible(false)
		return
	}
	run, ok := w.lastRunFolder()
	var names []string
	for _, c := range run.Categories {
		names = append(names, string(c)+".log")
	}
	files.SetText(strings.Join(names, " · "))
	files.SetVisible(ok && len(names) > 0)
	open.SetVisible(ok)
}

// overviewState is what the Overview's widgets show, so a flush that
// changes none of it (an output flood) costs a compare, not ~30 widget
// calls.
type overviewState struct {
	current, busyText string
	cache             string
	busy              bool
	phase             engine.Phase
}

// updateOverview redraws the cells' running lines and sensitivity, when
// what they show changed.
func (w *Window) updateOverview() {
	st := overviewState{w.current, w.model.Busy, w.cache.line, w.busy, w.model.Phase}
	if w.shownOverview != nil && *w.shownOverview == st {
		return
	}
	w.shownOverview = &st
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
		} else if id == actions.IDUserCache {
			sub = w.cache.line
		}
		r.sub.SetText(sub)
		r.sub.SetVisible(sub != "")
		// One action at a time.
		r.cell.SetSensitive(!w.busy)
	}
	w.dryFirst.SetSensitive(!w.busy)
}
