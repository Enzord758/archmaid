package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's run on the real Driver (StartPackages → the plan on a
// worker → the sheet → the polkit step through the Helper), with the plan
// and the helper faked: nothing reaches pacman, pkexec or the helper.

func pkgPlan() steps.PkgPlan {
	return steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo"},
		Hash: "sha256:" + strings.Repeat("c", 64),
		Rows: []steps.PkgRow{{Name: "foo", Version: "1-1", Repo: "extra", Target: true}}}
}

// runPackages sends StartPackages and reads events to Finished, answering
// the sheet with yes.
func (r *rig) runPackages(req steps.PkgRequest, yes bool) []engine.Event {
	r.t.Helper()
	if !r.d.Send(engine.StartPackages{Req: req}) {
		r.t.Fatal("Send(StartPackages) = false on an open Driver")
	}
	var evs []engine.Event
	for {
		ev, ok := r.next()
		if !ok {
			r.t.Fatalf("Events closed before Finished; reports:\n%s", r.reports.text())
		}
		if _, tick := ev.(engine.StatusUpdated); !tick {
			evs = append(evs, ev)
		}
		if c, ok := ev.(engine.NeedsConfirm); ok {
			r.d.Send(engine.Confirm{Prompt: c.ID, Yes: yes})
		}
		if _, ok := ev.(engine.Finished); ok {
			enginetest.CheckContract(r.t, evs)
			return evs
		}
	}
}

func pkgRig(t *testing.T, plan func(context.Context, steps.PkgRequest) (steps.PkgPlan, error), h Helper) (*rig, *[]steps.Step) {
	t.Helper()
	var ran []steps.Step
	wrapped := func(ctx context.Context, seq uint64, st steps.Step, emit func(engine.Input)) helperclient.Outcome {
		ran = append(ran, st)
		return h(ctx, seq, st, emit)
	}
	opts := engine.Options{Auth: engine.AuthPolkit, Env: actions.Env{PlanPackages: plan}}
	return newRig(t, opts, Deps{Polkit: true, Helper: wrapped}), &ran
}

func TestStartPackagesInstalls(t *testing.T) {
	var asked steps.PkgRequest
	r, ran := pkgRig(t, func(_ context.Context, req steps.PkgRequest) (steps.PkgPlan, error) {
		asked = req
		return pkgPlan(), nil
	}, fakeHelper([]string{"(1/1) installing foo"}, true, false, false))
	evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
	if asked.Names[0] != "foo" {
		t.Errorf("the plan was asked for %+v", asked)
	}
	if nc := find[engine.NeedsConfirm](t, evs); nc.Kind != engine.ConfirmPackages {
		t.Errorf("confirm = %+v", nc)
	}
	if len(*ran) != 1 || (*ran)[0].PkgPlan == nil || (*ran)[0].Verb != steps.PkgVerbInstall {
		t.Fatalf("helper steps run: %+v", *ran)
	}
	if got, _ := lines(evs); len(got) != 1 || got[0] != "(1/1) installing foo" {
		t.Errorf("output %q", got)
	}
	if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeCompleted || fin.Action != actions.IDPackages {
		t.Errorf("Finished = %+v", fin)
	}
}

func TestStartPackagesDeclinedRunsNothing(t *testing.T) {
	r, ran := pkgRig(t, func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) { return pkgPlan(), nil },
		fakeHelper(nil, true, false, false))
	evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, false)
	if len(*ran) != 0 {
		t.Errorf("a helper step ran after a no: %+v", *ran)
	}
	if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeCancelled {
		t.Errorf("Finished = %+v", fin)
	}
}

func TestStartPackagesRefusal(t *testing.T) {
	r, ran := pkgRig(t, func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) {
		return steps.PkgPlan{}, &steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}, Detail: "gvim replaces vim"}
	}, fakeHelper(nil, true, false, false))
	evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"gvim"}}, true)
	if pr := find[engine.PackagesRefused](t, evs); pr.Refusal.Reason != steps.RefuseConflict {
		t.Errorf("PackagesRefused = %+v", pr)
	}
	if len(*ran) != 0 {
		t.Errorf("a helper step ran for a refused request: %+v", *ran)
	}
}

// The helper's stale verdict reaches the engine as the step's error, whole.
func TestStartPackagesStaleFromTheHelper(t *testing.T) {
	newer := pkgPlan()
	stale := func(_ context.Context, seq uint64, _ steps.Step, emit func(engine.Input)) helperclient.Outcome {
		emit(engine.PrivilegeGranted{Seq: seq})
		err := &steps.PkgRefusal{Reason: steps.RefuseStale, Detail: "stale", Plan: &newer}
		emit(engine.StepExited{Seq: seq, Err: err})
		return helperclient.Outcome{Granted: true, Err: err}
	}
	r, _ := pkgRig(t, func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) { return pkgPlan(), nil }, stale)
	evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
	if ps := find[engine.PackagesStale](t, evs); ps.Plan == nil {
		t.Errorf("PackagesStale = %+v", ps)
	}
}
