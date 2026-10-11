package engine_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// part is a helper step that joins the combined verb "all".
func part(id string) steps.Step {
	s := helper(id, "v-"+id)
	s.Combine = "all"
	return s
}

// confirmed is part(id) with an optional confirm.
func confirmed(id string) steps.Step {
	s := part(id)
	s.Confirm, s.Optional, s.AlwaysLog = id+"?", true, true
	return s
}

// combinedQueue: a user step, five combined helper steps (two of them
// confirmed), then a user step.
func combinedQueue() []steps.Step {
	return []steps.Step{child("before", "true"), confirmed("o"), part("p"), confirmed("w"), part("t"), part("j"),
		child("after", "true")}
}

// doneOf is the StepDone events of o by step ID.
func doneOf(o Output) map[string]StepDone {
	m := map[string]StepDone{}
	for _, d := range all[StepDone](o.Events) {
		m[d.ID] = d
	}
	return m
}

// TestCombinedRunOneCall: every confirm first, one faillock read, ONE
// StartStep for the whole run, then each part keeps its own row, in plan
// order, and the queue goes on with the next user step.
func TestCombinedRunOneCall(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	o := h.ok() // "before"
	if nc := event[NeedsConfirm](t, o); nc.Step != "o" {
		t.Fatalf("first confirm for %q", nc.Step)
	}
	if len(o.Effects) != 0 {
		t.Fatalf("effects before the confirms were answered: %#v", o.Effects)
	}
	o = h.confirm(true)
	if nc := event[NeedsConfirm](t, o); nc.Step != "w" {
		t.Fatalf("second confirm for %q (want w: every confirm before the dialog)", nc.Step)
	}
	o = h.confirm(true)
	if !slices.Equal(o.Effects, []Effect{ReadFaillock{}}) {
		t.Fatalf("effects = %#v, want one faillock read", o.Effects)
	}
	o = h.handle(FaillockRead{Deny: 3})
	st := effect[StartStep](t, o)
	var got []string
	for _, p := range st.Step.Parts {
		got = append(got, p.ID)
	}
	if st.Step.Verb != "all" || !slices.Equal(got, []string{"o", "p", "w", "t", "j"}) {
		t.Fatalf("StartStep = verb %q, parts %q", st.Step.Verb, got)
	}
	if s := event[StepStarted](t, o); s.ID != "o" {
		t.Errorf("first row started: %q", s.ID)
	}
	if w := event[AuthWaiting](t, o); w != (AuthWaiting{Kind: WaitPolkit, Step: "o"}) {
		t.Errorf("AuthWaiting = %+v", w)
	}
	seq := st.Seq
	h.handle(PrivilegeGranted{Seq: seq})
	h.handle(StepOutput{Seq: seq, Lines: []string{"removing"}})
	o = h.handle(PartDone{Seq: seq, Step: "o"})
	if d := doneOf(o)["o"]; d.Status != steps.OK {
		t.Errorf("o: %+v", d)
	}
	if !hasLog(o, "OK: o done") {
		t.Errorf("logs = %+v", all[LogWritten](o.Events))
	}
	noOutput(t, "a part out of order", h.s.Handle(PartStarted{Seq: seq, Step: "w"}))
	noOutput(t, "a stale part", h.s.Handle(PartStarted{Seq: seq + 9, Step: "p"}))
	for _, id := range []string{"p", "w", "t"} {
		if s := event[StepStarted](t, h.handle(PartStarted{Seq: seq, Step: id})); s.ID != id {
			t.Fatalf("started %q, want %q", s.ID, id)
		}
		var err error
		if id == "w" {
			err = errors.New("paccache exited 1")
		}
		h.handle(PartDone{Seq: seq, Step: id, Err: err})
	}
	h.handle(PartStarted{Seq: seq, Step: "j"})
	o = h.handle(StepExited{Seq: seq})
	if d := doneOf(o)["j"]; d.Status != steps.OK {
		t.Errorf("j: %+v", d)
	}
	if s := effect[StartStep](t, o); s.Step.ID != "after" {
		t.Errorf("next started %q, want the user step after the run", s.Step.ID)
	}
	h.ok()
	fin := event[Finished](t, h.out)
	if fin.Errs != 1 || fin.Refused != 0 {
		t.Errorf("Finished = %+v (want only w failed)", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunRefused: one refused dialog marks every step it covered
// AuthRefused (and only those), and the run goes on with the user steps.
func TestCombinedRunRefused(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(false) // w declined: Skipped, not covered by the dialog
	o := h.handle(FaillockRead{Deny: 3})
	if parts := effect[StartStep](t, o).Step.Parts; len(parts) != 4 {
		t.Fatalf("parts = %d, want 4 (w declined)", len(parts))
	}
	o = h.handle(StepExited{Seq: h.seq(), Err: errors.New("exit status 127"), NotGranted: true})
	if r := event[AuthResolved](t, o); r.Granted {
		t.Error("granted")
	}
	done := doneOf(o)
	for _, id := range []string{"o", "p", "t", "j"} {
		if done[id].Status != steps.AuthRefused {
			t.Errorf("%s: %+v, want AuthRefused", id, done[id])
		}
	}
	if _, ok := done["w"]; ok {
		t.Error("the declined step was settled again")
	}
	if !hasLog(o, "SKIP: j done (privilege not granted)") {
		t.Errorf("logs = %+v", all[LogWritten](o.Events))
	}
	if s := effect[StartStep](t, o); s.Step.ID != "after" {
		t.Errorf("next started %q", s.Step.ID)
	}
	h.ok()
	if fin := event[Finished](t, h.out); fin.Refused != 4 || fin.Outcome != OutcomeCompleted {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunNoAgent: no polkit agent for the one call: every part it
// covered is AuthRefused and says why.
func TestCombinedRunNoAgent(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(false)
	effect[StartStep](t, h.handle(FaillockRead{Deny: 3}))
	err := fmt.Errorf("privilege not granted: %w (pkexec exited 127)", ErrNoAuthAgent)
	done := doneOf(h.handle(StepExited{Seq: h.seq(), Err: err, NotGranted: true}))
	for _, id := range []string{"o", "p", "t", "j"} {
		if d := done[id]; d.Status != steps.AuthRefused || d.Err != ErrNoAuthAgent.Error() {
			t.Errorf("%s: %+v, want AuthRefused with %q", id, d, ErrNoAuthAgent.Error())
		}
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunLocked: a locked account refuses every part, no dialog.
func TestCombinedRunLocked(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(true)
	o := h.handle(FaillockRead{Failures: 3, Deny: 3, Unlock: t0.Add(time.Minute)})
	if _, ok := find[AuthWaiting](o.Events); ok {
		t.Error("waited on polkit for a locked account")
	}
	if n := len(all[StepDone](o.Events)); n != 5 {
		t.Errorf("settled %d steps, want 5", n)
	}
	if s := effect[StartStep](t, o); s.Step.ID != "after" {
		t.Errorf("started %q", s.Step.ID)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunEndsEarly: a call that ends before its last parts (the
// helper failed at its lock) fails the running part and every part it
// never reached, each with the call's error.
func TestCombinedRunEndsEarly(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	o := h.handle(StepExited{Seq: seq, Err: errors.New("root helper (exit 5): pacman is running")})
	done := doneOf(o)
	for _, id := range []string{"o", "p", "w", "t", "j"} {
		if d := done[id]; d.Status != steps.Failed || d.Err != "root helper (exit 5): pacman is running" {
			t.Errorf("%s: %+v", id, d)
		}
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunAbort: an Abort mid-call stops it and ends the action;
// the parts not reached are not run.
func TestCombinedRunAbort(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	h.handle(PartDone{Seq: seq, Step: "o"})
	o := h.abort() // between two parts
	if s := effect[Stop](t, o); s.Seq != seq {
		t.Errorf("Stop = %+v", s)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeAborted || fin.NotRun != 5 {
		t.Errorf("Finished = %+v", fin)
	}
	noOutput(t, "the call's late exit", h.s.Handle(StepExited{Seq: seq}))
	enginetest.CheckContract(t, h.evs)
}

// TestCombineNeedsTwo: a lone combinable step, a dry run, sudo and a step
// with an OnAccept all run step by step, as before.
func TestCombineNeedsTwo(t *testing.T) {
	hook := part("p")
	hook.OnAccept = func() error { return nil }
	for name, c := range map[string]struct {
		opts  Options
		queue []steps.Step
	}{
		"lone":     {Options{Auth: AuthPolkit}, []steps.Step{part("p"), child("b", "true")}},
		"onaccept": {Options{Auth: AuthPolkit}, []steps.Step{hook, part("t")}},
		"sudo":     {Options{Auth: AuthSudo, DryRun: true}, []steps.Step{part("p"), part("t")}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, c.opts, queue("q", c.queue...))
			o := h.start("q")
			if c.opts.Auth == AuthPolkit && !c.opts.DryRun {
				o = h.handle(FaillockRead{Deny: 3})
			}
			if st := effect[StartStep](t, o); len(st.Step.Parts) != 0 || st.Step.ID != c.queue[0].ID {
				t.Errorf("StartStep = %+v", st.Step)
			}
		})
	}
}

// TestCombinedDryRun: a dry run under polkit takes the real run's route,
// one combined call with its parts in order and each step's own row, but
// asks nothing: every confirm first as ever, then no faillock read and no
// polkit wait, the StartStep marked as a dry run for the Driver to report.
func TestCombinedDryRun(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit, DryRun: true}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok() // "before"
	h.confirm(true)
	o := h.confirm(true)
	if _, ok := find[ReadFaillock](o.Effects); ok {
		t.Fatal("a dry run read the faillock count")
	}
	if _, ok := find[AuthWaiting](o.Events); ok {
		t.Fatal("a dry run waited for polkit")
	}
	st := effect[StartStep](t, o)
	var got []string
	for _, p := range st.Step.Parts {
		got = append(got, p.ID)
	}
	if st.Step.Verb != "all" || !st.Env.DryRun || !slices.Equal(got, []string{"o", "p", "w", "t", "j"}) {
		t.Fatalf("StartStep = verb %q, dry %v, parts %q", st.Step.Verb, st.Env.DryRun, got)
	}
	seq := st.Seq
	h.handle(PartDone{Seq: seq, Step: "o"})
	for _, id := range []string{"p", "w", "t"} {
		h.handle(PartStarted{Seq: seq, Step: id})
		h.handle(PartDone{Seq: seq, Step: id})
	}
	h.handle(PartStarted{Seq: seq, Step: "j"})
	o = h.handle(StepExited{Seq: seq})
	if s := effect[StartStep](t, o); s.Step.ID != "after" {
		t.Errorf("next started %q", s.Step.ID)
	}
	h.ok()
	if fin := event[Finished](t, h.out); fin.Errs != 0 || fin.Refused != 0 {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}
