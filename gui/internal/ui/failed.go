package ui

import (
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The failed step's actions (P8 row 12, spec A6): Run again, Open
// {category}.log and Copy error, on a failed step's header once the run
// has finished (never while one goes). All three are frontend only: Run
// again is an ordinary Start of the action (its gate, audit and password
// as a click on it), not a retry of the step, which the engine doesn't
// have.

// failedActions is a step header's row of buttons.
type failedActions struct {
	box              *gtk.Box
	again, log, copy *gtk.Button
}

func (w *Window) newFailedActions(h *stepHeader) *failedActions {
	a := &failedActions{box: gtk.NewBox(gtk.OrientationHorizontal, 6)}
	a.box.AddCSSClass("am-step-actions")
	a.box.SetVisible(false)
	a.again = gtk.NewButton()
	a.again.AddCSSClass("am-step-action")
	a.again.AddCSSClass("suggested-action") // the primary one; the others stay secondary
	a.log = gtk.NewButton()
	a.log.AddCSSClass("am-step-action")
	a.copy = gtk.NewButtonWithLabel("Copy error")
	a.copy.AddCSSClass("am-step-action")
	for _, b := range []*gtk.Button{a.again, a.log, a.copy} {
		a.box.Append(b)
	}
	// The buttons act on the row the header shows when clicked: a header
	// widget is recycled across sections.
	a.again.ConnectClicked(func() {
		if r, ok := w.failedRow(h); ok {
			w.runAgain(r)
		}
	})
	a.log.ConnectClicked(func() {
		if r, ok := w.failedRow(h); ok {
			w.openStepLog(r.Category)
		}
	})
	a.copy.ConnectClicked(func() {
		if r, ok := w.failedRow(h); ok {
			w.copyError(r.ID)
		}
	})
	return a
}

// failedRow is h's row when it shows a failed step's actions.
func (w *Window) failedRow(h *stepHeader) (runview.Row, bool) {
	if h.sec == nil {
		return runview.Row{}, false
	}
	r, i := w.rowOf(h.sec.step)
	return r, i >= 0 && w.offerFailed(r)
}

// offerFailed: r's actions are offered: it failed, and the run is over.
func (w *Window) offerFailed(r runview.Row) bool {
	return r.State == runview.RowFailed && w.model.Finished != nil && !w.busy && !w.closed
}

// drawFailed shows or hides h's actions for row r.
func (w *Window) drawFailed(h *stepHeader, r runview.Row) {
	show := w.offerFailed(r)
	h.acts.box.SetVisible(show)
	if !show {
		return
	}
	id, label := w.againAction(r)
	h.acts.again.SetLabel(label)
	h.acts.again.SetSensitive(id != "")
	cat := r.Category
	if cat == "" {
		cat = logstore.Cleanup
	}
	h.acts.log.SetLabel("Open " + string(cat) + ".log")
}

// againAction is the action Run again starts, and its button's label: a
// failed topgrade step starts Run topgrade ("Run topgrade again"); any
// other step starts the run's own action again ("Run Full Cleanup again").
func (w *Window) againAction(r runview.Row) (string, string) {
	if r.ID == steps.IDTopgrade {
		return actions.IDTopgrade, "Run topgrade again"
	}
	name, _, _ := strings.Cut(overview.Label(w.current), " (")
	return w.current, "Run " + name + " again"
}

// runAgain starts the failed row's action, dry when the run was dry.
func (w *Window) runAgain(r runview.Row) {
	id, _ := w.againAction(r)
	if id != "" {
		w.start(id, w.dry)
	}
}

// openStepLog shows the Logs page at this run's folder and the category's
// log.
func (w *Window) openStepLog(cat logstore.Category) {
	w.refreshLogs()
	if run, ok := runFolder(w.logs.runList, w.model.Started, w.model.DryRun); ok {
		w.showRun(run, cat)
	}
	w.stack.SetVisibleChildName("logs")
}

// runFolder is the run directory a run started at started writes to: the
// dry-run slot for a dry run, else the one stamped with its minute or the
// minute before (the Driver's log store is stamped when it is made, just
// before the queue is planned), else the newest real run. runs is
// logview.Runs' list, newest first.
func runFolder(runs []logview.Run, started time.Time, dry bool) (logview.Run, bool) {
	if dry {
		for _, r := range runs {
			if r.DryRun {
				return r, true
			}
		}
		return logview.Run{}, false
	}
	if !started.IsZero() {
		t := started.Local()
		for _, at := range []time.Time{t, t.Add(-time.Minute)} {
			for _, r := range runs {
				if !r.DryRun && r.Name == at.Format("20060102-1504") {
					return r, true
				}
			}
		}
	}
	for _, r := range runs {
		if !r.DryRun {
			return r, true
		}
	}
	return logview.Run{}, false
}

// copyError puts the step's error and its last lines on the clipboard,
// with URL credentials removed (runview.Model.CopyError): the desktop's
// clipboard history keeps what is copied.
func (w *Window) copyError(step string) {
	text := w.model.CopyError(step)
	w.copied = text
	w.win.Clipboard().SetText(text)
	w.toast("Error copied")
}

// redrawHeaders redraws the bound step headers (the failed actions follow
// busy).
func (w *Window) redrawHeaders() {
	if w.tl == nil {
		return
	}
	for _, s := range w.tl.sections {
		if s.header != nil {
			w.drawHeader(s.header)
		}
	}
}
