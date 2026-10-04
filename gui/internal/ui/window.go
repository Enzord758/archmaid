package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
)

// Window is the main window with its Run page. Every method runs on the
// GTK main thread.
type Window struct {
	app  *adw.Application
	win  *adw.ApplicationWindow
	opts Options

	drv   engine.Driver
	pump  *runview.Pump
	model *runview.Model
	// palette colours the output's ANSI 16: DMS's dank16, read once at
	// start, or the fallback table.
	palette runview.Palette

	toasts  *adw.ToastOverlay
	runBtn  *gtk.Button
	stopBtn *gtk.Button
	status  *gtk.Label
	steps   *gtk.ListBox
	rows    []stepRow
	out     *gtk.StringList
	outLen  uint
	outView *gtk.ListView
	// follow: keep the newest line in view. Scrolling up stops it;
	// reaching the bottom again restarts it.
	follow bool

	dialogs map[engine.PromptID]*adw.AlertDialog
	// quitDialog is the open "Stop the run and quit?" dialog, or nil.
	quitDialog *adw.AlertDialog
	// The latest selection's check boxes and password entry, for the
	// headless harness to tick and type into.
	checks  []*gtk.CheckButton
	pwEntry *adw.PasswordEntryRow
	// busy: a Start was sent and its Finished hasn't come. quitting: the
	// user confirmed closing mid-run. closed: the window is going away;
	// late flushes are ignored.
	busy, quitting, closed bool
	closeOnce              sync.Once

	// onApply, when set, sees every applied batch's Change (the harness).
	onApply func(runview.Change)
	// toasted is every toast shown, for the harness.
	toasted []string
}

type stepRow struct {
	row  *adw.ActionRow
	icon *gtk.Image
	spin *adw.Spinner
}

func newWindow(app *adw.Application, opts Options) *Window {
	w := &Window{app: app, opts: opts, model: runview.NewModel(), palette: runview.LoadPalette(), dialogs: map[engine.PromptID]*adw.AlertDialog{}}
	w.win = adw.NewApplicationWindow(&app.Application)
	w.win.SetTitle("archmaid")
	w.win.SetDefaultSize(960, 640)

	w.runBtn = gtk.NewButtonWithLabel("Run Full Cleanup")
	w.runBtn.AddCSSClass("suggested-action")
	w.runBtn.ConnectClicked(w.start)
	w.stopBtn = gtk.NewButtonWithLabel("Stop")
	w.stopBtn.AddCSSClass("destructive-action")
	w.stopBtn.SetSensitive(false)
	w.stopBtn.ConnectClicked(w.stop)
	header := adw.NewHeaderBar()
	header.PackStart(w.runBtn)
	header.PackEnd(w.stopBtn)

	w.steps = gtk.NewListBox()
	w.steps.AddCSSClass("boxed-list")
	w.steps.SetSelectionMode(gtk.SelectionNone)
	stepsScroll := gtk.NewScrolledWindow()
	stepsScroll.SetChild(w.steps)
	stepsScroll.SetSizeRequest(320, -1)

	w.out = gtk.NewStringList(nil)
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		label := gtk.NewLabel("")
		label.SetXAlign(0)
		obj.Cast().(*gtk.ListItem).SetChild(label)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		label := item.Child().(*gtk.Label)
		text := item.Item().Cast().(*gtk.StringObject).String()
		label.SetText(text)
		// The list mirrors the model's view line for line (apply keeps
		// them in step), so the colour spans are found by position; the
		// text check guards that.
		var attrs *pango.AttrList
		if l, ok := w.model.ViewLine(int(item.Position())); ok && l.Text == text && len(l.Spans) > 0 {
			attrs = pango.AttrListFromString(l.Attrs(&w.palette))
		}
		label.SetAttributes(attrs)
	})
	w.outView = gtk.NewListView(gtk.NewNoSelection(w.out), &factory.ListItemFactory)
	w.outView.AddCSSClass("monospace")
	outScroll := gtk.NewScrolledWindow()
	outScroll.SetChild(w.outView)
	outScroll.SetVExpand(true)
	outScroll.SetHExpand(true)
	w.follow = true
	wheel := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	wheel.ConnectScroll(func(_, dy float64) bool {
		if dy < 0 {
			w.follow = false
		}
		return false
	})
	outScroll.AddController(wheel)
	outScroll.ConnectEdgeReached(func(pos gtk.PositionType) {
		if pos == gtk.PosBottom {
			w.follow = true
		}
	})
	// TODO(P6b-4): dragging the scrollbar or the keyboard should stop
	// following too.

	paned := gtk.NewPaned(gtk.OrientationHorizontal)
	paned.SetStartChild(stepsScroll)
	paned.SetEndChild(outScroll)
	paned.SetPosition(340)
	paned.SetShrinkStartChild(false)

	w.status = gtk.NewLabel("Ready")
	w.status.SetXAlign(0)
	w.status.AddCSSClass("dim-label")
	w.status.SetMarginStart(12)

	view := adw.NewToolbarView()
	view.AddTopBar(header)
	view.SetContent(paned)
	view.AddBottomBar(w.status)
	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(view)
	w.win.SetContent(w.toasts)
	w.win.ConnectCloseRequest(w.closeRequest)

	w.drv = opts.NewDriver()
	w.pump = runview.NewPump(w.drv, post, w.apply, opts.Pump)
	return w
}

// start sends the Run button's action.
func (w *Window) start() {
	if w.busy || w.closed {
		return
	}
	w.busy = true
	w.runBtn.SetSensitive(false)
	w.stopBtn.SetSensitive(true)
	w.send(engine.Start{Action: w.opts.Action})
}

// stop is the Stop button: Abort the running action.
func (w *Window) stop() {
	if w.busy {
		w.send(engine.Abort{})
	}
}

// send hands c to the Driver, retrying on the main loop while its queue is
// full (Send never blocks).
func (w *Window) send(c engine.Command) {
	tries := 0
	var try func() bool
	try = func() bool {
		if w.closed || w.drv.Send(c) {
			return false
		}
		if tries++; tries == 50 {
			w.toast("archmaid couldn't reach the engine")
			return false
		}
		return true // try again in 20 ms
	}
	if try() {
		coreglib.TimeoutAdd(20, try)
	}
}

func (w *Window) toast(msg string) {
	w.toasted = append(w.toasted, msg)
	w.toasts.AddToast(adw.NewToast(msg))
}

// apply is the pump's flush: fold the batch into the model and redraw
// exactly what changed.
func (w *Window) apply(evs []engine.Event) {
	if w.closed {
		return
	}
	c := w.model.Apply(evs)
	if c.Reset {
		w.steps.RemoveAll()
		w.rows = w.rows[:0]
		for i := range w.model.Rows {
			w.rows = append(w.rows, w.newRow())
			w.steps.Append(w.rows[i].row)
			w.updateRow(i)
		}
	}
	for _, i := range c.Inserted {
		r := w.newRow()
		w.rows = append(w.rows[:i], append([]stepRow{r}, w.rows[i:]...)...)
		w.steps.Insert(r.row, i)
		w.updateRow(i)
	}
	for _, i := range c.Rows {
		w.updateRow(i)
	}
	w.output(c.OutRemove, c.OutAppend)
	for _, id := range c.Closed {
		if d, ok := w.dialogs[id]; ok {
			delete(w.dialogs, id)
			d.ForceClose()
		}
	}
	if c.Opened != nil {
		w.prompt(*c.Opened)
	}
	for _, t := range c.Toasts {
		w.toast(t)
	}
	if c.Finished {
		w.busy = false
		w.runBtn.SetSensitive(true)
		w.stopBtn.SetSensitive(false)
		if f := w.model.Finished; f != nil {
			w.toast("Run " + f.Outcome.String())
		}
	}
	if c.Status {
		w.status.SetText(w.statusText())
	}
	if w.onApply != nil {
		w.onApply(c)
	}
}

func (w *Window) newRow() stepRow {
	r := stepRow{row: adw.NewActionRow(), icon: gtk.NewImage(), spin: adw.NewSpinner()}
	r.row.SetUseMarkup(false)
	r.row.AddPrefix(r.icon)
	r.row.AddPrefix(r.spin)
	return r
}

func (w *Window) updateRow(i int) {
	if i < 0 || i >= len(w.rows) || i >= len(w.model.Rows) {
		return
	}
	m, r := w.model.Rows[i], w.rows[i]
	r.row.SetTitle(m.Title)
	r.row.SetSubtitle(m.Subtitle())
	icon := m.State.Icon()
	r.spin.SetVisible(icon == "")
	r.icon.SetVisible(icon != "")
	if icon != "" {
		r.icon.SetFromIconName(icon)
	}
}

// output applies the model's output diff to the list and keeps the newest
// line in view while following.
func (w *Window) output(remove int, add []string) {
	if remove == 0 && len(add) == 0 {
		return
	}
	if remove > 0 {
		w.out.Splice(0, uint(remove), nil)
		w.outLen -= uint(remove)
	}
	if len(add) > 0 {
		w.out.Splice(w.outLen, 0, add)
		w.outLen += uint(len(add))
	}
	if w.follow && w.outLen > 0 {
		w.outView.ScrollTo(w.outLen-1, gtk.ListScrollNone, nil)
	}
}

func (w *Window) statusText() string {
	m := w.model
	var parts []string
	switch {
	case m.Finished != nil:
		parts = append(parts, "Finished: "+m.Finished.Outcome.String())
		if n := m.Finished.NotRun; n > 0 {
			parts = append(parts, fmt.Sprintf("%d not run", n))
		}
	case m.Busy != "":
		parts = append(parts, m.Busy)
	case m.Phase == engine.PhaseRunning:
		parts = append(parts, "Running")
	case m.Phase == engine.PhaseProbing:
		parts = append(parts, "Checking privileges")
	default:
		parts = append(parts, "Ready")
	}
	if m.DryRun {
		parts = append(parts, "DRY RUN")
	}
	if m.Lines > 0 {
		parts = append(parts, fmt.Sprintf("%d lines", m.Lines))
	}
	if m.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d not shown (in the log)", m.Dropped))
	}
	if s := m.Stat.Status; s.Free != "" {
		parts = append(parts, "free "+s.Free)
	}
	return strings.Join(parts, " · ")
}

// closeRequest: closing mid-run asks first; then the window hides while
// the Driver closes off the main thread (a real Driver may take its kill
// grace to stop a child), and is destroyed once it has.
func (w *Window) closeRequest() bool {
	if w.closed {
		return true
	}
	if w.busy && !w.quitting {
		if w.quitDialog != nil {
			return true
		}
		d := adw.NewAlertDialog("Stop the run and quit?", "The running step is stopped; what it already changed stays changed.")
		d.AddResponse("cancel", "Keep Running")
		d.AddResponse("quit", "Stop and Quit")
		d.SetResponseAppearance("quit", adw.ResponseDestructive)
		d.SetDefaultResponse("cancel")
		d.SetCloseResponse("cancel")
		d.ConnectResponse(func(resp string) {
			w.quitting = resp == "quit"
		})
		// Close the window once the dialog is gone: while a dialog is up,
		// libadwaita turns the window's close into closing the dialog.
		d.ConnectClosed(func() {
			w.quitDialog = nil
			if w.quitting {
				w.win.Close()
			}
		})
		w.quitDialog = d
		d.Present(w.win)
		return true
	}
	w.closed = true
	for id, d := range w.dialogs {
		delete(w.dialogs, id)
		d.ForceClose()
	}
	w.win.SetVisible(false)
	go func() {
		w.closeDriver()
		post(w.win.Destroy)
	}()
	return true
}

// closeDriver closes the Driver and waits for the pump to drain; safe to
// call more than once and from any goroutine.
func (w *Window) closeDriver() {
	w.closeOnce.Do(func() {
		w.drv.Close()
		<-w.pump.Done()
	})
}
