package ui

import (
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The adapter between the engine's Packages events and the page's model
// (pkgview.Event). It sits in front of the run model: the Packages events
// and the sheet's NeedsConfirm are the page's, not the Run page's, so they
// never reach runview (whose confirm would open a dialog of its own).
//
//	PackagesPreview + NeedsConfirm{ConfirmPackages}  ->  Preview
//	PackagesRefused / PackagesUpdatesPending / PackagesStale  ->  the same-named event
//	QueueStarted                                     ->  RunStarted (and on to the Run page)
//	Finished                                         ->  Done
//
// A run that ends before its queue (a refusal, a no at the sheet, a plan
// that could not be made) is the sheet's business: its DiscoveryResult,
// Notice and Finished are kept from the Run page, so it still shows the
// last run, and the notice becomes a toast.

// pkgAdapter holds the state between the events of one Packages run.
type pkgAdapter struct {
	preview *engine.PackagesPreview
	queued  bool // QueueStarted came: the run is the Run page's too
}

// pkgSplit is what the adapter made of a batch.
type pkgSplit struct {
	// Rest goes on to the run model, in order.
	Rest []engine.Event
	// Page goes to the page's controller, in order.
	Page []pkgview.Event
	// Toasts are notices of a run that never reached its queue.
	Toasts []string
	// Unasked are sheets the engine asked that no preview came with: the
	// page cannot show them, so they are answered no.
	Unasked []engine.PromptID
	// Ended: the run is over (Finished came); Queued says it reached its
	// queue, so the Run page has its Finished too.
	Ended, Queued bool
}

// reset starts a run.
func (a *pkgAdapter) reset() { *a = pkgAdapter{} }

// split folds evs in. active says a Packages run is going: only then are
// the Notice, DiscoveryResult and Finished of the run the page's.
func (a *pkgAdapter) split(evs []engine.Event, active bool) pkgSplit {
	var out pkgSplit
	for _, ev := range evs {
		switch e := ev.(type) {
		case engine.PackagesPreview:
			p := e
			a.preview = &p
		case engine.NeedsConfirm:
			if e.Kind != engine.ConfirmPackages {
				out.Rest = append(out.Rest, ev)
				continue
			}
			if a.preview == nil || a.preview.Prompt != e.ID {
				out.Unasked = append(out.Unasked, e.ID)
				continue
			}
			out.Page = append(out.Page, pkgview.Preview{Prompt: int(e.ID), Plan: a.preview.Plan})
			a.preview = nil
		case engine.PackagesRefused:
			ref := e.Refusal
			out.Page = append(out.Page, pkgview.Refused{Req: e.Req, Refusal: &ref})
		case engine.PackagesUpdatesPending:
			out.Page = append(out.Page, pkgview.UpdatesPending{Count: e.Count})
		case engine.PackagesStale:
			var plan steps.PkgPlan
			if e.Plan != nil {
				plan = *e.Plan
			}
			out.Page = append(out.Page, pkgview.Stale{Req: e.Req, Plan: plan})
		case engine.QueueStarted:
			out.Rest = append(out.Rest, ev)
			if active && e.Action == actions.IDPackages {
				a.queued = true
				out.Page = append(out.Page, pkgview.RunStarted{})
			}
		case engine.DiscoveryResult:
			if active && e.Action == actions.IDPackages && !a.queued {
				continue // a plan that could not be made: the Notice says it
			}
			out.Rest = append(out.Rest, ev)
		case engine.Notice:
			if active && !a.queued {
				out.Toasts = append(out.Toasts, e.Text)
				continue
			}
			out.Rest = append(out.Rest, ev)
		case engine.Finished:
			if !active || e.Action != actions.IDPackages {
				out.Rest = append(out.Rest, ev)
				continue
			}
			out.Ended, out.Queued = true, a.queued
			out.Page = append(out.Page, pkgview.Done{OK: e.Outcome == engine.OutcomeCompleted && e.Errs == 0 && e.Refused == 0 && e.Skipped == 0})
			if a.queued {
				out.Rest = append(out.Rest, ev)
			}
		default:
			out.Rest = append(out.Rest, ev)
		}
	}
	return out
}
