package engine_test

import (
	"errors"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Security review of #204 (lateRoot). A pick's queue that starts with a
// root-free step settles root only before its first root step; no root step
// may start without auth whatever happens before it.

// noRootStarted fails when o starts a root step or primes root.
func noRootStarted(t *testing.T, what string, o Output) {
	t.Helper()
	for _, e := range o.Effects {
		switch e := e.(type) {
		case StartStep:
			if e.Step.NeedsPriv {
				t.Errorf("%s: root step %q started", what, e.Step.ID)
			}
		case Authenticate:
			t.Errorf("%s: %#v", what, e)
		case KeepAlive:
			if e.On {
				t.Errorf("%s: %#v", what, e)
			}
		}
	}
}

func u2() steps.Step { return child("u2", "true") }

// pickShape runs a custom pick (the two cache items) whose Steps builds the queue.
func pickShape(t *testing.T, mk func([]string) []steps.Step) (*harness, Output) {
	t.Helper()
	pk := cachePick()
	pk.Rows, pk.Steps = nil, mk
	h, ns := startPick(t, Options{}, pk)
	return h, h.do(h.s.Select(ns.ID, []string{actions.PickUserCache, actions.PickPkgCache}))
}

// Which pick-built queues defer root (no Probe at the pick) and which keep
// the up-front probe.
func TestLateRootPredicateThroughSelect(t *testing.T) {
	u := func() steps.Step { return child("u", "true") }
	r := func() steps.Step { return sudo(child("r", "sudo", "x")) }
	comb := func() steps.Step { s := r(); s.Combine = "c"; return s }
	cases := []struct {
		name string
		q    []steps.Step
		late bool
	}{
		{"user then root", []steps.Step{u(), r()}, true},
		{"user, user, root", []steps.Step{u(), u2(), r()}, true},
		{"root first", []steps.Step{r(), u()}, false},
		{"root alone", []steps.Step{r()}, false},
		{"confirmed root after user", []steps.Step{u(), withConfirm(r())}, false},
		{"in-process root after user", []steps.Step{u(), sudo(inproc("ip"))}, false},
		{"combined root after user", []steps.Step{u(), comb()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, o := pickShape(t, func([]string) []steps.Step { return tc.q })
			if got := !hasEffect[Probe](o); got != tc.late {
				t.Fatalf("deferred = %v, want %v: %#v", got, tc.late, o.Effects)
			}
			if !tc.late {
				for _, e := range o.Effects {
					if ss, ok := e.(StartStep); ok && ss.Step.NeedsPriv {
						t.Fatal("a root step started before the up-front probe")
					}
				}
			}
		})
	}
}

// A non-pick queue of the same shape keeps the up-front probe: lateRoot is
// for a pick's queue only.
func TestLateRootNeverForANonPickQueue(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", child("u", "true"), sudo(child("r", "sudo", "x"))))
	o := h.start("q")
	effect[Probe](t, o)
	if hasEffect[StartStep](o) {
		t.Fatal("a step started before the probe")
	}
}

// Services' pick is one root step (never lateRoot): the probe comes at the
// pick, before the step.
func TestLateRootNotForServices(t *testing.T) {
	pk := actions.NeedsSelection{Items: []string{"a.service", "b.service"}, Steps: actions.DisableUnits}
	h := newHarness(t, Options{}, cachePickAction())
	h.start(actions.IDClearCaches)
	ns := event[NeedsSelection](t, h.discovered(pk))
	o := h.do(h.s.Select(ns.ID, []string{"a.service", "b.service"}))
	effect[Probe](t, o)
	if hasEffect[StartStep](o) {
		t.Fatal("services step started before root")
	}
}

// Abort at each stage between the pick and the wipe, then every late answer
// the Driver may still deliver: the wipe never starts, nothing authenticates.
func TestLateRootAbortThenLateAnswers(t *testing.T) {
	stages := []struct {
		name string
		to   func(t *testing.T, h *harness, ns NeedsSelection)
		want Outcome
	}{
		{"user cache running", func(t *testing.T, h *harness, ns NeedsSelection) {
			h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache, actions.PickUserCache}))
		}, OutcomeAborted},
		{"probe in flight", func(t *testing.T, h *harness, ns NeedsSelection) { bothToRoot(t, h, ns) }, OutcomeAborted},
		{"faillock in flight", func(t *testing.T, h *harness, ns NeedsSelection) {
			bothToRoot(t, h, ns)
			effect[ReadFaillock](t, h.handle(ProbeDone{}))
		}, OutcomeAborted},
		{"password prompt open", func(t *testing.T, h *harness, ns NeedsSelection) {
			bothToRoot(t, h, ns)
			h.handle(ProbeDone{})
			h.handle(FaillockRead{Deny: 3})
		}, OutcomeAborted},
		{"password check in flight", func(t *testing.T, h *harness, ns NeedsSelection) {
			bothToRoot(t, h, ns)
			h.handle(ProbeDone{})
			h.handle(FaillockRead{Deny: 3})
			effect[Authenticate](t, h.password("pw"))
		}, OutcomeAborted},
	}
	for _, st := range stages {
		t.Run(st.name, func(t *testing.T) {
			h, ns := startPick(t, Options{}, cachePick())
			st.to(t, h, ns)
			o := h.abort()
			noRootStarted(t, "abort", o)
			if f := event[Finished](t, o); f.Outcome != st.want {
				t.Errorf("Finished %+v, want %v", f, st.want)
			}
			for _, in := range []Input{ProbeDone{Cached: true}, ProbeDone{}, FaillockRead{Deny: 3},
				AuthDone{OK: true}, AuthDone{OK: false}, StepExited{Seq: 1}, StepExited{Seq: 2}} {
				noRootStarted(t, "late answer", h.handle(in))
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// The first step failing (a crash) still stops at the probe before the wipe,
// and a cancelled password afterwards is aborted with the failure counted.
func TestLateRootFirstStepFailsStillProbes(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache, actions.PickUserCache}))
	o := h.exit(errors.New("boom"))
	effect[Probe](t, o)
	noRootStarted(t, "after a failed first step", o)
	h.handle(ProbeDone{})
	np := event[NeedsPassword](t, h.handle(FaillockRead{Deny: 3}))
	o = h.cancel(np.ID)
	noRootStarted(t, "cancel", o)
	if f := event[Finished](t, o); f.Outcome != OutcomeAborted || f.Errs != 1 {
		t.Errorf("Finished %+v", f)
	}
}

// A right password whose ticket can't be reused ends the run before the
// wipe. ~/.cache ran, so the run is aborted with the wipe skipped, not
// "cancelled" with a step left pending.
func TestLateRootNoTicketAfterUserCache(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	bothToRoot(t, h, ns)
	h.handle(ProbeDone{})
	h.handle(FaillockRead{Deny: 3})
	effect[Authenticate](t, h.password("pw"))
	o := h.handle(AuthDone{Detail: "sudo kept no ticket", Err: errors.New("no ticket"), NoTicket: true})
	noRootStarted(t, "no ticket", o)
	f := event[Finished](t, o)
	if f.Outcome != OutcomeAborted || f.NotRun != 0 {
		t.Errorf("Finished %+v, want aborted with nothing left pending", f)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeAborted {
		t.Errorf("record %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}
