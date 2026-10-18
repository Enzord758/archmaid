package engine_test

import (
	"errors"
	"slices"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
)

// lateWipe is the "Delete entire package cache" shape: one confirmed root
// step, no gate.
func lateWipe() actions.Action {
	wipe := sudo(child("wipe", "sudo", "w"))
	wipe.Confirm, wipe.Optional = "Wipe?", true
	return queue("lw", wipe)
}

func hasEffect[E Effect](o Output) bool {
	for _, e := range o.Effects {
		if _, ok := e.(E); ok {
			return true
		}
	}
	return false
}

// A lone confirmed root step asks its confirm before any probe or password,
// and a no costs neither.
func TestLoneRootConfirmNoNeverAsksPassword(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	o := h.start("lw")
	if hasEffect[Probe](o) {
		t.Fatalf("probed before the confirm: %#v", o.Effects)
	}
	if nc := event[NeedsConfirm](t, o); nc.Kind != ConfirmStep {
		t.Fatalf("first prompt = %+v", nc)
	}
	o = h.confirm(false)
	if hasEffect[Probe](o) || hasEffect[Authenticate](o) || hasEffect[StartStep](o) {
		t.Errorf("a no reached privilege: %#v", o.Effects)
	}
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Error("a no asked for a password")
	}
	event[Finished](t, o)
	enginetest.CheckContract(t, h.evs)
}

// After a yes the password is asked once; the step runs after it.
func TestLoneRootConfirmYesAsksOnce(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	h.start("lw")
	o := h.confirm(true)
	effect[Probe](t, o)
	if hasEffect[StartStep](o) {
		t.Fatal("the step started before root")
	}
	effect[ReadFaillock](t, h.handle(ProbeDone{Cached: false}))
	h.handle(FaillockRead{Failures: 0, Deny: 3})
	o = h.password("pw")
	effect[Authenticate](t, o)
	o = h.handle(AuthDone{OK: true})
	effect[StartStep](t, o)
	if !slices.Contains(o.Effects, Effect(KeepAlive{On: true})) {
		t.Errorf("no keep-alive: %#v", o.Effects)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// A cached ticket after the yes starts the step with no prompt; three wrong
// passwords refuse it.
func TestLoneRootConfirmCachedAndRefused(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	h.start("lw")
	h.confirm(true)
	o := h.handle(ProbeDone{Cached: true})
	effect[StartStep](t, o)
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Error("cached ticket still asked")
	}

	h = newHarness(t, Options{}, lateWipe())
	h.start("lw")
	h.confirm(true)
	h.handle(ProbeDone{})
	h.handle(FaillockRead{Deny: 3})
	for i := 0; i < 3; i++ {
		h.password("bad")
		o = h.handle(AuthDone{Err: errors.New("wrong")})
	}
	if hasEffect[StartStep](o) {
		t.Error("the step ran after three wrong passwords")
	}
	if f := event[Finished](t, o); f.Refused != 1 {
		t.Errorf("Finished = %+v", f)
	}
	enginetest.CheckContract(t, h.evs)
}
