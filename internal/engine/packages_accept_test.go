package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// acceptQueue is an AUR queue whose first step carries the audit accept
// (what actions.AURDiscover does for a plan with high findings).
func acceptQueue(accept func() error) []steps.Step {
	run := func(context.Context, steps.Env) steps.Result { return steps.Result{} }
	return []steps.Step{
		{ID: steps.IDAURFetchPrefix + "app", Title: "Checking out app…", Run: run, OnAccept: accept},
		{ID: steps.IDAURBuildPrefix + "app", Title: "Building app…", Run: run},
	}
}

func acceptPreview(h *harness, accept func() error) Output {
	h.startPackages(aurReq)
	return h.discovered(actions.PkgPreview{Plan: aurPlan(false), Steps: acceptQueue(accept)})
}

// TestPackagesAcceptRunsOnYesOnly: the sheet's yes runs the first step's
// OnAccept exactly once, as a hook before the first step starts (and not
// again when that step launches); a no, a cancel and an abort run nothing.
func TestPackagesAcceptRunsOnYesOnly(t *testing.T) {
	t.Run("yes", func(t *testing.T) {
		h := pkgHarness(t)
		acceptPreview(h, func() error { return nil })
		o := h.confirm(true)
		hooks := all[RunHook](o.Effects)
		if len(hooks) != 1 || hooks[0].Fn == nil || hooks[0].Step != steps.IDAURFetchPrefix+"app" {
			t.Fatalf("hooks = %+v", hooks)
		}
		// the hook comes before the first step starts
		hookAt, stepAt := -1, -1
		for i, e := range o.Effects {
			switch e.(type) {
			case RunHook:
				hookAt = i
			case StartStep:
				stepAt = i
			}
		}
		if hookAt < 0 || stepAt < 0 || hookAt > stepAt {
			t.Errorf("hook at %d, first step at %d: %+v", hookAt, stepAt, o.Effects)
		}
		if ss := effect[StartStep](t, o); ss.Step.OnAccept != nil {
			t.Error("the step still carries OnAccept: it would run twice")
		}
		// finishing the queue never runs a second hook
		for i := 0; i < 2; i++ {
			if hs := all[RunHook](h.result(steps.Result{}).Effects); len(hs) != 0 {
				t.Errorf("another hook after step %d: %+v", i, hs)
			}
		}
		enginetest.CheckContract(t, h.evs)
	})
	for name, answer := range map[string]func(*harness, PromptID) Output{
		"no":     func(h *harness, id PromptID) Output { return h.do(h.s.Confirm(id, false)) },
		"cancel": func(h *harness, id PromptID) Output { return h.cancel(id) },
		"abort":  func(h *harness, _ PromptID) Output { return h.abort() },
	} {
		t.Run(name, func(t *testing.T) {
			h := pkgHarness(t)
			id := event[NeedsConfirm](t, acceptPreview(h, func() error { t.Error("committed"); return nil })).ID
			o := answer(h, id)
			if _, ok := find[RunHook](o.Effects); ok {
				t.Error("a RunHook after " + name)
			}
			if _, ok := find[StartStep](o.Effects); ok {
				t.Error("a step started after " + name)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPackagesWithoutAcceptRunsNoHook: a queue with no OnAccept (no high
// findings, a repo install) opens no hook.
func TestPackagesWithoutAcceptRunsNoHook(t *testing.T) {
	h := pkgHarness(t)
	acceptPreview(h, nil)
	o := h.confirm(true)
	if _, ok := find[RunHook](o.Effects); ok {
		t.Errorf("RunHook without an accept: %+v", o.Effects)
	}
	if _, ok := find[StartStep](o.Effects); !ok {
		t.Error("the queue did not start")
	}
}

// TestPackagesAcceptSurvivesAFailedBuild: the hook ran at the yes, so a build
// that fails afterwards leaves the commit done. Right on purpose: the user
// accepted the maintainers' state, not the build's outcome; rolling it back
// would flag the same changes again and make them re-accept for a failure
// that has nothing to do with them (topgrade's accept does the same).
func TestPackagesAcceptSurvivesAFailedBuild(t *testing.T) {
	h := pkgHarness(t)
	commits := 0
	acceptPreview(h, func() error { commits++; return nil })
	o := h.confirm(true)
	effect[RunHook](t, o).Fn()
	h.result(steps.Result{})
	o = h.result(steps.Result{Err: errors.New("makepkg failed")})
	if _, ok := find[RunHook](o.Effects); ok {
		t.Error("a failed build ran a hook")
	}
	if commits != 1 {
		t.Errorf("commits = %d; want exactly 1, kept after the failure", commits)
	}
}
