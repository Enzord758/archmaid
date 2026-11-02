package ui

import (
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A Packages run on the window's Driver. Like any action it takes a fresh
// Driver and sets busy and current, but its sheet is the Packages page's, so
// the Run page comes up only when the queue starts (RunStarted). The events
// pass the adapter (packages_adapter.go) on their way to the run model.

// startPackages sends engine.StartPackages(req) on a fresh Driver.
func (w *Window) startPackages(req steps.PkgRequest) {
	if w.closed {
		return
	}
	if w.busy {
		// Another run is going: the sheet says so, nothing is sent.
		post(func() {
			if !w.closed && w.pkgs != nil && w.pkgs.ctl != nil {
				w.pkgs.ctl.Handle(pkgview.Refused{Req: req, Refusal: &steps.PkgRefusal{Reason: steps.RefuseBusy}})
			}
		})
		return
	}
	w.replaceDriver(false)
	w.pkgAd.reset()
	w.pkgRun = true
	w.queued, w.lastFinished, w.ended, w.dryOff = false, w.model.Finished, nil, false
	w.pagePrev = len(w.model.Rows) > 0
	w.current, w.dry, w.model.Stopped, w.agentNotice = actions.IDPackages, false, "", ""
	w.model.StartDry = w.drvDry
	// The status says this request's dry flag, not the run before's.
	w.model.DryRun = w.drvDry
	// The status says this request's dry flag, not the run before's.
	w.model.Notice = ""
	w.updateRunHeader()
	w.setBusy(true)
	w.pageBefore, w.nothingPending = w.stack.VisibleChildName(), false
	w.updateTitle()
	w.updateOverview()
	w.send(engine.StartPackages{Req: req})
}

// pkgTake runs a batch of engine events through the adapter: it returns
// what goes on to the run model and what to do once the model has it (the
// page's events, a run that ended before its queue).
func (w *Window) pkgTake(evs []engine.Event) ([]engine.Event, func()) {
	if w.pkgs == nil || w.pkgs.ctl == nil {
		return evs, func() {}
	}
	sp := w.pkgAd.split(evs, w.pkgRun)
	for _, t := range sp.Toasts {
		w.toast(t)
	}
	for _, id := range sp.Unasked {
		w.send(engine.Confirm{Prompt: id, Yes: false})
	}
	return sp.Rest, func() {
		for _, ev := range sp.Page {
			w.pkgs.ctl.Handle(ev)
		}
		if !sp.Ended {
			return
		}
		w.pkgRun = false
		if !sp.Queued {
			// It ended at its sheet: the Run page never heard of it, so
			// the controls and the status come back here.
			w.setBusy(false)
			w.updateTitle()
			w.setStatus()
			w.updateOverview()
		}
		if f := w.pkgAfter; f != nil {
			w.pkgAfter = nil
			post(f)
		}
	}
}

// pkgBackend is the page's Backend for wire: its own, or the window's.
func (w *Window) pkgBackend(wire *PackagesWiring) pkgBackend {
	var inner pkgview.Backend = pkgEngine{w}
	if wire.Backend != nil {
		inner = wire.Backend
	}
	return pkgBackend{w: w, inner: inner}
}

// pkgEngine is the page's Backend on the window's Driver: StartPackages,
// the sheet's answer and an abort. "Update system first" is the window's own
// topgrade (pkgBackend.UpdateSystem).
type pkgEngine struct{ w *Window }

func (b pkgEngine) StartPackages(r steps.PkgRequest) { b.w.startPackages(r) }
func (b pkgEngine) Answer(p int, yes bool) {
	b.w.send(engine.Confirm{Prompt: engine.PromptID(p), Yes: yes})
}
func (b pkgEngine) Abort() {
	if b.w.pkgRun {
		b.w.send(engine.Abort{})
	}
}
func (b pkgEngine) UpdateSystem() {}
