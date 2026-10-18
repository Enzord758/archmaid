package engine_test

import (
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Security review of #196 (lateAuth, #197). The predicate is exactly "one
// confirmed root step, no gate, a child, no Combine, sudo auth, real run";
// every other shape keeps its up-front probe, so no step can start without
// auth and no other queue lets a root step ask after a yes.

func withConfirm(s steps.Step) steps.Step {
	s.Confirm, s.Optional = "Sure?", true
	return s
}

// TestLateAuthPredicateIsExact: each way a queue differs from the lone
// confirmed root step still probes at Start (the up-front password is the
// gate), and so does the lone step under a dry-run-free polkit... (polkit
// and dry run never probe, they are checked separately below).
func TestLateAuthPredicateIsExact(t *testing.T) {
	root := func() steps.Step { return withConfirm(sudo(child("w", "sudo", "w"))) }
	gated := queue("q", root())
	gated.Gate = "Proceed?"
	noConfirm := root()
	noConfirm.Confirm = ""
	inprocRoot := withConfirm(sudo(inproc("ip")))
	cases := []struct {
		name string
		a    actions.Action
		late bool // no Probe at Start
	}{
		{"the lone confirmed root step", queue("q", root()), true},
		{"with a gate", gated, false},
		{"two steps", queue("q", root(), child("b", "true")), false},
		{"root step after a user step", queue("q", child("b", "true"), root()), false},
		{"no confirm", queue("q", noConfirm), false},
		{"in-process root step", queue("q", inprocRoot), false},
		// A step that is not NeedsPriv has nothing to probe: hyprpm-only
		// queues (SudoByName) and confirmed user steps never reach auth.
		{"SudoByName only", queue("q", func() steps.Step {
			s := withConfirm(child("hyprpm", "hyprpm", "update"))
			s.SudoByName = true
			return s
		}()), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Options{}, tc.a)
			o := h.start("q")
			probed := hasEffect[Probe](o)
			if probed == tc.late {
				t.Fatalf("Probe at Start = %v, want %v: %#v", probed, !tc.late, o.Effects)
			}
			if tc.late {
				// The first thing is the confirm; nothing ran, nothing keeps alive.
				if hasEffect[StartStep](o) && tc.name != "SudoByName only" {
					t.Fatal("a root step started at Start")
				}
				if hasEffect[KeepAlive](o) {
					t.Fatalf("keep-alive before the confirm: %#v", o.Effects)
				}
			}
		})
	}
	// Polkit and dry run never use the late path (and never probe).
	for _, opts := range []Options{{Auth: AuthPolkit}, {DryRun: true}} {
		h := newHarness(t, opts, queue("q", root()))
		o := h.start("q")
		if hasEffect[Probe](o) || hasEffect[KeepAlive](o) {
			t.Errorf("%+v: %#v", opts, o.Effects)
		}
	}
}

// A real run's step that does NOT match still gets its up-front probe:
// Services' disable step (no confirm, a pick list is its choice).
func TestOtherRootQueuesKeepUpFrontProbe(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", actions.DisableUnits([]string{"a.service"})...))
	if o := h.start("q"); !hasEffect[Probe](o) {
		t.Fatalf("Services' queue starts without the probe: %#v", o.Effects)
	}
}

// The password path of the late ask: the faillock count is read after the
// probe and before the prompt (the prompt comes only with the read's
// answer), the prompt counts down 3-2-1, the third wrong password refuses
// the step with no Authenticate, StartStep or KeepAlive after it, and no
// further prompt opens.
func TestLateAuthFaillockAndThreeWrong(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	h.start("lw")
	o := h.confirm(true)
	effect[Probe](t, o)
	o = h.handle(ProbeDone{})
	effect[ReadFaillock](t, o)
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Fatal("password asked before the faillock count was read")
	}
	o = h.handle(FaillockRead{Failures: 2, Deny: 3})
	if fs := event[FaillockState](t, o); fs.Failures != 2 || fs.Locked {
		t.Errorf("FaillockState = %+v", fs)
	}
	np := event[NeedsPassword](t, o)
	if np.AttemptsLeft != 3 {
		t.Fatalf("first prompt = %+v", np)
	}
	for left := 2; left >= 0; left-- {
		o = h.password("bad")
		effect[Authenticate](t, o)
		o = h.handle(AuthDone{})
		if hasEffect[StartStep](o) || hasEffect[KeepAlive](o) {
			t.Fatalf("after a wrong password (%d left): %#v", left, o.Effects)
		}
		if left > 0 {
			if np = event[NeedsPassword](t, o); np.AttemptsLeft != left {
				t.Fatalf("AttemptsLeft = %d, want %d", np.AttemptsLeft, left)
			}
		}
	}
	if _, ok := find[NeedsPassword](o.Events); ok {
		t.Error("a fourth prompt opened")
	}
	if f := event[Finished](t, o); f.Refused != 1 {
		t.Errorf("Finished = %+v, want the step refused", f)
	}
	enginetest.CheckContract(t, h.evs)
}

// A locked account: the state is reported before the prompt, as up front.
func TestLateAuthLockedAccountIsReported(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	h.start("lw")
	h.confirm(true)
	h.handle(ProbeDone{})
	o := h.handle(FaillockRead{Failures: 3, Deny: 3, Unlock: t0})
	if fs := event[FaillockState](t, o); !fs.Locked {
		t.Errorf("FaillockState = %+v, want locked", fs)
	}
}

// Cancelling the late password prompt, or aborting anywhere between the yes
// and the step, ends the action with nothing started; the late answers of
// the probe, the faillock read or the check are ignored.
func TestLateAuthCancelAndAbortStartNothing(t *testing.T) {
	steps := []struct {
		name string
		to   func(h *harness) Output // leaves the Session in the stage, returns the last Output
		end  func(h *harness) Output
	}{
		{"cancel the password prompt", func(h *harness) Output {
			h.confirm(true)
			h.handle(ProbeDone{})
			return h.handle(FaillockRead{Deny: 3})
		}, nil},
		{"abort while probing", func(h *harness) Output { return h.confirm(true) }, nil},
		{"abort at the faillock read", func(h *harness) Output { h.confirm(true); return h.handle(ProbeDone{}) }, nil},
		{"abort at the password check", func(h *harness) Output {
			h.confirm(true)
			h.handle(ProbeDone{})
			h.handle(FaillockRead{Deny: 3})
			return h.password("pw")
		}, nil},
	}
	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Options{}, lateWipe())
			h.start("lw")
			last := tc.to(h)
			var o Output
			if np, ok := find[NeedsPassword](last.Events); ok && tc.name == "cancel the password prompt" {
				o = h.cancel(np.ID)
			} else {
				o = h.abort()
			}
			if f := event[Finished](t, o); f.Outcome != OutcomeCancelled {
				t.Errorf("Finished = %+v, want cancelled", f)
			}
			for _, late := range []Input{ProbeDone{Cached: true}, FaillockRead{Deny: 3}, AuthDone{OK: true}} {
				if o := h.handle(late); hasEffect[StartStep](o) || hasEffect[KeepAlive](o) || len(o.Events) != 0 {
					t.Errorf("late %T after the end: %#v", late, o)
				}
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// After the yes and a right password the step starts with the confirm's
// OnAccept hook run (it was accepted), keep-alive on, and keep-alive off
// again when the action ends. After a cached probe the same.
func TestLateGoStartsAcceptedWithKeepAlive(t *testing.T) {
	for _, cached := range []bool{true, false} {
		wipe := withConfirm(sudo(child("wipe", "sudo", "w")))
		wipe.OnAccept = func() error { return nil }
		h := newHarness(t, Options{}, queue("lw", wipe))
		h.start("lw")
		h.confirm(true)
		var o Output
		if cached {
			o = h.handle(ProbeDone{Cached: true})
		} else {
			h.handle(ProbeDone{})
			h.handle(FaillockRead{Deny: 3})
			h.password("pw")
			o = h.handle(AuthDone{OK: true})
		}
		effect[StartStep](t, o)
		effect[RunHook](t, o)
		if !hasEffect[KeepAlive](o) {
			t.Fatalf("cached=%v: no keep-alive on the late start: %#v", cached, o.Effects)
		}
		o = h.ok()
		var off bool
		for _, e := range o.Effects {
			if k, ok := e.(KeepAlive); ok && !k.On {
				off = true
			}
		}
		if !off {
			t.Errorf("cached=%v: keep-alive left on at the end: %#v", cached, o.Effects)
		}
		enginetest.CheckContract(t, h.evs)
	}
}

// A no: not one effect that reaches privilege (Probe, ReadFaillock,
// Authenticate, KeepAlive, StartStep), from Start to Finished.
func TestLateAuthNoIsSilent(t *testing.T) {
	h := newHarness(t, Options{}, lateWipe())
	o1 := h.start("lw")
	o2 := h.confirm(false)
	for _, o := range []Output{o1, o2} {
		for _, e := range o.Effects {
			switch e.(type) {
			case Probe, ReadFaillock, Authenticate, KeepAlive, StartStep:
				t.Errorf("a no reached privilege: %#v", e)
			}
		}
	}
}
