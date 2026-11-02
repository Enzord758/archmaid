package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// The Packages run end to end, headless: the real engine Session and the
// real Driver, with the helper's real verbs (in-process, against the pacman
// fake) behind both seams the GUI wires to the helper — Env.PlanPackages
// (the plan verb, as the user) and Deps.Helper (the root verb, as pkexec
// would start it). The Packages page is GUI- and polkit-only, so the TUI's
// `make e2e` cannot reach it; this is the driver-level stand-in. The seams
// are fakes of helperclient.Run's process handling only: the argv comes
// from helperclient.Argv, and the helper's output goes through
// helperclient's own parser (ParsePkgPlan).

type fakeSudo struct{}

func (fakeSudo) Probe(context.Context) bool                           { return true }
func (fakeSudo) Authenticate(context.Context, string) (string, error) { return "", nil }
func (fakeSudo) Refresh(context.Context)                              {}
func (fakeSudo) Command(argv []string, _ bool) proc.Spec {
	return proc.Spec{Name: argv[0], Args: argv[1:]}
}

// pkgRun is one driver over one pacman fake.
type pkgRun struct {
	t    *testing.T
	p    *pkgFake
	d    *driver.Driver
	root []steps.Step // the root steps the driver started
}

func newPkgRun(t *testing.T) *pkgRun {
	t.Helper()
	r := &pkgRun{t: t, p: newPkgFake(t)}
	plan := func(_ context.Context, req steps.PkgRequest) (steps.PkgPlan, error) {
		if err := helperclient.CheckPkgRequest(req); err != nil {
			return steps.PkgPlan{}, err
		}
		args := []string{planVerb(req)}
		if req.AsDeps {
			args = append(args, "--asdeps")
		}
		if req.WithConfig {
			args = append(args, "-n")
		}
		code, out := r.p.run(append(args, req.Names...)...)
		return parse(planVerb(req), req, out, code)
	}
	rootVerb := func(_ context.Context, seq uint64, st steps.Step, emit func(engine.Input)) helperclient.Outcome {
		r.root = append(r.root, st)
		argv, err := helperclient.Argv(st)
		if err != nil {
			emit(engine.StepExited{Seq: seq, Err: err})
			return helperclient.Outcome{Err: err}
		}
		emit(engine.PrivilegeGranted{Seq: seq})
		code, out := r.p.run(helperArgs(t, argv)...)
		var exitErr error
		if code != helper.ExitOK {
			req := steps.PkgRequest{Mode: st.PkgPlan.Mode, Names: st.Items, AsDeps: st.PkgPlan.AsDeps, WithConfig: st.PkgPlan.WithConfig}
			_, exitErr = parse(st.Verb, req, out, code)
			if exitErr == nil {
				exitErr = errors.New("the helper failed without a refusal")
			}
		} else {
			emit(engine.StepOutput{Seq: seq, Lines: []string{"done"}})
		}
		emit(engine.StepExited{Seq: seq, Err: exitErr})
		return helperclient.Outcome{Granted: true, Err: exitErr}
	}
	opts := engine.Options{Auth: engine.AuthPolkit, Env: actions.Env{PlanPackages: plan}}
	r.d = driver.New(engine.New(opts), driver.Deps{
		Priv: fakeSudo{}, Log: logstore.New(logstore.Options{Dir: t.TempDir()}),
		Polkit: true, Helper: rootVerb,
		Status:    func() sysstat.Status { return sysstat.Status{} },
		Faillock:  func() engine.FaillockRead { return engine.FaillockRead{Deny: 3} },
		TickEvery: time.Hour, KillGrace: 300 * time.Millisecond, WaitDelay: 200 * time.Millisecond,
	})
	t.Cleanup(r.d.Close)
	return r
}

func (r *pkgRun) next() engine.Event {
	r.t.Helper()
	select {
	case ev, ok := <-r.d.Events():
		if !ok {
			r.t.Fatal("Events closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		r.t.Fatal("no event for 10 s")
		return nil
	}
}

// run sends StartPackages and reads to Finished, answering the sheet yes.
// beforeConfirm runs once the sheet is up, before the answer goes.
func (r *pkgRun) run(req steps.PkgRequest, beforeConfirm func()) []engine.Event {
	r.t.Helper()
	if !r.d.Send(engine.StartPackages{Req: req}) {
		r.t.Fatal("Send(StartPackages) = false")
	}
	var evs []engine.Event
	for {
		ev := r.next()
		if _, tick := ev.(engine.StatusUpdated); !tick {
			evs = append(evs, ev)
		}
		if c, ok := ev.(engine.NeedsConfirm); ok {
			if beforeConfirm != nil {
				beforeConfirm()
			}
			r.d.Send(engine.Confirm{Prompt: c.ID, Yes: true})
		}
		if _, ok := ev.(engine.Finished); ok {
			enginetest.CheckContract(r.t, evs)
			return evs
		}
	}
}

func eventOf[T engine.Event](evs []engine.Event) (T, bool) {
	for _, ev := range evs {
		if e, ok := ev.(T); ok {
			return e, true
		}
	}
	var z T
	return z, false
}

// TestPackagesRunPlanConfirmRootDone: plan → confirm → root verb → done, for
// an install and a removal: the root step carries the plan the helper made,
// the helper runs pacman for exactly that, and the run completes.
func TestPackagesRunPlanConfirmRootDone(t *testing.T) {
	for _, c := range pkgRequests {
		t.Run(c.name, func(t *testing.T) {
			r := newPkgRun(t)
			evs := r.run(c.req, nil)
			if nc, ok := eventOf[engine.NeedsConfirm](evs); !ok || nc.Kind != engine.ConfirmPackages {
				t.Fatalf("no packages sheet: %+v", nc)
			}
			if len(r.root) != 1 || r.root[0].Verb != runVerb(c.req) || r.root[0].PkgPlan == nil {
				t.Fatalf("root steps %+v", r.root)
			}
			if len(r.p.streams) != 1 || !slices.Contains(r.p.streams[0], c.req.Names[0]) {
				t.Errorf("pacman ran %q", r.p.streams)
			}
			if fin, _ := eventOf[engine.Finished](evs); fin.Outcome != engine.OutcomeCompleted || fin.Action != actions.IDPackages {
				t.Errorf("Finished = %+v", fin)
			}
		})
	}
}

// TestPackagesRunStaleThenRePreview: the system moves between the plan and
// the confirm. The root verb ran nothing, the run ends with PackagesStale
// carrying the helper's new plan, and a second request previews that new
// plan and, confirmed, installs.
func TestPackagesRunStaleThenRePreview(t *testing.T) {
	r := newPkgRun(t)
	req := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}
	evs := r.run(req, func() { r.p.planItems = append(r.p.planItems, "extra surprise 9-1") })
	st, ok := eventOf[engine.PackagesStale](evs)
	if !ok || st.Plan == nil || !slices.ContainsFunc(st.Plan.Rows, func(row steps.PkgRow) bool { return row.Name == "surprise" }) {
		t.Fatalf("PackagesStale = %+v (%v)", st, ok)
	}
	if len(r.p.streams) != 0 {
		t.Fatalf("a stale run ran pacman: %q", r.p.streams)
	}
	// The re-preview: asked again, the plan is the new one, and it runs.
	evs = r.run(req, nil)
	if len(r.p.streams) != 1 || !slices.Contains(r.p.streams[0], "foo") {
		t.Fatalf("pacman ran %q", r.p.streams)
	}
	if got := r.root[len(r.root)-1].PkgPlan; got.Hash != st.Plan.Hash {
		t.Errorf("second run confirmed %s, want the stale run's new plan %s", got.Hash, st.Plan.Hash)
	}
	if fin, _ := eventOf[engine.Finished](evs); fin.Outcome != engine.OutcomeCompleted {
		t.Errorf("Finished = %+v", fin)
	}
}

// TestPackagesRunRefusedAtThePlan: a refused request reaches the page with
// the helper's reason (updates pending as its own event with the count, any
// other as PackagesRefused) and no sheet or root step.
func TestPackagesRunRefusedAtThePlan(t *testing.T) {
	req := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}
	t.Run("updates pending", func(t *testing.T) {
		r := newPkgRun(t)
		r.p.updates = []string{"glibc 2.40-1 -> 2.41-1", "bash 5.2-1 -> 5.3-1"}
		evs := r.run(req, nil)
		if up, ok := eventOf[engine.PackagesUpdatesPending](evs); !ok || up.Count != 2 {
			t.Fatalf("PackagesUpdatesPending = %+v (%v)", up, ok)
		}
		if len(r.root) != 0 || len(r.p.streams) != 0 {
			t.Errorf("a refused request ran %v %q", r.root, r.p.streams)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		r := newPkgRun(t)
		r.p.rel["foo"] = "Conflicts With  : foo-old"
		evs := r.run(req, nil)
		pr, ok := eventOf[engine.PackagesRefused](evs)
		if !ok || pr.Refusal.Reason != steps.RefuseConflict || !slices.Equal(pr.Refusal.Names, []string{"foo", "foo-old"}) {
			t.Fatalf("PackagesRefused = %+v (%v)", pr, ok)
		}
		if len(r.root) != 0 || len(r.p.streams) != 0 {
			t.Errorf("a refused request ran %v %q", r.root, r.p.streams)
		}
	})
}
