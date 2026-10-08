package ui

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The Run page's timeline (P8 row 11; spec 1.5, 3.2): the run's header
// (kicker, elapsed, counts, the segment bar), then ONE GtkListView over a
// GtkFlattenListModel of per-step GtkStringLists. Each step is a section:
// its header (marker, number, title, root tag, state line) is drawn by the
// header factory, and its list holds its output lines, or one summary item
// while it is collapsed (a section is never empty: an empty one draws no
// header). Only the visible rows and headers exist as widgets, as before.
//
// Throughput: a flush is one Splice on the running step's list (plus one
// on the oldest step's when the model's cap trims), driven by the model's
// grouped diff (Change.Steps). A line's colour is found by its place in its
// step (runview.Model.StepLine), parsed at bind for the ~30 visible rows.
//
// Which steps are open: the running one, a failed one, and the last one
// that ran (so a finished run still shows its end); a finished step folds
// to its summary line once a later step starts. A click on a step's header
// opens or folds it, and that choice then sticks for the run.

// summaryItem marks a section's one summary item: a control character
// (record separator), which sanitized output lines never hold. (Not NUL:
// a GtkStringList keeps C strings.)
const summaryItem = "\x1e"

// section is one step's part of the timeline.
type section struct {
	step string
	list *gtk.StringList
	// n is the step's lines in the model's view; items is what list holds:
	// n lines when lines is set, else 1 (the summary item).
	n, items int
	lines    bool
	// userSet: the user opened or folded it (userOpen); auto otherwise.
	userSet, userOpen bool
	header            *stepHeader // the bound header widget, or nil
	summary           string      // what its summary item last showed
}

// stepHeader is a step's header widget (one per bound list header).
type stepHeader struct {
	box   *gtk.Box
	icon  *gtk.Image
	spin  *adw.Spinner
	num   *gtk.Label
	title *gtk.Label
	root  *gtk.Label
	sub   *gtk.Label
	acts  *failedActions // a failed step's actions (failed.go)
	sec   *section
}

// timeline is the Run page's list and its sections.
type timeline struct {
	store    *gio.ListStore
	flat     *gtk.FlattenListModel
	view     *gtk.ListView
	scroll   *gtk.ScrolledWindow
	sections []*section
	byStep   map[string]*section
	headers  map[uintptr]*stepHeader // by the GtkListHeader they belong to

	// The run header.
	kicker, meta, elapsed, counts *gtk.Label
	segments                      *gtk.Box
	segs                          []*gtk.Box
	ticking                       bool
	foldQueued                    bool // foldNow is queued
}

// newRunPage builds the Run page: the run header over the timeline.
func (w *Window) newRunPage() gtk.Widgetter {
	tl := &timeline{byStep: map[string]*section{}, headers: map[uintptr]*stepHeader{}}
	w.tl = tl

	tl.store = gio.NewListStore(gio.GTypeListModel)
	tl.flat = gtk.NewFlattenListModel(tl.store)

	lines := gtk.NewSignalListItemFactory()
	lines.ConnectSetup(func(obj *coreglib.Object) {
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		label.AddCSSClass("am-line")
		obj.Cast().(*gtk.ListItem).SetChild(label)
	})
	lines.ConnectBind(func(obj *coreglib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		w.bindLine(item.Child().(*gtk.Label), item.Item().Cast().(*gtk.StringObject).String(), item.Position())
	})
	headers := gtk.NewSignalListItemFactory()
	headers.ConnectSetup(func(obj *coreglib.Object) {
		lh := obj.Cast().(*gtk.ListHeader)
		h, row := w.newStepHeader()
		lh.SetChild(h.box)
		tl.headers[coreglib.InternObject(lh).Native()] = h
		// A click on the header's line folds or opens the step (not one on
		// its failed-step buttons below it).
		click := gtk.NewGestureClick()
		click.ConnectReleased(func(int, float64, float64) {
			if h.sec != nil {
				w.toggleStep(h.sec)
			}
		})
		row.AddController(click)
	})
	headers.ConnectBind(func(obj *coreglib.Object) {
		lh := obj.Cast().(*gtk.ListHeader)
		h := tl.headers[coreglib.InternObject(lh).Native()]
		if h == nil {
			return
		}
		sec := tl.sectionAt(lh.Start())
		h.sec = sec
		if sec != nil {
			sec.header = h
		}
		w.drawHeader(h)
	})
	headers.ConnectUnbind(func(obj *coreglib.Object) {
		h := tl.headers[coreglib.InternObject(obj).Native()]
		if h != nil && h.sec != nil && h.sec.header == h {
			h.sec.header = nil
		}
		if h != nil {
			h.sec = nil
		}
	})
	headers.ConnectTeardown(func(obj *coreglib.Object) {
		delete(tl.headers, coreglib.InternObject(obj).Native())
	})

	tl.view = gtk.NewListView(gtk.NewNoSelection(tl.flat), &lines.ListItemFactory)
	tl.view.SetHeaderFactory(&headers.ListItemFactory)
	tl.view.AddCSSClass("monospace")
	tl.view.AddCSSClass("am-timeline")
	w.outView = tl.view
	tl.scroll = gtk.NewScrolledWindow()
	tl.scroll.SetChild(tl.view)
	tl.scroll.SetVExpand(true)
	tl.scroll.SetHExpand(true)

	// Follow the newest line: scrolling up with the wheel, dragging the
	// scrollbar or a key stops it; reaching the bottom again restarts it.
	w.follow = true
	wheel := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	wheel.ConnectScroll(func(_, dy float64) bool {
		if dy < 0 {
			w.follow = false
		}
		return false
	})
	tl.scroll.AddController(wheel)
	w.dragStop = gtk.NewGestureDrag()
	w.dragStop.ConnectDragBegin(func(float64, float64) { w.follow = false })
	gtk.BaseWidget(tl.scroll.VScrollbar()).AddController(w.dragStop)
	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Up, gdk.KEY_Page_Up, gdk.KEY_Home, gdk.KEY_KP_Up, gdk.KEY_KP_Page_Up:
			w.follow = false
		}
		return false
	})
	tl.view.AddController(keys)
	tl.scroll.ConnectEdgeReached(func(pos gtk.PositionType) {
		if pos == gtk.PosBottom {
			w.follow = true
		}
	})

	page := gtk.NewBox(gtk.OrientationVertical, 0)
	page.Append(w.newRunHeader())
	page.Append(tl.scroll)
	return page
}

// newRunHeader is the run's header: kicker and meta, the elapsed counter,
// the counts, and the segment bar (one segment per step).
func (w *Window) newRunHeader() gtk.Widgetter {
	tl := w.tl
	tl.kicker = gtk.NewLabel("")
	tl.kicker.SetXAlign(0)
	tl.kicker.AddCSSClass("am-kicker")
	tl.meta = gtk.NewLabel("")
	tl.meta.SetXAlign(0)
	tl.meta.AddCSSClass("am-run-meta")
	tl.counts = gtk.NewLabel("")
	tl.counts.SetXAlign(0)
	tl.counts.AddCSSClass("am-run-counts")
	left := gtk.NewBox(gtk.OrientationVertical, 2)
	left.SetHExpand(true)
	left.Append(tl.kicker)
	left.Append(tl.meta)
	left.Append(tl.counts)
	tl.elapsed = gtk.NewLabel("")
	tl.elapsed.AddCSSClass("am-run-elapsed")
	tl.elapsed.AddCSSClass("numeric")
	row := gtk.NewBox(gtk.OrientationHorizontal, 12)
	row.Append(left)
	row.Append(tl.elapsed)
	tl.segments = gtk.NewBox(gtk.OrientationHorizontal, 2)
	tl.segments.SetHomogeneous(true)
	tl.segments.AddCSSClass("am-segments")
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.AddCSSClass("am-run-header")
	box.Append(row)
	box.Append(tl.segments)
	return box
}

// newStepHeader is a step header widget, and its clickable line.
func (w *Window) newStepHeader() (*stepHeader, *gtk.Box) {
	h := &stepHeader{icon: gtk.NewImage(), spin: adw.NewSpinner()}
	h.num = gtk.NewLabel("")
	h.num.AddCSSClass("am-step-num")
	h.title = gtk.NewLabel("")
	h.title.SetXAlign(0)
	h.title.SetHExpand(true)
	h.title.SetWrap(true)
	h.title.AddCSSClass("am-step-title")
	h.root = tag("root", "am-tag-root")
	h.sub = gtk.NewLabel("")
	h.sub.SetXAlign(1)
	h.sub.SetWrap(true)
	h.sub.SetUseMarkup(false)
	h.sub.AddCSSClass("am-step-sub")
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.Append(h.icon)
	row.Append(h.spin)
	row.Append(h.num)
	row.Append(h.title)
	row.Append(h.root)
	row.Append(h.sub)
	h.box = gtk.NewBox(gtk.OrientationVertical, 6)
	h.box.AddCSSClass("am-step-header")
	h.box.Append(row)
	h.acts = w.newFailedActions(h)
	h.box.Append(h.acts.box)
	return h, row
}

// rowOf is the model's row for step, and its index; -1 when it has none.
func (w *Window) rowOf(step string) (runview.Row, int) {
	for i, r := range w.model.Rows {
		if r.ID == step {
			return r, i
		}
	}
	return runview.Row{}, -1
}

// drawHeader fills h from its section's row.
func (w *Window) drawHeader(h *stepHeader) {
	if h.sec == nil {
		return
	}
	r, i := w.rowOf(h.sec.step)
	h.box.SetVisible(i >= 0)
	if i < 0 {
		return
	}
	h.num.SetText(fmt.Sprintf("%02d", i+1))
	h.title.SetText(r.Title)
	h.root.SetVisible(r.NeedsPriv)
	h.sub.SetText(r.Subtitle())
	icon := r.State.Icon()
	setStateClass(&h.icon.Widget, r)
	setStateClass(&h.spin.Widget, r)
	for _, c := range []string{"am-step-failed", "am-step-running"} {
		h.box.RemoveCSSClass(c)
	}
	switch {
	case r.State == runview.RowFailed:
		h.box.AddCSSClass("am-step-failed")
	case r.State == runview.RowRunning:
		h.box.AddCSSClass("am-step-running")
	}
	h.spin.SetVisible(icon == "")
	h.icon.SetVisible(icon != "")
	if icon != "" {
		h.icon.SetFromIconName(icon)
	}
	w.drawFailed(h, r)
}

// bindLine shows item text at flat position pos: an output line with its
// colour, or its section's summary.
func (w *Window) bindLine(label *gtk.Label, text string, pos uint) {
	sec, off := w.tl.locate(pos)
	if text == summaryItem {
		label.SetAttributes(nil)
		s := ""
		if sec != nil {
			if r, i := w.rowOf(sec.step); i >= 0 {
				s = sanitize.OneLine(r.Summary())
			}
		}
		label.SetText(s)
		label.SetVisible(s != "")
		label.RemoveCSSClass("am-line")
		label.AddCSSClass("am-summary")
		return
	}
	label.SetVisible(true)
	label.RemoveCSSClass("am-summary")
	label.AddCSSClass("am-line")
	label.SetText(text)
	var attrs *pango.AttrList
	if sec != nil {
		if l, ok := w.model.StepLine(sec.step, off); ok && l.Text == text && len(l.Spans) > 0 {
			attrs = pango.AttrListFromString(l.Attrs(&w.palette))
		}
	}
	label.SetAttributes(attrs)
}

// locate is the section holding flat position pos, and pos's offset in it.
func (tl *timeline) locate(pos uint) (*section, int) {
	start := uint(0)
	for _, s := range tl.sections {
		if pos < start+uint(s.items) {
			return s, int(pos - start)
		}
		start += uint(s.items)
	}
	return nil, 0
}

// sectionAt is the section starting at flat position start.
func (tl *timeline) sectionAt(start uint) *section {
	s, off := tl.locate(start)
	if off != 0 {
		return nil
	}
	return s
}

// total is the flat list's length.
func (tl *timeline) total() uint {
	n := uint(0)
	for _, s := range tl.sections {
		n += uint(s.items)
	}
	return n
}

// newSection is an empty section for step (its summary item only).
func newSection(step string) *section {
	return &section{step: step, list: gtk.NewStringList([]string{summaryItem}), items: 1}
}

// rebuild makes one empty section per row (a new plan, or Clear).
func (w *Window) rebuild() {
	tl := w.tl
	tl.sections = tl.sections[:0]
	clear(tl.byStep)
	var lists []*coreglib.Object
	for _, r := range w.model.Rows {
		s := newSection(r.ID)
		tl.sections = append(tl.sections, s)
		tl.byStep[r.ID] = s
		lists = append(lists, coreglib.InternObject(s.list))
	}
	tl.store.Splice(0, tl.store.NItems(), lists)
	w.outLen = 0
}

// insert adds a section for the row inserted at index i.
func (w *Window) insert(i int) {
	tl := w.tl
	if i < 0 || i >= len(w.model.Rows) {
		return
	}
	s := newSection(w.model.Rows[i].ID)
	at := i
	if len(tl.sections) > 0 && tl.sections[0].step == "" && tl.byStep[""] == tl.sections[0] {
		at++ // after the lines outside any step
	}
	at = min(at, len(tl.sections))
	tl.sections = append(tl.sections[:at], append([]*section{s}, tl.sections[at:]...)...)
	tl.byStep[s.step] = s
	tl.store.Splice(uint(at), 0, []*coreglib.Object{coreglib.InternObject(s.list)})
}

// sectionFor is step's section; lines outside any step get one of their
// own, first.
func (w *Window) sectionFor(step string) *section {
	tl := w.tl
	if s := tl.byStep[step]; s != nil {
		return s
	}
	s := newSection(step)
	s.userSet, s.userOpen = true, true
	tl.sections = append([]*section{s}, tl.sections...)
	tl.byStep[step] = s
	tl.store.Splice(0, 0, []*coreglib.Object{coreglib.InternObject(s.list)})
	return s
}

// wantOpen is whether s shows its lines: the user's choice, else open
// while it runs, when it failed, and for the last step that ran.
func (w *Window) wantOpen(s *section, last string) bool {
	if s.userSet {
		return s.userOpen
	}
	r, i := w.rowOf(s.step)
	if i < 0 {
		return true
	}
	switch r.State {
	case runview.RowRunning, runview.RowFailed:
		return true
	case runview.RowPending, runview.RowNotRun:
		return false
	}
	return s.step == last
}

// lastStarted is the ID of the last row that started (not pending or not
// run).
func (w *Window) lastStarted() string {
	last := ""
	for _, r := range w.model.Rows {
		if r.State != runview.RowPending && r.State != runview.RowNotRun {
			last = r.ID
		}
	}
	return last
}

// show makes s hold its lines (open) or its summary item.
func (w *Window) show(s *section, open bool) {
	want := open && s.n > 0
	if want == s.lines {
		return
	}
	if want {
		lines := w.model.StepText(s.step)
		s.n, s.lines = len(lines), true
		w.outLen += uint(len(lines))
		w.replaceList(s, lines)
	} else {
		w.outLen -= uint(s.items)
		s.lines, s.summary = false, ""
		if r, i := w.rowOf(s.step); i >= 0 {
			s.summary = r.Summary() // what the new item binds to
		}
		w.replaceList(s, []string{summaryItem})
	}
}

// replaceList gives s a new list holding items, in place of its old one
// in the store: one items-changed for the section.
func (w *Window) replaceList(s *section, items []string) {
	tl := w.tl
	i := 0
	for i < len(tl.sections) && tl.sections[i] != s {
		i++
	}
	if i == len(tl.sections) {
		return
	}
	s.list = gtk.NewStringList(items)
	s.items = len(items)
	tl.store.Splice(uint(i), 1, []*coreglib.Object{coreglib.InternObject(s.list)})
}

// splice changes s's list, its count first: a bind that runs during the
// splice finds its section by the counts (locate).
func (s *section) splice(pos, remove int, add []string) {
	s.items += len(add) - remove
	s.list.Splice(uint(pos), uint(remove), add)
}

// fold opens the sections their rows now want open, at once, and folds
// the others from an idle callback after the batch: folding a step of
// thousands of lines and opening the next in one flush cost ~6 ms (the
// view rebinds the rows that come into sight), over the 4 ms per flush the
// pump allows; apart, each stays under it.
func (w *Window) fold() {
	last := w.lastStarted()
	for _, s := range w.tl.sections {
		switch open := w.wantOpen(s, last); {
		case open:
			w.show(s, true)
		case s.lines && !w.tl.foldQueued:
			w.tl.foldQueued = true
			coreglib.IdleAdd(w.foldNow)
		}
	}
}

// foldNow folds the open sections that no longer want to be open.
func (w *Window) foldNow() {
	w.tl.foldQueued = false
	if w.closed {
		return
	}
	last := w.lastStarted()
	for _, s := range w.tl.sections {
		if s.lines && !w.wantOpen(s, last) {
			w.show(s, false)
		}
	}
}

// toggleStep is a click on a step's header: open it or fold it, for
// the rest of the run.
func (w *Window) toggleStep(s *section) {
	open := !s.lines
	s.userSet, s.userOpen = true, open
	w.show(s, open)
}

// output applies the model's grouped output diff to the sections, and
// keeps the newest line in view while following.
//
// The scroll is once per frame, not once per flush: ListView.ScrollTo
// measures the rows it brings into view right away, and a flood flushes
// about three times a frame. A tick callback scrolls to whatever is newest
// just before the frame's layout.
func (w *Window) output(c runview.Change) {
	if len(c.Steps) == 0 {
		return
	}
	last := w.lastStarted()
	for _, o := range c.Steps {
		if c.Reset && o.Remove > 0 && o.Append == nil {
			continue // lines from before the reset: the sections are new
		}
		s := w.sectionFor(o.ID)
		if k := min(o.Remove, s.n); k > 0 {
			s.n -= k
			if s.lines {
				w.outLen -= uint(k)
				s.splice(0, k, nil)
				if s.items == 0 {
					s.lines, s.summary = false, ""
					s.splice(0, 0, []string{summaryItem})
				}
			}
		}
		if len(o.Append) == 0 {
			continue
		}
		if s.lines {
			s.n += len(o.Append)
			w.outLen += uint(len(o.Append))
			s.splice(s.items, 0, o.Append)
			continue
		}
		s.n += len(o.Append)
		if w.wantOpen(s, last) {
			w.show(s, true)
		}
	}
	w.followBottom()
}

// followBottom scrolls to the newest item before the next frame, while
// following.
func (w *Window) followBottom() {
	if !w.follow || w.scrollQueued || w.tl.total() == 0 {
		return
	}
	w.scrollQueued = true
	w.outView.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		w.scrollQueued = false
		if n := w.tl.total(); w.follow && n > 0 && !w.closed {
			w.outView.ScrollTo(n-1, gtk.ListScrollNone, nil)
		}
		return false // once
	})
}

// updateSteps redraws the bound headers and the open/folded state after
// rows changed.
func (w *Window) updateSteps() {
	w.fold()
	for _, s := range w.tl.sections {
		if s.header != nil {
			w.drawHeader(s.header)
		}
		// A folded step's summary item shows its row's summary: rebind
		// it when that changed (one splice of a one-item list).
		if !s.lines {
			sum := ""
			if r, i := w.rowOf(s.step); i >= 0 {
				sum = r.Summary()
			}
			if sum != s.summary {
				s.summary = sum
				s.splice(0, 1, []string{summaryItem})
			}
		}
	}
}

// updateRunHeader redraws the run's header from the model.
func (w *Window) updateRunHeader() {
	tl, m := w.tl, w.model
	h := m.Header()
	tl.kicker.SetText(h.Kicker)
	meta := ""
	if !m.Started.IsZero() && len(m.Rows) > 0 {
		meta = "Started " + m.Started.Local().Format("15:04:05")
		if m.DryRun {
			meta += " · DRY RUN · NOTHING ON THE SYSTEM CHANGES"
		}
	}
	tl.meta.SetText(meta)
	tl.meta.SetVisible(meta != "")
	counts := ""
	if h.Of > 0 {
		counts = fmt.Sprintf("%d done · %d failed · %d skipped", h.Done, h.Failed, h.Skipped+h.Refused+h.NotRun)
	}
	tl.counts.SetText(counts)
	tl.counts.SetVisible(counts != "")
	// The segment bar: one segment per step, in its state's colour.
	for len(tl.segs) < len(m.Rows) {
		s := gtk.NewBox(gtk.OrientationHorizontal, 0)
		s.AddCSSClass("am-segment")
		tl.segments.Append(s)
		tl.segs = append(tl.segs, s)
	}
	for i, s := range tl.segs {
		s.SetVisible(i < len(m.Rows))
		if i < len(m.Rows) {
			setStateClass(&s.Widget, m.Rows[i])
		}
	}
	tl.segments.SetVisible(len(m.Rows) > 0)
	w.tickElapsed()
}

// tickElapsed shows the run's elapsed time, and keeps it ticking once a
// second while the run goes (a GLib timeout on the main thread, gone once
// the run is over).
func (w *Window) tickElapsed() {
	tl := w.tl
	d := w.model.Elapsed(time.Now())
	if d > 0 {
		tl.elapsed.SetText(fmtClock(d))
	} else {
		tl.elapsed.SetText("")
	}
	if !w.model.Running || tl.ticking {
		return
	}
	tl.ticking = true
	coreglib.TimeoutAdd(1000, func() bool {
		if w.closed || !w.model.Running {
			tl.ticking = false
			return false
		}
		tl.elapsed.SetText(fmtClock(w.model.Elapsed(time.Now())))
		return true
	})
}

// fmtClock is a duration as mm:ss (h:mm:ss past an hour).
func fmtClock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
