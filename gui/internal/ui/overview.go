package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

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
	// Last run and the Updates card are the grid's top row, outside the
	// FlowBox: in it, a seventh cell left an empty slot at the end of the
	// last line, drawn in the grid's hairline grey (Dev 5's VM round r8).
	// The FlowBox holds the six actions, and at most three to a line
	// divides them evenly. Where it has three columns, Last run spans the
	// first two and Updates is the third, as wide as a cell (layoutTop);
	// else Updates sits under Last run at full width.
	w.topRows.head, w.topRows.line = gtk.NewSizeGroup(gtk.SizeGroupVertical), gtk.NewSizeGroup(gtk.SizeGroupVertical)
	w.topLast = w.newLastRun()
	w.topUpd = w.newUpdatesCard()
	w.top = gtk.NewGrid()
	w.top.AddCSSClass("am-grid")
	w.top.SetColumnSpacing(1) // the grid's hairline between the cards
	w.top.SetColumnHomogeneous(true)
	w.topCol = gtk.NewBox(gtk.OrientationVertical, 1)
	w.topCol.AddCSSClass("am-grid")
	w.layoutTop(false)
	box.Append(w.top)
	box.Append(w.topCol)

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
	// Run topgrade's way in is the Updates card's button now: it is the
	// topgrade row (no cell of its own), so a start by action ID, the
	// running state and the sensitivity treat it as before (#270).
	if w.upd != nil {
		for _, e := range overview.Entries() {
			if e.ID == actions.IDTopgrade {
				spin := adw.NewSpinner()
				w.menu[e.ID] = &menuRow{entry: e, cell: w.upd.topg, sub: gtk.NewLabel(""), spin: spin}
			}
		}
	}
	box.Append(w.cells)
	// Off screen the tick doesn't run, and a stale wide layout would count in
	// the Overview's minimum width (a hidden stack page does): back to the
	// stacked one, which the tick undoes on the next frame it is shown.
	w.cells.ConnectUnmap(func() { w.layoutTop(false) })
	w.cells.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if w.cells.Mapped() { // else the sizes are stale
			w.layoutTop(w.cellsThreeWide())
		}
		return !w.closed
	})

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
	hero.AddCSSClass("am-over-grid") // glass.css: on the cells' edges

	preview, ok := w.planPreview(actions.IDFullCleanup)
	w.kickerText = [2]string{heroKicker(preview, ok, false), heroKicker(preview, ok, true)}
	kicker := gtk.NewLabel(w.kickerText[0])
	w.heroKicker = kicker
	kicker.SetXAlign(0)
	// It wraps: on one line it alone made the Overview 529 px wide at least
	// (Dev 5's VM round b5, in a 360 px window). When narrow its text
	// breaks where it reads well (heroKicker), not wherever the line ends.
	wrapNatural(kicker)
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
	w.heroSteps.AddCSSClass("am-card-buttons") // no child padding: the plan starts at the hero's text
	// The gap the child padding gave (3 px each side, plus the 2 px of the
	// wrap): 8 px between the columns, 6 between the rows (#264).
	w.heroSteps.SetColumnSpacing(8)
	w.heroSteps.SetRowSpacing(6)
	w.fillHeroPlan(preview, ok)
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
	// When narrow the two are stacked at the hero's full width
	// (setNarrow): left-aligned at their own widths, one over the other,
	// they looked left over (the owner's r532 screenshot).
	buttons := gtk.NewFlowBox()
	w.heroButtons = buttons
	buttons.SetSelectionMode(gtk.SelectionNone)
	buttons.SetFocusable(false)
	buttons.AddCSSClass("am-card-buttons") // no child padding: they start at the hero's text
	buttons.SetColumnSpacing(8)
	buttons.SetRowSpacing(8)
	w.stackButtons(w.narrow)
	for _, c := range []gtk.Widgetter{run, dry, spin, sub} {
		buttons.Append(c)
	}
	for c := buttons.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		gtk.BaseWidget(c).SetFocusable(false)
	}
	setShown(spin, false)
	setShown(sub, false)
	hero.Append(buttons)
	w.menu[actions.IDFullCleanup] = &menuRow{entry: card.Entry, cell: run, sub: sub, spin: spin}
	return hero
}

// fillHeroPlan fills the hero's plan: a row per step, its number, its title
// and its tags (the Preview's steps), after clearing what was there.
func (w *Window) fillHeroPlan(preview overview.Preview, ok bool) {
	for c := gtk.BaseWidget(w.heroSteps).FirstChild(); c != nil; c = gtk.BaseWidget(w.heroSteps).FirstChild() {
		w.heroSteps.Remove(c)
	}
	w.heroLines = nil
	if ok {
		for i, s := range preview.Steps {
			// The number, then the title and its tags (tagLine), on the
			// title's first baseline: the title wraps rather than being
			// cut, and when narrow the tags go under it.
			head := gtk.NewBox(gtk.OrientationHorizontal, 8)
			head.SetVAlign(gtk.AlignCenter)
			n := gtk.NewLabel(fmt.Sprintf("%02d", i+1))
			n.AddCSSClass("am-step-num")
			n.SetVAlign(gtk.AlignBaselineCenter)
			t := gtk.NewLabel(s.Title)
			t.SetXAlign(0)
			wrapNatural(t)
			var tags []gtk.Widgetter
			if s.Asks {
				tags = append(tags, tag("asks", "am-tag-asks"))
			}
			switch {
			case s.Root:
				tags = append(tags, privTag("root"))
			case s.Sudo:
				tags = append(tags, privTag("sudo"))
			}
			line := newTagLine(t, 8, tags...)
			line.tags.SetVisible(len(tags) > 0)
			line.box.SetVAlign(gtk.AlignBaselineCenter)
			line.setUnder(w.narrow)
			w.heroLines = append(w.heroLines, line)
			head.Append(n)
			head.Append(line.box)
			w.heroSteps.Append(head)
		}
	}
}

// heroKicker is the hero's kicker (spec 1.11). Narrow, it doesn't fit one
// line, so it breaks at its second separator instead of before its last
// word or two.
func heroKicker(p overview.Preview, ok, narrow bool) string {
	if !ok {
		return "Recommended"
	}
	sep := " · "
	if narrow {
		sep = "\n"
	}
	// The root count is the plan's root-tagged steps: topgrade, tagged sudo,
	// is not one of the steps in the one authorisation.
	return "Recommended · " + plural.N(len(p.Steps), "step", "steps") + sep + plural.N(p.Root, "root step", "root steps") + " in one authorisation"
}

// setNarrow lays the window's rows out for a window under the narrow
// breakpoint (narrow) or above it: the tags of the hero's plan, of the Run
// page's step headers and of the action cells under their titles or at
// the line's end, the hero's buttons stacked at full width or side by
// side, and the kickers' text.
func (w *Window) setNarrow(narrow bool) {
	w.narrow = narrow
	if narrow {
		w.win.AddCSSClass("am-narrow")
	} else {
		w.win.RemoveCSSClass("am-narrow")
	}
	if w.heroKicker != nil {
		i := 0
		if narrow {
			i = 1
		}
		w.heroKicker.SetText(w.kickerText[i])
	}
	for _, l := range w.heroLines {
		l.setUnder(narrow)
	}
	w.stackButtons(narrow)
	for _, r := range w.cellRoots {
		r[0].SetVisible(!narrow)
		r[1].SetVisible(narrow)
	}
	if w.tl != nil {
		for _, h := range w.tl.headers {
			h.line.setUnder(narrow)
		}
	}
	w.setSummaryNarrow(narrow)
	w.pkgs.setNarrow(narrow)
}

// setShown shows or hides wd, and the flow box child holding it if it is
// in one (the hero's spinner and running line): a flow box keeps a line,
// and its spacing, for a child whose content is hidden, which left a gap
// under the stacked buttons.
func setShown(wd gtk.Widgetter, on bool) {
	b := gtk.BaseWidget(wd)
	b.SetVisible(on)
	if fc, ok := b.Parent().(*gtk.FlowBoxChild); ok {
		fc.SetVisible(on)
	}
}

// stackButtons stacks the hero's buttons at its full width (narrow), or
// puts them side by side at their own widths.
func (w *Window) stackButtons(narrow bool) {
	b := w.heroButtons
	if b == nil {
		return
	}
	if narrow {
		b.SetMaxChildrenPerLine(1)
		b.SetHAlign(gtk.AlignFill)
		return
	}
	b.SetMaxChildrenPerLine(4)
	b.SetHAlign(gtk.AlignStart)
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

// privTag is a step's privilege tag (runview.PrivTag: "root" or "sudo")
// as a tag, nil for "".
func privTag(priv string) *gtk.Label {
	if priv == "" {
		return nil
	}
	return tag(priv, "am-tag-"+priv)
}

// setPrivClass gives a privilege tag its tone: am-tag-root for "root",
// am-tag-sudo for "sudo".
func setPrivClass(l *gtk.Label, priv string) {
	l.RemoveCSSClass("am-tag-root")
	l.RemoveCSSClass("am-tag-sudo")
	if priv != "" {
		l.AddCSSClass("am-tag-" + priv)
	}
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
	title := gtk.NewLabel(c.Label)
	title.SetXAlign(0)
	title.SetWrap(true)
	title.AddCSSClass("am-cell-title")
	// The root tag: at the icon line's end, or under the title when
	// narrow, as the hero's plan has it (setNarrow).
	var rootUnder *gtk.Label
	if c.Root {
		wide := privTag(c.Tag)
		wide.SetVAlign(gtk.AlignCenter)
		head.Append(wide)
		rootUnder = privTag(c.Tag)
		rootUnder.SetHAlign(gtk.AlignStart)
		wide.SetVisible(!w.narrow)
		rootUnder.SetVisible(w.narrow)
		w.cellRoots = append(w.cellRoots, [2]*gtk.Label{wide, rootUnder})
	}
	// System health: a look tag at the icon line's end and a third line
	// (healthview.CellFor); both hidden while the cell is calm.
	var healthLook, healthAttn *gtk.Label
	if c.ID == overview.IDHealth {
		healthLook = tag("look", "am-tag-warn")
		healthLook.SetVAlign(gtk.AlignCenter)
		healthLook.SetVisible(false)
		head.Append(healthLook)
		healthAttn = gtk.NewLabel("")
		healthAttn.SetXAlign(0)
		healthAttn.SetWrap(true)
		healthAttn.SetVisible(false)
	}
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
	if rootUnder != nil {
		inner.Append(rootUnder)
	}
	inner.Append(desc)
	if healthAttn != nil {
		inner.Append(healthAttn)
	}
	inner.Append(sub)
	b := gtk.NewButton()
	b.SetChild(inner)
	b.AddCSSClass("am-cell")
	b.AddCSSClass("flat")
	id := c.ID
	if healthAttn != nil {
		b.ConnectClicked(w.openHealth) // not an action: a sheet (#270)
	} else {
		b.ConnectClicked(func() { w.startAction(id) })
	}
	w.menu[c.ID] = &menuRow{entry: c.Entry, cell: b, sub: sub, spin: spin}
	if healthAttn != nil {
		icon, _ := gtk.BaseWidget(head).FirstChild().(*gtk.Image)
		w.initHealth(b, icon, healthLook, healthAttn)
	}
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
	// Its own height, centred on the title as the icon is: filling the
	// row drew a tall box beside a title wrapped over 2-3 lines.
	w.lastTag.SetVAlign(gtk.AlignCenter)
	head.Append(w.lastIcon)
	head.Append(w.lastTitle)
	head.Append(w.lastTag)
	w.lastIssues = gtk.NewLabel("")
	w.lastIssues.SetXAlign(0)
	w.lastIssues.SetWrap(true)
	w.lastIssues.SetWrapMode(pango.WrapWordChar) // an error's path wraps inside itself
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
	// Raised, as the Updates card's buttons are (#260).
	open.SetVisible(false)
	open.ConnectClicked(w.openLastRunLogs)
	// The two cards' rows are as tall as each other's, so their buttons
	// are on one row (#260): a vertical size group over the title rows and
	// over the files line and the Updates card's detail line.
	w.topRows.head.AddWidget(head)
	w.topRows.line.AddWidget(files)
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
	busy, prompt      bool
	phase             engine.Phase
}

// updateOverview redraws the cells' running lines and sensitivity, when
// what they show changed.
func (w *Window) updateOverview() {
	w.renderUpdates()
	st := overviewState{w.current, w.model.Busy, w.cache.line, w.busy, w.model.Prompt != nil, w.model.Phase}
	if w.shownOverview != nil && *w.shownOverview == st {
		return
	}
	w.shownOverview = &st
	for id, r := range w.menu {
		running := w.busy && id == w.current
		setShown(r.spin, running)
		sub := ""
		if running && w.model.Phase == engine.PhaseDiscovering && w.model.Prompt != nil {
			// The discovery is over, its pick (or gate) waits for the user:
			// the cell is back to its idle lines, as the status line says
			// "Waiting…". Busy stays set in the model until the next phase.
			if id == actions.IDClearCaches {
				sub = w.cache.line
			}
		} else if running && w.model.Phase == engine.PhaseDiscovering {
			sub = w.model.Busy
			if sub == "" {
				sub = r.entry.Busy
			}
		} else if running {
			sub = "Running"
		} else if id == actions.IDClearCaches {
			sub = w.cache.line
		}
		r.sub.SetText(sub)
		setShown(r.sub, sub != "")
		// One action at a time. System health runs nothing: its sheet opens
		// while a run goes too.
		r.cell.SetSensitive(!w.busy || id == overview.IDHealth)
	}
	w.dryFirst.SetSensitive(!w.busy)
}

// cellsThreeWide: the cell grid has three columns now (its first and third
// cells are on one line).
func (w *Window) cellsThreeWide() bool {
	c0, c2 := w.cells.ChildAtIndex(0), w.cells.ChildAtIndex(2)
	if c0 == nil || c2 == nil || gtk.BaseWidget(c2).Width() <= 0 {
		return w.topWide
	}
	r, ok := gtk.BaseWidget(c2).ComputeBounds(c0)
	// On one line: a few px of slack, for cells that start a little apart
	// (the glass skin's 3 px child padding); the next line is a cell away.
	return ok && r.X() > 1 && r.Y() > -6 && r.Y() < 6
}

// layoutTop puts Updates in the grid's third column beside Last run (wide:
// the cell grid below has three columns), or under it at full width. The
// top grid has three homogeneous columns one hairline apart, as the cell
// grid has, so a column here is a column there.
func (w *Window) layoutTop(wide bool) {
	if w.top == nil || (w.topLaid && wide == w.topWide) {
		return
	}
	if w.topLaid {
		if w.topWide {
			w.top.Remove(w.topLast)
			w.top.Remove(w.topUpd)
		} else {
			w.topCol.Remove(w.topLast)
			w.topCol.Remove(w.topUpd)
		}
	}
	w.topLaid, w.topWide = true, wide
	// Two containers, one shown: a Grid spans the cards over three
	// homogeneous columns when wide; stacked, a Box does, because a Grid
	// counts a spanning child's whole minimum width in each column it
	// spans, which made the Overview 604 px wide at least (TestGUIRunReport,
	// 360 px).
	w.top.SetVisible(wide)
	w.topCol.SetVisible(!wide)
	if wide {
		w.top.Attach(w.topLast, 0, 0, 2, 1)
		w.top.Attach(w.topUpd, 2, 0, 1, 1)
	} else {
		w.topCol.Append(w.topLast)
		w.topCol.Append(w.topUpd)
	}
}
