package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// Window is the main window: a view stack of the Overview, Run, Logs and
// Settings pages. Every method runs on the GTK main thread.
type Window struct {
	// agentNotice says why archmaid has no polkit agent of its own this run
	// (Options.Notices), shown with the polkit wait.
	agentNotice string

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
	// pkgAd, pkgRun and pkgAfter are the Packages run's state
	// (packages_run.go): the adapter between its events and the page, a
	// run going, and what to start when it is over ("Update system first"
	// asked while the sheet's answer was still on its way).
	pkgAd    pkgAdapter
	pkgRun   bool
	pkgAfter func()
	// pkgs is the Packages page (packages.go).
	pkgs  *pkgPage
	model *runview.Model
	// palette colours the output's ANSI 16: DMS's dank16, read once at
	// start, or the fallback table.
	palette runview.Palette

	// shell is the frame around the pages (shell.go).
	shell *shell
	// banner is the stub's StubBanner (nil on the real Driver).
	banner  *adw.Banner
	toasts  *adw.ToastOverlay
	stack   *adw.ViewStack
	stopBtn *gtk.Button
	status  *gtk.Label
	// tl is the Run page's timeline (timeline.go); outLen counts the
	// output lines its sections hold (not the summary items).
	tl      *timeline
	sum     *summaryView // the finished page's summary (summary.go)
	outLen  uint
	outView *gtk.ListView
	// dragStop: a drag on the output's scrollbar stops following.
	dragStop *gtk.GestureDrag
	// dry: the current run's Driver was made dry this once (startDry), so
	// a failed step's Run again repeats it dry. copied is what Copy error
	// last put on the clipboard (the harness reads it).
	dry    bool
	copied string
	// drvDry: the current Driver is a dry run (the Settings switch, or Dry
	// run first); its confirms say so up front.
	drvDry bool
	// queued: the current action's queue has started (QueueStarted, which
	// clears the Run page). lastFinished is the Run page's run's end when
	// the action started: an action that ends before its queue puts it
	// back, so the page, its header and elapsed time stay the last run's.
	queued       bool
	lastFinished *engine.Finished
	// pagePrev: the Run page shows the run before the current action (from
	// its start until its queue starts, and after it ended before one);
	// the header's "previous run" tag.
	pagePrev bool
	// ended is how the current action ended (its Finished), whether or not
	// the page shows it.
	ended *engine.Finished
	// follow: keep the newest line in view. Scrolling up stops it;
	// reaching the bottom again restarts it.
	follow bool
	// scrollQueued: a tick callback scrolls to the newest line before the
	// next frame (see output).
	scrollQueued bool

	dialogs map[engine.PromptID]*amDialog
	// pageBefore is the page Start left; nothingDialog is the open "No cache
	// to clear" popup, nothingPending that its end is awaited (nothing.go).
	pageBefore     string
	nothingDialog  *amDialog
	nothingPending bool
	// stayUntilQueue: the action's page change waits for its queue.
	stayUntilQueue bool
	// quitDialog is the open "Stop the run and quit?" dialog, or nil.
	quitDialog *amDialog
	// The latest selection's check boxes and password entry, for the
	// headless harness to tick and type into.
	checks  []*gtk.CheckButton
	pwEntry *adw.PasswordEntryRow
	// launcher opens files and folders (Options.Launcher).
	launcher Launcher
	// pwCaller is the open password dialog's caller line (the harness reads it).
	pwCaller *gtk.Label
	// busy: a Start was sent and its Finished hasn't come. quitting: the
	// user confirmed closing mid-run. closed: the window is going away;
	// late flushes are ignored.
	busy, quitting, closed bool
	closeOnce              sync.Once

	// The Overview page: the hero (its steps, Dry run first), the last
	// run's cell, the cells, the actions by ID. current is the action last
	// started.
	heroSteps *gtk.FlowBox
	dryFirst  *gtk.Button
	// narrow: the window is under the narrow breakpoint (setNarrow). The
	// Overview's widgets that follow it: the hero's kicker and its text
	// for each width, the plan's tag lines, the buttons' row, and each
	// action cell's root tag on its icon line and under its title.
	narrow      bool
	heroKicker  *gtk.Label
	kickerText  [2]string // wide, narrow
	heroLines   []*tagLine
	heroButtons *gtk.FlowBox
	cellRoots   [][2]*gtk.Label
	cells       *gtk.FlowBox
	lastIcon    *gtk.Image
	lastTitle   *gtk.Label
	lastTag     *gtk.Label
	lastIssues  *gtk.Label
	lastRun     overview.LastRun
	menu        map[string]*menuRow
	current     string
	logs        *logsPage
	// shownOverview is what the Overview's widgets show (nil: not drawn).
	shownOverview *overviewState
	// The Clear caches cell's size line (usercache.go): what was
	// measured, whether a measurement goes, whether another was asked for
	// meanwhile, and the run count that tells a stale result.
	cache cacheMeter

	// onApply, when set, sees every applied batch's Change (the harness).
	onApply func(runview.Change)
	// toasted is every toast shown, for the harness.
	toasted []string
}

func newWindow(app *adw.Application, opts Options) *Window {
	w := &Window{app: app, opts: opts, model: runview.NewModel(), palette: runview.LoadPalette(), dialogs: map[engine.PromptID]*amDialog{}}
	w.win = adw.NewApplicationWindow(&app.Application)
	w.win.AddCSSClass(scopeClass) // style.css's scope
	w.launcher = opts.Launcher
	if w.launcher == nil {
		w.launcher = gtkLauncher{parent: &w.win.Window, report: func(err error) { w.toast("Couldn't open it: " + err.Error()) }}
	}
	w.win.SetTitle(w.title())
	w.win.SetDefaultSize(960, 640)

	w.stopBtn = gtk.NewButtonWithLabel("Stop")
	w.stopBtn.AddCSSClass("destructive-action")
	w.stopBtn.SetSensitive(false)
	w.stopBtn.SetVisible(false) // only while a run goes (setBusy)
	w.stopBtn.ConnectClicked(w.stop)
	w.stack = adw.NewViewStack()

	w.status = gtk.NewLabel(w.statusText())
	w.status.SetXAlign(0)
	w.status.AddCSSClass("dim-label")

	pages := map[string]gtk.Widgetter{"overview": w.newOverview(), "packages": w.newPackages(), "run": w.newRunPage(), "logs": w.newLogs(), "settings": w.newSettings()}
	for _, p := range navPages {
		w.stack.AddTitledWithIcon(pages[p.name], p.name, p.title, p.icon)
	}
	w.stack.SetVisibleChildName("overview")
	w.stack.NotifyProperty("visible-child-name", func() {
		if w.stack.VisibleChildName() == "logs" {
			w.refreshLogs()
		}
	})
	w.buildShell()
	w.win.ConnectCloseRequest(w.closeRequest)

	if w.opts.Notices != nil {
		w.opts.Notices(func(n string) {
			post(func() {
				if w.closed {
					return
				}
				w.agentNotice = n
				w.setStatus()
			})
		})
	}
	w.newDriver(false)
	w.measureUserCache()
	return w
}

// newDriver makes the Driver (and its pump) for w.settings; dry makes
// it a dry run whatever they say (Dry run first: this Driver only, the
// saved settings untouched).
func (w *Window) newDriver(dry bool) {
	w.gen++
	gen := w.gen
	s := w.settings
	if dry {
		s.DryRun = true
	}
	w.drvDry = s.DryRun
	w.drv = w.opts.NewDriver(s.Apply(w.opts.Engine))
	po := w.opts.Pump
	po.Ended = func() {
		// A Driver the window closed (replaced, or the window closing)
		// has a newer gen or w.closed; any other stopped on its own.
		if gen == w.gen {
			w.engineStopped()
		}
	}
	w.pump = runview.NewPump(w.drv, post, func(evs []engine.Event) {
		if gen == w.gen {
			w.apply(evs)
		}
	}, po)
}

// engineStopped: the current Driver's events ended without the window
// closing it (a Session panic closes the Driver). The run, if one was
// going, is over: its dialogs close, the controls and the menu come back
// with the error shown, and the next Start makes a fresh Driver.
func (w *Window) engineStopped() {
	if w.closed {
		return
	}
	for id, d := range w.dialogs {
		delete(w.dialogs, id)
		d.ForceClose()
	}
	w.redraw(w.model.Stop())
}

// replaceDriver closes the idle Driver and makes a fresh one with the
// current settings. It waits for the old pump, which posts without
// blocking, so it never waits on this thread; an idle Driver has no child
// to stop, so its Close is quick.
func (w *Window) replaceDriver(dry bool) {
	w.drv.Close()
	<-w.pump.Done()
	w.newDriver(dry)
}

// startAction starts action id (a menu row) and shows the Run page.
func (w *Window) startAction(id string) { w.start(id, false) }

// startDry starts action id as a dry run this once (the hero's Dry run
// first): its Driver is made with dry run on; the Settings page and
// gui.json are left as they are.
func (w *Window) startDry(id string) { w.start(id, true) }

func (w *Window) start(id string, dry bool) {
	if w.busy || w.closed {
		return
	}
	// Every Start runs on a fresh Driver: one Session and one log store per
	// run, so each real run gets its own run directory, stamped now (not
	// when the window opened or the last run ended), and the Settings
	// page's changes apply to it.
	w.replaceDriver(dry)
	// The Run page is cleared when the new run's queue starts (the model
	// clears at QueueStarted, Q12), not here: until then it keeps the last
	// run, so an action that ends before any queue (a declined gate, a
	// notice) leaves it as it was (P8 row 13, Dev 5).
	w.queued, w.lastFinished, w.ended = false, w.model.Finished, nil
	w.pagePrev = len(w.model.Rows) > 0
	w.current, w.dry, w.model.Stopped, w.agentNotice = id, dry, "", ""
	w.model.StartDry = dry || w.drvDry
	w.model.Notice = "" // the last action's; this one's comes with its end
	w.updateRunHeader()
	w.setBusy(true)
	w.pageBefore, w.nothingPending = w.stack.VisibleChildName(), false
	// Clear caches stays where it was while it measures and while its pick
	// is open; the Run page comes when its queue starts (redraw, c.Reset).
	w.stayUntilQueue = strings.HasPrefix(id, actions.IDClearCaches)
	if !w.stayUntilQueue {
		w.stack.SetVisibleChildName("run")
	}
	w.updateTitle()
	w.updateOverview()
	w.send(engine.Start{Action: id})
}

// stop is the Stop button: Abort the running action.
func (w *Window) stop() {
	if w.busy {
		w.send(engine.Abort{})
	}
}

// send hands c to the current Driver, retrying on the main loop while its
// queue is full (Send never blocks). Every retry goes to that same Driver:
// once it has closed (its events ended: closed by the window, or a Session
// panic) or been replaced, or the window closes, c is dropped. Prompt IDs
// restart with every Session, so an answer carried over to the next run's
// Driver could answer that run's prompt (its gate is ID 1 too) before the
// user saw it.
//
// A dropped SubmitPassword's Secret is a Go string, which can't be zeroed
// (docs/HANDOVER.md → "The GUI password's last copy"); the entry and the
// bytes it was read into are already wiped, and send lets go of it.
func (w *Window) send(c engine.Command) {
	drv, pump, gen := w.drv, w.pump, w.gen
	tries := 0
	var try func() bool
	try = func() bool {
		if w.closed || w.gen != gen || pumpDone(pump) {
			c = nil
			return false
		}
		if drv.Send(c) {
			c = nil
			return false
		}
		if tries++; tries == 50 {
			c = nil
			w.toast("archmaid couldn't reach the engine")
			return false
		}
		return true // try again in 20 ms
	}
	if try() {
		coreglib.TimeoutAdd(20, try)
	}
}

// pumpDone: p's Driver's events have ended, so the Driver is closed.
func pumpDone(p *runview.Pump) bool {
	select {
	case <-p.Done():
		return true
	default:
		return false
	}
}

func (w *Window) toast(msg string) {
	w.toasted = append(w.toasted, msg)
	w.toasts.AddToast(plainToast(msg))
}

// plainToast is a toast showing msg as plain text. AdwToast parses its
// title as markup by default, and a toast's message can carry an error's
// text or a path: a "<" or "&" in it would garble the toast or fake markup.
func plainToast(msg string) *adw.Toast {
	t := adw.NewToast("")
	t.SetUseMarkup(false)
	t.SetTitle(msg)
	return t
}

// apply is the pump's flush: fold the batch into the model and redraw
// exactly what changed.
func (w *Window) apply(evs []engine.Event) {
	if w.closed {
		return
	}
	evs, after := w.pkgTake(evs)
	w.redraw(w.model.Apply(w.takeNothing(evs)))
	after()
}

// redraw redraws exactly what c says changed.
func (w *Window) redraw(c runview.Change) {
	if c.Opened != nil && c.Opened.Report != nil && !w.queued && w.current != "" {
		// A report (the AUR audit) is the action's own result, with no
		// queue: its page is its own, not the last run's tagged "previous".
		r := w.model.ShowReport(w.current)
		c.Reset, c.Inserted, c.Status = r.Reset, nil, true
		c.Steps, c.OutTrim, c.OutRemove, c.OutAppend = r.Steps, r.OutTrim, r.OutRemove, r.OutAppend
	}
	if c.Reset {
		// Only QueueStarted resets the model: the run's queue started.
		w.queued, w.pagePrev = true, false
		if w.stayUntilQueue {
			w.stayUntilQueue = false
			w.stack.SetVisibleChildName("run")
		}
		w.rebuild()
		// A queue run follows its newest line; a discovery result is a
		// finished page that opens at its top, header in view.
		w.follow = w.model.Discovery == nil
		if !w.follow {
			w.scrollTop()
		}
	}
	for _, i := range c.Inserted {
		w.insert(i)
	}
	if c.Reset || len(c.Inserted) > 0 || len(c.Rows) > 0 {
		w.updateSteps()
		w.updateRunBadge()
	}
	w.output(c)
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
		f := w.model.Finished
		w.ended = f
		if !w.queued {
			// It ended before its queue (a declined gate, a notice): the
			// page still shows the last run, so its header does too, not
			// this attempt's outcome and the time its gate was open. The
			// toast says how the attempt ended. The page title names
			// the page's run too, not the attempted action, and the header
			// is tagged "previous run".
			w.model.Finished = w.lastFinished
			if w.pagePrev && w.model.Action != "" {
				w.current = w.model.Action
				w.updateTitle()
			}
		}
		w.setBusy(false)
		w.refreshLastRun()
		w.invalidateUserCache()
		w.reloadPackages() // topgrade, a Full Cleanup or a repair changed the packages
		switch {
		case w.nothingPending && f != nil && f.Outcome == engine.OutcomeNotice:
			w.showNothing()
		case f != nil:
			w.toast(w.model.EndToast(*f))
		}
		w.nothingPending = false
	}
	if c.Reset || len(c.Rows) > 0 || c.Status || c.Finished {
		w.updateRunHeader()
	}
	if c.Reset || c.Report || c.Finished {
		w.updateSummary()
	}
	if c.Status || c.Finished {
		w.setStatus()
		w.shell.meters.show(w.model.Stat.Status)
		w.updateOverview()
		w.updateRunBadge()
	}
	if w.onApply != nil {
		w.onApply(c)
	}
}

// title is the window's title: "(stub)" marks a demo.
func (w *Window) title() string {
	if w.opts.Stub {
		return "archmaid (stub)"
	}
	return "archmaid"
}

func (w *Window) statusText() string {
	m := w.model
	var parts []string
	if w.opts.Stub {
		parts = append(parts, "STUB")
	}
	// before: this action ended before its queue started (a declined
	// gate, a notice), so the page, its header and m.Finished are still
	// the last run's, tagged "previous run". The status line says this
	// action, not the page: the last run's "Finished: completed" and line
	// count read as if that run had just ended (Dev 5's VM round b6, a
	// cancelled gate at 380 px).
	before := w.ended != nil && !w.queued
	dry := m.DryRun
	if before {
		dry = m.StartDry
	}
	switch {
	case m.Stopped != "":
		parts = append(parts, m.Stopped)
	case m.Notice != "" && !m.Running:
		// The action ended in a notice (what it found, or why it couldn't
		// look). A discovery's ends it before any queue, so the page and
		// m.Finished are still the last run's: the notice is what this
		// action said, and it stays until the next one starts.
		parts = append(parts, runview.Shorten(m.Notice, runview.ShortMax))
	case before:
		// How it ended, as its toast says ("Run cancelled, nothing
		// ran"), until the next Start, as a notice stays.
		parts = append(parts, m.EndToast(*w.ended))
	case m.Finished != nil && (w.queued || w.ended != nil):
		// Only this action's end: until its queue starts, m.Finished is
		// still the previous run's (QueueStarted clears it).
		status, _ := m.FinishedText(*m.Finished)
		parts = append(parts, status)
		if n := m.Finished.NotRun; n > 0 {
			parts = append(parts, fmt.Sprintf("%d not run", n))
		}
	case m.Prompt != nil && (!w.queued || m.Prompt.Confirm != nil):
		// A gate, the up-front password or a pick, before the queue
		// starts, or a step's confirm in the queue. Before the busy line:
		// a discovery's Busy ("Measuring the caches…") stays set while its
		// pick waits for the user.
		parts = append(parts, "Waiting…")
	case m.Busy != "":
		parts = append(parts, m.Busy)
	case m.Auth != nil && m.Auth.Kind == engine.WaitPolkit:
		s := "Waiting for authorisation"
		if w.agentNotice != "" {
			s += " (" + w.agentNotice + ")"
		}
		parts = append(parts, s)
	case m.Phase == engine.PhaseRunning:
		parts = append(parts, "Running")
	case m.Phase == engine.PhaseProbing:
		parts = append(parts, "Checking privileges")
	default:
		parts = append(parts, "Ready")
	}
	if dry {
		parts = append(parts, "DRY RUN")
	}
	// The page's counts are the last run's when the action ended before
	// its queue: not this action's (as the audit's report doesn't carry
	// the run before it, TestGUIAuditRerun).
	// Likewise between Start and the queue (discovery, a gate, the password):
	// the page still holds the last run's counts.
	stale := before || (!w.queued && w.pagePrev)
	if m.Lines > 0 && !stale {
		parts = append(parts, plural.N(m.Lines, "line", "lines"))
	}
	if m.Dropped > 0 && !stale {
		parts = append(parts, fmt.Sprintf("%d not shown (in the log)", m.Dropped))
	}
	if s := m.Stat.Status; s.Free != "" {
		parts = append(parts, "free "+s.Free)
	}
	return strings.Join(parts, " · ")
}

// polkitWaiting: a helper step's pkexec waits for polkit's dialog.
func (w *Window) polkitWaiting() bool {
	return w.model.Auth != nil && w.model.Auth.Kind == engine.WaitPolkit
}

// closeRequest: closing mid-run asks first (but not while polkit's dialog
// is up, see polkitWaiting); then the window hides while the Driver closes
// off the main thread (a real Driver may take its kill grace to stop a
// child), and is destroyed once it has.
func (w *Window) closeRequest() bool {
	if w.closed {
		return true
	}
	if w.busy && !w.quitting && w.polkitWaiting() {
		// pkexec waits for polkit's dialog, which covers our window: a
		// question would open under it. Nothing of this step runs as
		// root yet, so the X is Stop and Quit at once: the run is
		// aborted and recorded, and the waiting pkexec is killed (its
		// dialog goes with it).
		w.quitting = true
	}
	if w.busy && !w.quitting {
		if w.quitDialog != nil {
			return true
		}
		d := alertDialog(adw.NewAlertDialog("Stop the run and quit?", "The running step is stopped; what it already changed stays changed."))
		d.AddResponse("cancel", "Keep Running")
		d.AddResponse("quit", "Stop and Quit")
		d.SetResponseAppearance("quit", adw.ResponseDestructive)
		d.SetDefaultResponse("cancel")
		d.SetCloseResponse("cancel")
		// Act on the response, never on the dialog's closed signal: a
		// clicked button closes the dialog first and emits its response
		// after (libadwaita's AdwAlertDialog), so at "closed" the answer
		// isn't known yet; a close by Escape or by the window emits the
		// close response, "cancel". The window is then closed directly
		// (closeRequest), not through win.Close, which libadwaita may
		// still turn into closing a dialog. Every response comes exactly
		// once, the run over or not: Stop and Quit always quits.
		d.ConnectResponse(func(resp string) {
			if w.quitDialog == d {
				w.quitDialog = nil
			}
			if resp == "quit" && !w.closed {
				w.quitting = true
				w.closeRequest()
			}
		})
		w.quitDialog = d
		d.Present(w.win)
		return true
	}
	w.closed = true
	w.pkgs.closeSheet() // the Packages sheet is not in w.dialogs
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
