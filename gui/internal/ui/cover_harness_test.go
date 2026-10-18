package ui

// Coverage scenarios: small headless children (the harness's runChild with
// a "cov-" scenario name) that drive one area of the window through its
// real widgets and signals and assert the outcome inside the child. Each
// check that fails lands in the report's Errors, which child() turns into a
// test failure. They add nothing to the other scenarios' timing: the window
// is up after 200 ms and the scenario runs, then closes it.
//
// A scenario gets the window and a few helpers (covEnv); covOptions may
// change the Options the window is made with (the launcher, the Driver, the
// notices hook). Events are fed through w.apply, the pump's own entry, so
// the model and every redraw run as they do for a real Driver's.

import (
	"slices"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// covEnv is what a cover scenario works with.
type covEnv struct {
	w    *Window
	rep  *report
	fail func(string, ...any)
}

var (
	covScenarios = map[string]func(*covEnv){}
	covOptions   = map[string]func(*Options, *report){}
)

// ok fails the scenario with the message when cond is false.
func (c *covEnv) ok(cond bool, format string, a ...any) {
	if !cond {
		c.fail(format, a...)
	}
}

// sent is the answers the Driver got so far.
func (c *covEnv) sent() []string { return slices.Clone(c.rep.Sent) }

// finish closes the window shortly (after the checks of the last step).
func (c *covEnv) finish() { coreglib.TimeoutAdd(50, func() { c.w.win.Close() }) }

// quiet takes the harness's own reactions off the window (it answers every
// prompt and ends the scenario at a Finished): the scenario drives those.
func (c *covEnv) quiet() { c.w.onApply = nil }

// seq runs the steps one after another, gap ms apart, and finishes.
func (c *covEnv) seq(gap uint, steps ...func()) {
	var run func(i int)
	run = func(i int) {
		if i == len(steps) {
			c.finish()
			return
		}
		coreglib.TimeoutAdd(gap, func() {
			steps[i]()
			run(i + 1)
		})
	}
	run(0)
}

// feed applies events as the pump would.
func (c *covEnv) feed(evs ...engine.Event) { c.w.apply(evs) }

// press clicks a dialog's response button as a user does.
func (c *covEnv) press(d *amDialog, resp string) {
	if b := findButton(&d.Widget, d.ResponseLabel(resp)); b != nil {
		b.Emit("clicked")
		return
	}
	c.fail("dialog %q has no %q button", d.Heading(), resp)
}

// eachWidget walks root and everything under it.
func eachWidget(root gtk.Widgetter, f func(gtk.Widgetter)) {
	f(root)
	for ch := gtk.BaseWidget(root).FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
		eachWidget(ch, f)
	}
}

// findTooltip is the widget under root with that tooltip, or nil.
func findTooltip(root gtk.Widgetter, tip string) gtk.Widgetter {
	var found gtk.Widgetter
	eachWidget(root, func(w gtk.Widgetter) {
		if found == nil && gtk.BaseWidget(w).TooltipText() == tip {
			found = w
		}
	})
	return found
}

// controllersOf are the event controllers of a widget.
func controllersOf(w gtk.Widgetter) []*coreglib.Object {
	m := gtk.BaseWidget(w).ObserveControllers()
	var out []*coreglib.Object
	for i := uint(0); i < m.NItems(); i++ {
		out = append(out, m.Item(i))
	}
	return out
}

// failLauncher is a Launcher whose opens fail.
type failLauncher struct {
	recordLauncher
	err error
}

func (f *failLauncher) OpenFile(path string) error   { f.add("file " + path); return f.err }
func (f *failLauncher) OpenFolder(path string) error { f.add("folder " + path); return f.err }

// refuseDriver is a Driver whose queue never takes a command.
type refuseDriver struct{ engine.Driver }

func (refuseDriver) Send(engine.Command) bool { return false }

func covChild(t *testing.T, scenario string, env ...string) *report {
	t.Helper()
	needDisplay(t)
	rep, _ := child(t, scenario, env...)
	return rep
}

// ---- dialogs ----

func init() { covScenarios["cov-dialogs"] = covDialogs }

// covDialogs: the dialogs' less-travelled paths. A confirm with only a
// question, the default-yes one, a plain item list, a prompt the model
// doesn't hold (its answer goes nowhere), the engine withdrawing a dialog,
// Enter in the password entry (empty: nothing; typed: the answer), a
// selection with no title and its Cancel, and the window closing under an
// open dialog.
func covDialogs(c *covEnv) {
	w := c.w
	c.quiet()
	var d *amDialog
	dialog := func(id engine.PromptID) *amDialog {
		d = w.dialogs[id]
		c.ok(d != nil, "prompt %d has no dialog", id)
		return d
	}
	c.seq(150,
		func() { // a prompt with nothing to ask opens nothing
			w.prompt(runview.Prompt{ID: 90})
			c.ok(len(w.dialogs) == 0, "an empty prompt opened %d dialogs", len(w.dialogs))
			c.feed(engine.NeedsConfirm{ID: 91, Msg: "Remove the old cache?"})
		},
		func() { // the question alone is the heading; no is the default
			if dialog(91) == nil {
				return
			}
			c.ok(d.Heading() == "Remove the old cache?", "heading %q", d.Heading())
			c.ok(d.DefaultResponse() == "no", "default response %q, want no", d.DefaultResponse())
			c.press(d, "no")
		},
		func() {
			c.ok(slices.Contains(c.sent(), "confirm 91 false"), "sent %q, want the refusal of 91", c.sent())
			c.feed(engine.NeedsConfirm{ID: 92, Title: "Go on?", Default: true, YesLabel: "Proceed"})
		},
		func() { // Default makes yes the default and the suggested action
			if dialog(92) == nil {
				return
			}
			c.ok(d.DefaultResponse() == "yes", "default response %q, want yes", d.DefaultResponse())
			c.ok(d.ResponseAppearance("yes") == adw.ResponseSuggested, "yes is not suggested")
			c.ok(d.ResponseLabel("yes") == "Proceed", "yes label %q", d.ResponseLabel("yes"))
			c.press(d, "yes")
		},
		func() {
			c.ok(slices.Contains(c.sent(), "confirm 92 true"), "sent %q, want the yes of 92", c.sent())
			c.feed(engine.NeedsConfirm{ID: 93, Title: "Delete these?", Items: []string{"one.txt", "two.txt"}})
		},
		func() { // a plain list of what the step acts on
			if dialog(93) == nil {
				return
			}
			c.ok(strings.Contains(dialogLabels(d, nil), "one.txt\ntwo.txt"), "dialog text %q lacks the items", dialogLabels(d, nil))
			c.press(d, "no")
			// A dialog the model doesn't know is answered into nothing.
			w.prompt(runview.Prompt{ID: 95, Confirm: &engine.NeedsConfirm{ID: 95, Title: "Orphan"}})
		},
		func() {
			if dialog(95) == nil {
				return
			}
			before := c.sent()
			c.press(d, "yes")
			c.ok(len(w.dialogs) == 0 || w.dialogs[95] == nil, "the answered dialog 95 is still held")
			c.ok(slices.Equal(c.sent(), before), "an answer to a prompt the model doesn't hold was sent: %q", c.sent())
			c.feed(engine.NeedsConfirm{ID: 94, Title: "Withdrawn"})
		},
		func() { // the engine withdraws it
			c.ok(w.dialogs[94] != nil, "no dialog for 94")
			c.feed(engine.PromptClosed{ID: 94})
			c.ok(w.dialogs[94] == nil, "the withdrawn dialog 94 is still held")
			c.ok(w.model.Prompt == nil, "the model still holds the prompt")
			c.feed(engine.NeedsPassword{ID: 96, Kind: engine.PasswordUpFront})
		},
		func() { // Enter on an empty entry does nothing; typed, it answers
			if dialog(96) == nil || w.pwEntry == nil {
				c.fail("no password entry")
				return
			}
			entry := w.pwEntry
			entry.Emit("entry-activated")
			c.ok(w.dialogs[96] != nil, "Enter on an empty password answered")
			c.ok(!slices.ContainsFunc(c.sent(), func(s string) bool { return strings.HasPrefix(s, "password 96") }), "an empty password was sent: %q", c.sent())
			entry.SetText("secret")
			entry.Emit("entry-activated")
			c.ok(slices.Contains(c.sent(), "password 96 len 6"), "sent %q, want the 6 typed characters", c.sent())
			c.ok(w.dialogs[96] == nil && w.pwEntry == nil, "the answered password dialog is still held")
			c.ok(entry.TextLength() == 0, "the password entry was not wiped")
			c.feed(engine.NeedsSelection{ID: 97, Items: []string{"x", "y"}})
		},
		func() { // a selection without a title, cancelled
			if dialog(97) == nil {
				return
			}
			c.ok(d.Heading() == "Choose", "heading %q, want Choose", d.Heading())
			c.press(d, "cancel")
		},
		func() {
			c.ok(slices.Contains(c.sent(), "cancel 97"), "sent %q, want the cancel of 97", c.sent())
			c.feed(engine.NeedsSelection{ID: 99, Title: "Clear caches", Items: []string{"x", "y"}, Go: "clear",
				Rows: []actions.PickRow{{Label: "x"}, {Label: "y"}}})
		},
		func() { // a destructive pick: Clear is drawn destructive and is not the default
			if dialog(99) == nil {
				return
			}
			c.ok(d.ResponseLabel("ok") == "Clear" && d.ResponseAppearance("ok") == adw.ResponseDestructive, "ok is %q", d.ResponseLabel("ok"))
			c.ok(d.DefaultResponse() == "cancel", "default response %q, want cancel", d.DefaultResponse())
			c.press(d, "cancel")
		},
		func() {
			c.feed(engine.NeedsConfirm{ID: 98, Title: "Still open"})
		},
		func() { // closing the window takes its open dialogs with it
			c.ok(w.dialogs[98] != nil, "no dialog for 98")
			w.closeRequest()
			c.ok(w.closed && len(w.dialogs) == 0, "closed %v, dialogs held %d", w.closed, len(w.dialogs))
		},
	)
}

// ---- quit dialog and signals ----

func init() { covScenarios["cov-quit"] = covQuit }

// covQuit: the close question is asked once; a signal's quit takes it down
// and closes the window.
func covQuit(c *covEnv) {
	w := c.w
	c.quiet()
	c.seq(150,
		func() {
			w.busy = true // as mid-run: the question is asked
			w.closeRequest()
			d := w.quitDialog
			c.ok(d != nil, "closing mid-run opened no question")
			c.ok(w.closeRequest(), "a second close request wasn't claimed")
			c.ok(w.quitDialog == d, "a second question was opened")
		},
		func() {
			w.quitOnSignal()
			c.ok(w.quitDialog == nil, "the question stayed up after a signal")
			c.ok(w.closed && w.quitting, "closed %v quitting %v after a signal", w.closed, w.quitting)
			w.busy = false
		},
	)
}

// ---- window ----

func init() {
	covScenarios["cov-window"] = covWindow
	covOptions["cov-window"] = func(o *Options, rep *report) {
		o.Launcher = nil // the real one
		notices := func(set func(string)) { covNotice = set }
		o.Notices = notices
		orig := o.NewDriver
		o.NewDriver = func(e engine.Options) engine.Driver { return refuseDriver{orig(e)} }
	}
}

var covNotice func(string)

// covWindow: the default Launcher; a command the engine never takes is
// given up on with a toast; a notice that arrives after the window closed
// is dropped; the Overview's cell says what a discovery is doing even when
// the model has no wording.
func covWindow(c *covEnv) {
	w := c.w
	c.quiet()
	_, real := w.launcher.(gtkLauncher)
	c.ok(real, "launcher %T, want the real one when none is given", w.launcher)
	c.ok(covNotice != nil, "the window didn't register for notices")

	// A discovery running with no text of its own shows the action's.
	w.current, w.busy = actions.IDRepair, true
	c.feed(engine.PhaseChanged{Phase: engine.PhaseDiscovering})
	row := w.menu[actions.IDRepair]
	c.ok(row != nil && row.entry.Busy != "", "Repair has no busy wording")
	if row != nil {
		c.ok(row.sub.Label() == row.entry.Busy && row.sub.IsVisible(), "the running cell says %q, want %q", row.sub.Label(), row.entry.Busy)
	}
	w.busy, w.current = false, ""
	c.feed(engine.PhaseChanged{Phase: engine.PhaseIdle})

	n := len(w.toasted)
	w.send(engine.Abort{})
	c.seq(1300,
		func() {
			c.ok(len(w.toasted) == n+1 && w.toasted[n] == "archmaid couldn't reach the engine", "toasts %q, want the engine unreachable", w.toasted[n:])
			w.win.Close()
			covNotice("late notice") // posted to the main loop
			for glib.MainContextDefault().Iteration(false) {
			}
			c.ok(w.closed && w.agentNotice == "", "closed %v, a notice reached the closed window: %q", w.closed, w.agentNotice)
		},
	)
}

// ---- shell ----

func init() { covScenarios["cov-shell"] = covShell }

// covShell: the sidebar overlay's key and click handlers through their
// controllers: with the overlay closed they pass the event on; open, Escape
// and a click outside close it and the click is claimed.
func covShell(c *covEnv) {
	w := c.w
	c.quiet()
	sh := w.shell
	var key *gtk.EventControllerKey
	var press *gtk.GestureClick
	for _, o := range controllersOf(sh.split) {
		switch x := o.Cast().(type) {
		case *gtk.EventControllerKey:
			key = x
		case *gtk.GestureClick:
			press = x
		}
	}
	if key == nil || press == nil {
		c.fail("the split view lacks its key or press controller (%v %v)", key != nil, press != nil)
		c.finish()
		return
	}
	c.ok(!w.navKey(gdk.KEY_a), "a key other than Escape was handled")
	c.ok(!w.navKey(gdk.KEY_Escape), "Escape was handled with the overlay closed")
	c.ok(!w.navPress(5, 5), "a press was handled with the overlay closed")
	c.seq(150,
		func() {
			sh.split.SetCollapsed(true)
			sh.split.SetShowSidebar(true)
			c.ok(sh.overlayOpen(), "the overlay didn't open")
			// Escape through the controller.
			key.Emit("key-pressed", uint(gdk.KEY_Escape), uint(0), gdk.ModifierType(0))
			c.ok(!sh.overlayOpen(), "Escape through the controller left the overlay open")
			sh.split.SetShowSidebar(true)
		},
		func() {
			// A press far right of the sidebar is outside it: closes the
			// overlay and claims the sequence.
			x := float64(w.win.Width() - 5)
			press.Emit("pressed", 1, x, 40.0)
			c.ok(!sh.overlayOpen(), "a press outside through the controller left the overlay open")
		},
	)
}
