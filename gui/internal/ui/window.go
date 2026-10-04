package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/engine"
)

// Window is the main window: a view stack of the Overview, Run, Logs and
// Settings pages. Every method runs on the GTK main thread.
type Window struct {
	app  *adw.Application
	win  *adw.ApplicationWindow
	opts Options

	drv  engine.Driver
	pump *runview.Pump
	// gen counts the Drivers made; a flush from a replaced one's pump
	// (already posted when it closed) is dropped.
	gen int
	// settings are the Settings page's (saved, or the defaults); the
	// next Start's fresh Driver is made with them.
	settings settings.Settings
	set      *settingsPage
	model    *runview.Model
	// palette colours the output's ANSI 16: DMS's dank16, read once at
	// start, or the fallback table.
	palette runview.Palette

	toasts  *adw.ToastOverlay
	stack   *adw.ViewStack
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
	// scrollQueued: a tick callback scrolls to the newest line before the
	// next frame (see output).
	scrollQueued bool

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

	// The Overview page: the status header, the last run, the menu rows by
	// action ID. current is the action last started.
	statLabel *gtk.Label
	lastRow   *adw.ActionRow
	lastIcon  *gtk.Image
	lastRun   overview.LastRun
	menu      map[string]*menuRow
	current   string
	logs      *logsPage
	// shownOverview is what the Overview's widgets show (nil: not drawn).
	shownOverview *overviewState

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

	w.stopBtn = gtk.NewButtonWithLabel("Stop")
	w.stopBtn.AddCSSClass("destructive-action")
	w.stopBtn.SetSensitive(false)
	w.stopBtn.ConnectClicked(w.stop)
	w.stack = adw.NewViewStack()
	switcher := adw.NewViewSwitcher()
	switcher.SetStack(w.stack)
	switcher.SetPolicy(adw.ViewSwitcherPolicyWide)
	header := adw.NewHeaderBar()
	header.SetTitleWidget(switcher)
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

	w.stack.AddTitledWithIcon(w.newOverview(), "overview", "Overview", "go-home-symbolic")
	w.stack.AddTitledWithIcon(paned, "run", "Run", "media-playback-start-symbolic")
	w.stack.AddTitledWithIcon(w.newLogs(), "logs", "Logs", "document-open-recent-symbolic")
	w.stack.AddTitledWithIcon(w.newSettings(), "settings", "Settings", "emblem-system-symbolic")
	w.stack.SetVisibleChildName("overview")
	w.stack.NotifyProperty("visible-child-name", func() {
		if w.stack.VisibleChildName() == "logs" {
			w.refreshLogs()
		}
	})

	view := adw.NewToolbarView()
	view.AddTopBar(header)
	view.SetContent(w.stack)
	view.AddBottomBar(w.status)
	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(view)
	w.win.SetContent(w.toasts)
	w.win.ConnectCloseRequest(w.closeRequest)

	w.newDriver()
	return w
}

// newDriver makes the Driver (and its pump) for w.settings.
func (w *Window) newDriver() {
	w.gen++
	gen := w.gen
	w.drv = w.opts.NewDriver(w.settings.Apply(w.opts.Engine))
	w.pump = runview.NewPump(w.drv, post, func(evs []engine.Event) {
		if gen == w.gen {
			w.apply(evs)
		}
	}, w.opts.Pump)
}

// replaceDriver closes the idle Driver and makes a fresh one with the
// current settings. It waits for the old pump, which posts without
// blocking, so it never waits on this thread; an idle Driver has no child
// to stop, so its Close is quick.
func (w *Window) replaceDriver() {
	w.drv.Close()
	<-w.pump.Done()
	w.newDriver()
}

// startAction starts action id (a menu row) and shows the Run page.
func (w *Window) startAction(id string) {
	if w.busy || w.closed {
		return
	}
	// Every Start runs on a fresh Driver: one Session and one log store per
	// run, so each real run gets its own run directory, stamped now (not
	// when the window opened or the last run ended), and the Settings
	// page's changes apply to it.
	w.replaceDriver()
	w.busy, w.current = true, id
	w.stopBtn.SetSensitive(true)
	w.stack.SetVisibleChildName("run")
	w.updateOverview()
	w.send(engine.Start{Action: id})
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
		w.stopBtn.SetSensitive(false)
		w.refreshLastRun()
		if f := w.model.Finished; f != nil {
			w.toast("Run " + f.Outcome.String())
		}
	}
	if c.Status || c.Finished {
		w.status.SetText(w.statusText())
		w.updateOverview()
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
//
// The scroll is once per frame, not once per flush: ListView.ScrollTo
// measures the rows it brings into view right away (for the colour flood,
// multi-run Pango layouts: ~2.8 ms of a ~3.4 ms flush), and a flood flushes
// about three times a frame. A tick callback scrolls to whatever is newest
// just before the frame's layout, so the rows are measured once, for the
// frame that draws them.
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
	if w.follow && w.outLen > 0 && !w.scrollQueued {
		w.scrollQueued = true
		w.outView.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
			w.scrollQueued = false
			if w.follow && w.outLen > 0 && !w.closed {
				w.outView.ScrollTo(w.outLen-1, gtk.ListScrollNone, nil)
			}
			return false // once
		})
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
	case m.Auth != nil && m.Auth.Kind == engine.WaitPolkit:
		parts = append(parts, "Waiting for authorisation")
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
