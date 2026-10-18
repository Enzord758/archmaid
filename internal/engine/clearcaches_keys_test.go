package engine_test

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The pick matches its two keys exactly: a near miss is not a pick, and it
// costs no probe, no password and no step.
func TestClearCachesKeysMatchExactly(t *testing.T) {
	for _, key := range []string{
		"pkgcache ", " pkgcache", "PkgCache", "pkg cache", "pkgcache\n", "~/.cache/", "~/.cache ",
		"~/.Cache", "~", "$HOME/.cache", "*", "../pkgcache", "", "pkgcache,~/.cache", "~/.cache pkgcache",
	} {
		h, ns := startPick(t, Options{}, cachePick())
		o := h.do(h.s.Select(ns.ID, []string{key}))
		for _, e := range o.Effects {
			switch e.(type) {
			case Probe, ReadFaillock, Authenticate, StartStep, KeepAlive:
				t.Errorf("key %q: %#v", key, e)
			}
		}
		if _, ok := find[NeedsPassword](o.Events); ok {
			t.Errorf("key %q asked for a password", key)
		}
		if f := event[Finished](t, o); f.Outcome != OutcomeCancelled {
			t.Errorf("key %q: Finished %+v", key, f)
		}
	}
}

// A repeated key is one pick: one ~/.cache step and one wipe, never two.
func TestClearCachesDuplicateKeysRunEachStepOnce(t *testing.T) {
	h, ns := startPick(t, Options{}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache, actions.PickPkgCache, actions.PickUserCache, actions.PickUserCache}))
	if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDUserCache {
		t.Fatalf("first step %+v", ss.Step)
	}
	o = h.ok()
	effect[Probe](t, o)
	h.handle(ProbeDone{Cached: false})
	h.handle(FaillockRead{Failures: 0, Deny: 3})
	h.password("pw")
	o = h.handle(AuthDone{OK: true})
	if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDPaccacheWipe {
		t.Fatalf("second step %+v", ss.Step)
	}
	o = h.ok()
	if hasEffect[StartStep](o) {
		t.Errorf("a third step started: %#v", o.Effects)
	}
	enginetest.CheckContract(t, h.evs)
}

// Ticking both with only one row enabled runs only that one: a disabled
// package-cache row costs no password, a disabled ~/.cache row is not run.
func TestClearCachesOneDisabledRowOfTwo(t *testing.T) {
	t.Run("package cache disabled", func(t *testing.T) {
		h, ns := startPick(t, Options{}, cachePick(
			actions.PickRow{Label: "~/.cache — 1 MiB"},
			actions.PickRow{Label: "Entire package cache", Note: "Empty", Disabled: true}))
		o := h.do(h.s.Select(ns.ID, []string{actions.PickUserCache, actions.PickPkgCache}))
		if hasEffect[Probe](o) || hasEffect[ReadFaillock](o) || hasEffect[Authenticate](o) {
			t.Fatalf("a disabled wipe reached privilege: %#v", o.Effects)
		}
		if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDUserCache || ss.Step.NeedsPriv {
			t.Errorf("started %+v", ss.Step)
		}
		if hasEffect[StartStep](h.ok()) {
			t.Error("more than ~/.cache ran")
		}
	})
	t.Run("user cache disabled", func(t *testing.T) {
		h, ns := startPick(t, Options{}, cachePick(
			actions.PickRow{Label: "~/.cache", Note: "Empty", Disabled: true},
			actions.PickRow{Label: "Entire package cache — 1 MiB (1 file)", Note: "warn"}))
		h.do(h.s.Select(ns.ID, []string{actions.PickUserCache, actions.PickPkgCache}))
		h.handle(ProbeDone{Cached: false})
		h.handle(FaillockRead{Failures: 0, Deny: 3})
		h.password("pw")
		o := h.handle(AuthDone{OK: true})
		if ss := effect[StartStep](t, o); ss.Step.ID != steps.IDPaccacheWipe {
			t.Errorf("first step %+v, want only the wipe", ss.Step)
		}
		if hasEffect[StartStep](h.ok()) {
			t.Error("~/.cache ran although its row was disabled")
		}
	})
}

// Under polkit the wipe is never ahead of the faillock read, and archmaid
// asks no sudo password of its own (no probe, no keep-alive).
func TestClearCachesPolkitWipeReadsFaillockFirst(t *testing.T) {
	h, ns := startPick(t, Options{Auth: AuthPolkit}, cachePick())
	o := h.do(h.s.Select(ns.ID, []string{actions.PickPkgCache}))
	if hasEffect[Probe](o) || hasEffect[KeepAlive](o) || hasEffect[StartStep](o) {
		t.Fatalf("polkit wipe: %#v", o.Effects)
	}
	effect[ReadFaillock](t, o)
}
