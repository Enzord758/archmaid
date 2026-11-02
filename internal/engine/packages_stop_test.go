package engine_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A Packages or AUR run stops at its first step that does not end OK: the
// steps after it are Skipped and never started (B6). The AUR queue below has
// the shape aurbuild.Fetcher.Queue gives: deps, pin, build, collect (which
// inserts the root install), remember.

var errFail = errors.New("it failed")

func aurStopQueue() []steps.Step {
	inproc := func(context.Context, steps.Env) steps.Result { return steps.Result{} }
	return []steps.Step{
		{ID: steps.IDPkgInstall, Verb: steps.VerbPkgInstall, Title: "Installing dependencies…", NeedsPriv: true, PkgPlan: &steps.PkgPlan{}},
		{ID: steps.IDAURFetchPrefix + "app", Title: "Checking out app…", Run: inproc},
		{ID: steps.IDAURBuildPrefix + "app", Title: "Building app…", Argv: []string{"makepkg"}},
		{ID: "aur-collect", Title: "Checking the built packages…", Run: func(context.Context, steps.Env) steps.Result {
			return steps.Result{InsertNext: &steps.Step{ID: steps.IDPkgInstallLocal, Verb: steps.VerbPkgInstallLocal,
				Title: "Installing AUR packages…", NeedsPriv: true, PkgLocal: &steps.PkgLocal{}}}
		}},
		{ID: "aur-remember", Title: "Recording what you reviewed…", Run: inproc},
	}
}

// drive answers every step the run starts: those named in fail end with
// errFail (notGranted: refused by polkit), the rest end OK. It returns the
// ids of the steps that were started, in order, and the last Output.
func drive(t *testing.T, h *harness, fail map[string]bool, notGranted bool) (started []string, o Output) {
	t.Helper()
	o = h.confirm(true)
	for i := 0; i < 20; i++ {
		if _, ok := find[ReadFaillock](o.Effects); ok {
			o = h.handle(FaillockRead{Deny: 3})
		}
		ss, ok := find[StartStep](o.Effects)
		if !ok {
			return started, o
		}
		started = append(started, ss.Step.ID)
		switch {
		case fail[ss.Step.ID] && notGranted:
			o = h.handle(StepExited{Seq: ss.Seq, Err: exit127, NotGranted: true})
		case fail[ss.Step.ID] && ss.Step.Run != nil:
			o = h.handle(StepResult{Seq: ss.Seq, Result: steps.Result{Err: errFail}})
		case fail[ss.Step.ID]:
			o = h.handle(StepExited{Seq: ss.Seq, Err: errFail})
		case ss.Step.Run != nil:
			o = h.handle(StepResult{Seq: ss.Seq, Result: ss.Step.Run(context.Background(), steps.Env{})})
		default:
			o = h.handle(StepExited{Seq: ss.Seq})
		}
	}
	t.Fatal("the run did not end")
	return
}

// TestAURQueueStopsAtTheFirstFailure: each step of the AUR queue fails in
// turn; every later step is Skipped and never started, and the run ends
// failed (last-run.json: failed), so makepkg never builds an unpinned tree
// and nothing is collected, installed or remembered after a failure.
func TestAURQueueStopsAtTheFirstFailure(t *testing.T) {
	order := []string{steps.IDPkgInstall, steps.IDAURFetchPrefix + "app", steps.IDAURBuildPrefix + "app",
		"aur-collect", steps.IDPkgInstallLocal, "aur-remember"}
	for fi, failing := range order[:len(order)-1] {
		for _, refused := range []bool{false, true} {
			name := failing
			if refused {
				name += "/refused"
				if failing == steps.IDAURFetchPrefix+"app" || failing == "aur-collect" || failing == steps.IDAURBuildPrefix+"app" {
					continue // only root steps are refused by polkit
				}
			}
			t.Run(name, func(t *testing.T) {
				h := pkgHarness(t)
				h.startPackages(aurReq)
				h.discovered(actions.PkgPreview{Plan: aurPlan(true), Steps: aurStopQueue()})
				started, o := drive(t, h, map[string]bool{failing: true}, refused)
				if want := order[:fi+1]; !slices.Equal(started, want) {
					t.Fatalf("started %v, want only %v", started, want)
				}
				fin := event[Finished](t, o)
				planLen := 5 // collect inserts the install only when it succeeds
				if fi >= 4 {
					planLen = 6
				}
				wantSkipped := planLen - fi - 1
				if fin.Outcome != OutcomeCompleted || fin.Skipped != wantSkipped+boolInt(refused) || fin.NotRun != 0 {
					t.Errorf("Finished = %+v, want %d skipped after the failure", fin, wantSkipped)
				}
				if refused {
					if fin.Refused != 1 {
						t.Errorf("Finished = %+v, want 1 refused", fin)
					}
				} else if fin.Errs != 1 {
					t.Errorf("Finished = %+v, want 1 error", fin)
				}
				// the steps after the failure were settled Skipped
				done := map[string]steps.Status{}
				for _, d := range all[StepDone](h.evs) {
					done[d.ID] = d.Status
				}
				for _, id := range order[fi+1:] {
					if st, ok := done[id]; ok && st != steps.Skipped {
						t.Errorf("%s: %v, want Skipped", id, st)
					}
				}
				if done["aur-remember"] != steps.Skipped {
					t.Errorf("aur-remember: %v, want Skipped", done["aur-remember"])
				}
				rec := h.lastRun()
				want := logstore.OutcomeFailed
				if refused {
					want = logstore.OutcomeIncomplete
					if fi == 0 {
						want = logstore.OutcomeCancelled // nothing ran: the existing record rule
					}
				}
				if rec.Outcome != want {
					t.Errorf("record outcome %v, want %v", rec.Outcome, want)
				}
				enginetest.CheckContract(t, h.evs)
			})
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestAURQueueRunsToTheEnd: with no failure every step runs (the stop must
// not trip on the inserted install step).
func TestAURQueueRunsToTheEnd(t *testing.T) {
	h := pkgHarness(t)
	h.startPackages(aurReq)
	h.discovered(actions.PkgPreview{Plan: aurPlan(true), Steps: aurStopQueue()})
	started, o := drive(t, h, nil, false)
	want := []string{steps.IDPkgInstall, steps.IDAURFetchPrefix + "app", steps.IDAURBuildPrefix + "app",
		"aur-collect", steps.IDPkgInstallLocal, "aur-remember"}
	if !slices.Equal(started, want) {
		t.Fatalf("started %v, want %v", started, want)
	}
	if fin := event[Finished](t, o); fin.Errs != 0 || fin.Skipped != 0 {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestRepoQueuesStopOnFailure: the repo install and remove run end failed
// (their one step), and a longer queue built for them would stop too.
func TestRepoQueuesStopOnFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		req  steps.PkgRequest
		plan steps.PkgPlan
		id   string
	}{
		"install": {installReq, installPlan(), steps.IDPkgInstall},
		"remove":  {steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo"}}, removePlan(false), steps.IDPkgRemove},
	} {
		t.Run(name, func(t *testing.T) {
			h := pkgHarness(t)
			h.startPackages(tc.req)
			q := append(actions.Packages(tc.plan), steps.Step{ID: "after", Title: "after", Argv: []string{"true"}})
			h.discovered(actions.PkgPreview{Plan: tc.plan, Steps: q})
			started, o := drive(t, h, map[string]bool{tc.id: true}, false)
			if !slices.Equal(started, []string{tc.id}) {
				t.Fatalf("started %v", started)
			}
			if fin := event[Finished](t, o); fin.Errs != 1 || fin.Skipped != 1 {
				t.Errorf("Finished = %+v", fin)
			}
			if rec := h.lastRun(); rec.Outcome != logstore.OutcomeFailed {
				t.Errorf("record = %+v", rec)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestOtherQueuesKeepGoing: a Full Cleanup style queue is not stopped by a
// failed step: it is the Packages queues that depend on their predecessors.
func TestOtherQueuesKeepGoing(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q",
		steps.Step{ID: "a", Title: "a", Argv: []string{"a"}},
		steps.Step{ID: "b", Title: "b", Argv: []string{"b"}}))
	h.start("q")
	o := h.exit(errFail)
	if ss, ok := find[StartStep](o.Effects); !ok || ss.Step.ID != "b" {
		t.Fatalf("the second step did not start after a failure: %+v", o.Effects)
	}
}
