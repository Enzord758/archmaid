package engine_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Clear caches through the Session: the discovery's pick, then the queue
// built from what was ticked. Owner decision 2026-10-18: no password before
// the pick, ever; the pick is the destructive confirm; a password only when
// the package cache was ticked.

func cachePickAction() actions.Action {
	return actions.Action{ID: actions.IDClearCaches, Busy: "Measuring the caches…",
		Discover: func(context.Context, actions.Env) actions.Discovery { panic("the Session never discovers") }}
}

func cachePick(rows ...actions.PickRow) actions.NeedsSelection {
	if rows == nil {
		rows = []actions.PickRow{
			{Label: "~/.cache — 2.3 GiB"},
			{Label: "Entire package cache — 7.3 GiB (1398 files)", Note: "Can't reinstall or downgrade those packages without internet"},
		}
	}
	return actions.NeedsSelection{
		Items: []string{actions.PickUserCache, actions.PickPkgCache}, Rows: rows, Steps: actions.ClearCachesSteps}
}

// startPick runs Clear caches to its open pick and checks that nothing
// privileged has happened yet.
func startPick(t *testing.T, opts Options, pick actions.NeedsSelection) (*harness, NeedsSelection) {
	t.Helper()
	h := newHarness(t, opts, cachePickAction())
	o := h.start(actions.IDClearCaches)
	if hasEffect[Probe](o) {
		t.Fatal("probed before discovering")
	}
	o = h.discovered(pick)
	ns := event[NeedsSelection](t, o)
	for _, e := range o.Effects {
		switch e.(type) {
		case Probe, ReadFaillock, Authenticate, StartStep, KeepAlive:
			t.Fatalf("the pick is open and %#v already happened: no password before the pick", e)
		}
	}
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Fatal("a password was asked before the pick")
	}
	return h, ns
}

// The pick event carries the rows with the warning, titled and worded.
func TestClearCachesPickEvent(t *testing.T) {
	_, ns := startPick(t, Options{}, cachePick())
	if !strings.HasPrefix(ns.Title, "Clear caches") || ns.Go != "clear" || len(ns.Rows) != 2 {
		t.Fatalf("event %+v", ns)
	}
	if !slices.Equal(ns.Items, []string{actions.PickUserCache, actions.PickPkgCache}) {
		t.Errorf("items %q", ns.Items)
	}
	if got := ns.Rows[1]; !strings.Contains(got.Note, "Can't reinstall or downgrade") || !strings.Contains(got.Label, "7.3 GiB (1398 files)") {
		t.Errorf("package-cache row %+v lost its size or its warning", got)
	}
}

// A pick of nothing, and a cancelled pick, run nothing and ask no password.
func TestClearCachesNothingPickedRunsNothing(t *testing.T) {
	for _, how := range []string{"empty", "unknown key", "cancel"} {
		t.Run(how, func(t *testing.T) {
			h, ns := startPick(t, Options{}, cachePick())
			var o Output
			switch how {
			case "empty":
				o = h.do(h.s.Select(ns.ID, nil))
			case "unknown key":
				o = h.do(h.s.Select(ns.ID, []string{"zzz"}))
			default:
				o = h.cancel(ns.ID)
			}
			for _, e := range o.Effects {
				switch e.(type) {
				case Probe, ReadFaillock, Authenticate, StartStep:
					t.Errorf("%s: %#v", how, e)
				}
			}
			if _, ok := find[NeedsPassword](o.Events); ok {
				t.Errorf("%s asked for a password", how)
			}
			if f := event[Finished](t, o); f.Outcome != OutcomeCancelled {
				t.Errorf("%s: Finished %+v", how, f)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// A disabled row can't be picked, even by a front end that sends it.
func TestClearCachesDisabledRowIgnored(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick(
		actions.PickRow{Label: "~/.cache", Note: "Empty: nothing to clear", Disabled: true},
		actions.PickRow{Label: "Entire package cache — 1 MiB (3 files)", Note: "warn", Disabled: true}))
	o := h.do(h.s.Select(ns.ID, []string{actions.PickUserCache, actions.PickPkgCache}))
	if hasEffect[StartStep](o) || hasEffect[Probe](o) {
		t.Errorf("a disabled row ran: %#v", o.Effects)
	}
	if f := event[Finished](t, o); f.Outcome != OutcomeCancelled {
		t.Errorf("Finished %+v", f)
	}
}

// ~/.cache alone runs as the user at once: no probe, no password, no root.
func TestClearCachesUserOnlyAsksNoPassword(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickUserCache}))
	if hasEffect[Probe](o) || hasEffect[ReadFaillock](o) || hasEffect[Authenticate](o) {
		t.Fatalf("~/.cache alone touched privilege: %#v", o.Effects)
	}
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Fatal("~/.cache alone asked for a password")
	}
	if _, ok := find[NeedsConfirm](o.Events); ok {
		t.Fatal("the pick was the confirm: ~/.cache asked again")
	}
	ss := effect[StartStep](t, o)
	if ss.Step.ID != steps.IDUserCache || ss.Step.NeedsPriv || ss.Step.Verb != "" {
		t.Errorf("started %+v", ss.Step)
	}
	if hasEffect[KeepAlive](o) {
		t.Error("a keep-alive for a step with no root")
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// The package cache ticked: the password comes after the pick (probe, then
// the faillock count, then the prompt), then the wipe runs with no second
// confirm.
func TestClearCachesPackageCacheAsksPasswordAfterThePick(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache}))
	effect[Probe](t, o)
	if hasEffect[StartStep](o) {
		t.Fatal("the wipe started before root")
	}
	o = h.handle(ProbeDone{Cached: false})
	effect[ReadFaillock](t, o)
	o = h.handle(FaillockRead{Failures: 0, Deny: 3})
	if _, ok := find[NeedsPassword](o.Events); !ok {
		t.Fatalf("no password prompt after the faillock read: %#v", o.Events)
	}
	effect[Authenticate](t, h.password("pw"))
	o = h.handle(AuthDone{OK: true})
	if _, ok := find[NeedsConfirm](o.Events); ok {
		t.Fatal("the pick was the confirm: the wipe asked again")
	}
	ss := effect[StartStep](t, o)
	if ss.Step.ID != steps.IDPaccacheWipe || ss.Step.Verb != steps.VerbPaccacheWipe {
		t.Errorf("started %+v", ss.Step)
	}
	if !slices.Contains(o.Effects, Effect(KeepAlive{On: true})) {
		t.Errorf("no keep-alive after auth: %#v", o.Effects)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// Both ticked: one up-front password (the root step is in the queue), then
// ~/.cache first and the wipe second.
func TestClearCachesBothAskPasswordThenRunBothInOrder(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache, actions.PickUserCache}))
	effect[Probe](t, o)
	if hasEffect[StartStep](o) {
		t.Fatal("a step started before root")
	}
	h.handle(ProbeDone{Cached: false})
	h.handle(FaillockRead{Failures: 0, Deny: 3})
	h.password("pw")
	o = h.handle(AuthDone{OK: true})
	if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDUserCache {
		t.Errorf("first step %+v, want ~/.cache", ss.Step)
	}
	o = h.ok()
	if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDPaccacheWipe {
		t.Errorf("second step %+v, want the wipe", ss.Step)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// Cancelling the password after the pick runs neither step.
func TestClearCachesPasswordCancelRunsNothing(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache, actions.PickUserCache}))
	h.handle(ProbeDone{Cached: false})
	o := h.handle(FaillockRead{Failures: 0, Deny: 3})
	np := event[NeedsPassword](t, o)
	o = h.cancel(np.ID)
	if hasEffect[StartStep](o) || hasEffect[Authenticate](o) {
		t.Errorf("a cancelled password ran something: %#v", o.Effects)
	}
	if f := event[Finished](t, o); f.Outcome != OutcomeCancelled {
		t.Errorf("Finished %+v", f)
	}
	enginetest.CheckContract(t, h.evs)
}

// Under polkit the wipe asks through the helper's own dialog: the pick is
// still first, and the user-cache step runs with no prompt at all.
func TestClearCachesPolkitUserOnlyAsksNothing(t *testing.T) {
	h, ns := startPick(t, Options{Auth: AuthPolkit}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickUserCache}))
	if hasEffect[Probe](o) || hasEffect[ReadFaillock](o) {
		t.Fatalf("~/.cache alone touched privilege under polkit: %#v", o.Effects)
	}
	effect[StartStep](t, o)
}
