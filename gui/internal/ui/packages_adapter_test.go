package ui

import (
	"reflect"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The adapter's rules, event by event (packages_adapter.go). No window: it is
// a plain function of the batch.

var (
	adPlan = steps.PkgPlan{Targets: []string{"foo"}, Hash: "sha256:x",
		Rows: []steps.PkgRow{{Name: "foo", Version: "1-1", Repo: "extra", Target: true}}}
	adReq = steps.PkgRequest{Names: []string{"foo"}}
)

func confirmEv(id engine.PromptID) engine.NeedsConfirm {
	return engine.NeedsConfirm{ID: id, Kind: engine.ConfirmPackages, Title: "Install 1 package?"}
}

func TestAdapterPreviewPairsWithItsConfirm(t *testing.T) {
	var a pkgAdapter
	sp := a.split([]engine.Event{engine.PackagesPreview{Prompt: 4, Plan: adPlan}, confirmEv(4)}, true)
	want := []pkgview.Event{pkgview.Preview{Prompt: 4, Plan: adPlan}}
	if !reflect.DeepEqual(sp.Page, want) || len(sp.Rest) != 0 || len(sp.Unasked) != 0 {
		t.Fatalf("page %#v rest %#v unasked %v", sp.Page, sp.Rest, sp.Unasked)
	}
	// The pair may come in two batches.
	a.reset()
	sp = a.split([]engine.Event{engine.PackagesPreview{Prompt: 5, Plan: adPlan}}, true)
	if len(sp.Page) != 0 || len(sp.Rest) != 0 {
		t.Fatalf("the preview alone gave %#v %#v", sp.Page, sp.Rest)
	}
	sp = a.split([]engine.Event{confirmEv(5)}, true)
	if !reflect.DeepEqual(sp.Page, []pkgview.Event{pkgview.Preview{Prompt: 5, Plan: adPlan}}) {
		t.Fatalf("page %#v", sp.Page)
	}
}

// A sheet no preview came with cannot be shown: it is answered no, and the
// run model never sees it (its confirm would open a dialog of its own).
func TestAdapterAConfirmWithoutAPreviewIsAnsweredNo(t *testing.T) {
	var a pkgAdapter
	sp := a.split([]engine.Event{confirmEv(7)}, true)
	if len(sp.Page) != 0 || len(sp.Rest) != 0 || !reflect.DeepEqual(sp.Unasked, []engine.PromptID{7}) {
		t.Fatalf("%#v", sp)
	}
	// A preview of another prompt does not stand for it.
	a.reset()
	sp = a.split([]engine.Event{engine.PackagesPreview{Prompt: 1, Plan: adPlan}, confirmEv(2)}, true)
	if len(sp.Page) != 0 || len(sp.Unasked) != 1 {
		t.Fatalf("%#v", sp)
	}
}

// Another kind of confirm is the run model's.
func TestAdapterLeavesOtherConfirmsAlone(t *testing.T) {
	var a pkgAdapter
	gate := engine.NeedsConfirm{ID: 1, Kind: engine.ConfirmGate}
	sp := a.split([]engine.Event{gate}, false)
	if len(sp.Page) != 0 || !reflect.DeepEqual(sp.Rest, []engine.Event{gate}) {
		t.Fatalf("%#v", sp)
	}
}

func TestAdapterRefusedUpdatesPendingStale(t *testing.T) {
	var a pkgAdapter
	ref := steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}, Detail: "gvim replaces vim"}
	newer := steps.PkgPlan{Targets: []string{"foo"}, Hash: "sha256:new"}
	sp := a.split([]engine.Event{
		engine.PackagesRefused{Req: adReq, Refusal: ref},
		engine.PackagesUpdatesPending{Req: adReq, Count: 7},
		engine.PackagesStale{Req: adReq, Plan: &newer},
		engine.PackagesStale{Req: adReq},
	}, true)
	want := []pkgview.Event{
		pkgview.Refused{Req: adReq, Refusal: &ref},
		pkgview.UpdatesPending{Count: 7},
		pkgview.Stale{Req: adReq, Plan: newer},
		pkgview.Stale{Req: adReq},
	}
	if !reflect.DeepEqual(sp.Page, want) || len(sp.Rest) != 0 {
		t.Fatalf("page %#v rest %#v", sp.Page, sp.Rest)
	}
}

// QueueStarted is the run model's and the page's: the Run page takes over.
func TestAdapterQueueStartedIsRunStarted(t *testing.T) {
	var a pkgAdapter
	q := engine.QueueStarted{Action: actions.IDPackages}
	sp := a.split([]engine.Event{q}, true)
	if !reflect.DeepEqual(sp.Page, []pkgview.Event{pkgview.RunStarted{}}) || !reflect.DeepEqual(sp.Rest, []engine.Event{q}) {
		t.Fatalf("%#v", sp)
	}
	// Another action's queue is not a Packages run.
	a.reset()
	other := engine.QueueStarted{Action: actions.IDTopgrade}
	if sp := a.split([]engine.Event{other}, true); len(sp.Page) != 0 || len(sp.Rest) != 1 {
		t.Fatalf("%#v", sp)
	}
}

func TestAdapterFinishedIsDone(t *testing.T) {
	ok := engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCompleted}
	for name, tc := range map[string]struct {
		f      engine.Finished
		queued bool
		okDone bool
	}{
		"completed":              {ok, true, true},
		"a failed step":          {engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCompleted, Errs: 1}, true, false},
		"a refused step":         {engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCompleted, Refused: 1, Skipped: 1}, true, false},
		"aborted":                {engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeAborted}, true, false},
		"cancelled at the sheet": {engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeCancelled}, false, false},
		"a notice":               {engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeNotice}, false, false},
	} {
		var a pkgAdapter
		a.queued = tc.queued
		sp := a.split([]engine.Event{tc.f}, true)
		if !reflect.DeepEqual(sp.Page, []pkgview.Event{pkgview.Done{OK: tc.okDone}}) || !sp.Ended || sp.Queued != tc.queued {
			t.Errorf("%s: %#v", name, sp)
		}
		// The Run page gets the Finished of a run it showed, and only that.
		if got := len(sp.Rest) == 1; got != tc.queued {
			t.Errorf("%s: the run model got the Finished = %v, want %v", name, got, tc.queued)
		}
	}
	// With no Packages run going, or another action's end, it is not ours.
	var a pkgAdapter
	if sp := a.split([]engine.Event{ok}, false); sp.Ended || len(sp.Page) != 0 || len(sp.Rest) != 1 {
		t.Errorf("no run: %#v", sp)
	}
	if sp := a.split([]engine.Event{engine.Finished{Action: actions.IDTopgrade}}, true); sp.Ended || len(sp.Rest) != 1 {
		t.Errorf("another action: %#v", sp)
	}
}

// A run that ended at its sheet leaves the Run page alone: its discovery
// result is dropped, and the notice (a plan that could not be made) is a
// toast.
func TestAdapterAPlanThatCouldNotBeMadeIsAToast(t *testing.T) {
	var a pkgAdapter
	sp := a.split([]engine.Event{
		engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking the packages…"},
		engine.DiscoveryResult{Action: actions.IDPackages, Outcome: engine.DiscoveryFailed},
		engine.Notice{Text: "Couldn't plan the package change."},
		engine.Finished{Action: actions.IDPackages, Outcome: engine.OutcomeNotice},
	}, true)
	if !reflect.DeepEqual(sp.Toasts, []string{"Couldn't plan the package change."}) || len(sp.Rest) != 1 {
		t.Fatalf("toasts %q rest %#v", sp.Toasts, sp.Rest)
	}
	if _, ok := sp.Rest[0].(engine.PhaseChanged); !ok {
		t.Fatalf("rest %#v: the phase is the run model's", sp.Rest)
	}
	// After the queue started a notice is the Run page's, as any run's.
	a.queued = true
	n := engine.Notice{Text: "x"}
	if sp := a.split([]engine.Event{n}, true); len(sp.Toasts) != 0 || len(sp.Rest) != 1 {
		t.Fatalf("%#v", sp)
	}
}

func TestAdapterResetsBetweenRuns(t *testing.T) {
	var a pkgAdapter
	a.split([]engine.Event{engine.PackagesPreview{Prompt: 1, Plan: adPlan}, engine.QueueStarted{Action: actions.IDPackages}}, true)
	if !a.queued || a.preview == nil {
		t.Fatalf("%+v", a)
	}
	a.reset()
	if a.queued || a.preview != nil {
		t.Fatalf("%+v", a)
	}
}
