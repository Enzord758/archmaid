package engine_test

import (
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The pick's root step has no confirm, so lateGo starts it unaccepted: an
// OnAccept hook (a confirm's "yes" commit) must not run for it. The lone
// confirmed step of lateAuth keeps running its hook (lateauth_review_test).
func TestLateRootRootStepStartsUnaccepted(t *testing.T) {
	h, _ := pickShape(t, func([]string) []steps.Step {
		r := sudo(child("r", "sudo", "x"))
		r.OnAccept = func() error { return nil }
		return []steps.Step{child("u", "true"), r}
	})
	o := h.ok()
	effect[Probe](t, o)
	o = h.handle(ProbeDone{Cached: true})
	if ss := effect[StartStep](t, o); ss.Step.ID != "r" {
		t.Fatalf("started %+v", ss.Step)
	}
	if hasEffect[RunHook](o) {
		t.Fatalf("an OnAccept hook ran for a step nobody confirmed: %#v", o.Effects)
	}
}
