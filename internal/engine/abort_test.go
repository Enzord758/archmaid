package engine_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

func (h *harness) abort() Output { return h.do(h.s.Abort()) }

// finished checks the Finished event of o, and that it is the last event
// but for PhaseChanged.
func finished(t *testing.T, o Output, want Finished) {
	t.Helper()
	fin := event[Finished](t, o)
	fin.Elapsed = 0
	if fin != want {
		t.Errorf("Finished = %+v, want %+v", fin, want)
	}
	for _, ev := range o.Events[len(o.Events)-1:] {
		if _, ok := ev.(PhaseChanged); !ok {
			if _, ok := ev.(Finished); !ok {
				t.Errorf("last event %T, want Finished or PhaseChanged", ev)
			}
		}
	}
}

func TestSessionAbortIdle(t *testing.T) {
	h := newHarness(t, Options{})
	noOutput(t, "Abort when idle", h.abort())
}

// Abort during a running child: Stop, then Aborted for it, nothing for the
// steps behind it, and Finished counting them as not run. The record is the
// TUI's: aborted, with the aborted step and the pending ones skipped.
func TestSessionAbortRunningStep(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", child("a", "true"), sudo(child("p", "sudo", "x")), child("c", "true"), child("d", "true")))
	h.start("q")
	h.handle(ProbeDone{Cached: true})
	h.ok()
	seq := h.seq()
	h.wait(2 * time.Second)
	o := h.abort()
	if len(o.Effects) == 0 || o.Effects[0] != (Stop{Seq: seq}) {
		t.Fatalf("effects = %#v, want Stop{%d} first", o.Effects, seq)
	}
	if got := event[Aborted](t, o); got != (Aborted{ID: "p", Index: 1, Elapsed: 2 * time.Second}) {
		t.Errorf("Aborted = %+v", got)
	}
	if n := len(all[StepDone](o.Events)); n != 0 {
		t.Errorf("%d StepDone on abort, want none", n)
	}
	finished(t, o, Finished{Action: "q", Outcome: OutcomeAborted, NotRun: 2})
	if !reflect.DeepEqual(all[KeepAlive](o.Effects), []KeepAlive{{On: false}}) {
		t.Errorf("keep-alive not turned off: %#v", o.Effects)
	}
	rec := effect[WriteLastRun](t, o).Record
	if rec.Outcome != logstore.OutcomeAborted || rec.StepsSkipped != 3 || rec.StepsFailed != 0 || rec.StepsRefused != 0 {
		t.Errorf("record = %+v", rec)
	}
	if n := len(all[WriteLog](o.Effects)); n != 0 {
		t.Errorf("an abort wrote %d log entries; the TUI writes none", n)
	}
	noOutput(t, "the stopped step's late output", h.s.Handle(StepOutput{Seq: seq, Lines: []string{"x"}}))
	noOutput(t, "the stopped step's late exit", h.s.Handle(StepExited{Seq: seq}))
	noOutput(t, "a second Abort", h.abort())
	enginetest.CheckContract(t, h.evs)
}

// The first step aborted: the run is aborted, not cancelled, as in the TUI.
func TestSessionAbortFirstStep(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", inproc("audit"), child("b", "true")))
	h.start("q")
	seq := h.seq()
	o := h.abort()
	if o.Effects[0] != (Stop{Seq: seq}) {
		t.Errorf("effects = %#v", o.Effects)
	}
	event[Aborted](t, o)
	finished(t, o, Finished{Action: "q", Outcome: OutcomeAborted, NotRun: 1})
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeAborted || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
	noOutput(t, "the late result", h.s.Handle(StepResult{Seq: seq, Result: steps.Result{Output: "x"}}))
	enginetest.CheckContract(t, h.evs)
}

// An open confirm is withdrawn; no step is running, so there is nothing to
// stop and no Aborted.
func TestSessionAbortDuringConfirm(t *testing.T) {
	y := child("y", "true")
	y.Confirm = "Go on?"
	h := newHarness(t, Options{}, queue("q", child("a", "true"), y, child("z", "true")))
	h.start("q")
	o := h.ok()
	nc := event[NeedsConfirm](t, o)
	o = h.abort()
	if got := event[PromptClosed](t, o); got.ID != nc.ID {
		t.Errorf("PromptClosed = %+v, want ID %d", got, nc.ID)
	}
	if _, ok := find[Stop](o.Effects); ok {
		t.Error("stopped something with no step running")
	}
	if _, ok := find[Aborted](o.Events); ok {
		t.Error("Aborted with no step running")
	}
	finished(t, o, Finished{Action: "q", Outcome: OutcomeAborted, NotRun: 2})
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeAborted || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
	noOutput(t, "the withdrawn confirm", h.s.Confirm(nc.ID, true))
	enginetest.CheckContract(t, h.evs)

	// Before any step ran it is a cancel.
	h = newHarness(t, Options{}, queue("q", y, child("z", "true")))
	nc = event[NeedsConfirm](t, h.start("q"))
	o = h.abort()
	event[PromptClosed](t, o)
	finished(t, o, Finished{Action: "q", Outcome: OutcomeCancelled, NotRun: 2})
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// Before the queue: the gate, the password prompt, the probe and the
// password check end as a cancel, with the prefetch dropped and late
// answers ignored.
func TestSessionAbortBeforeTheQueue(t *testing.T) {
	cancelled := func(t *testing.T, h *harness, o Output) {
		t.Helper()
		finished(t, o, Finished{Action: "q", Outcome: OutcomeCancelled})
		if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeCancelled {
			t.Errorf("record = %+v", rec)
		}
		if _, ok := find[QueueStarted](h.evs); ok {
			t.Error("planned")
		}
		enginetest.CheckContract(t, h.evs)
	}
	pre := inproc("audit")
	pre.Prefetch = "aur"
	gated := queue("q", pre, sudo(child("p", "sudo", "x")))
	gated.Gate = "Proceed?"

	t.Run("gate", func(t *testing.T) {
		h := newHarness(t, Options{}, gated)
		h.start("q")
		nc := event[NeedsConfirm](t, h.handle(ProbeDone{Cached: true}))
		o := h.abort()
		if got := event[PromptClosed](t, o); got.ID != nc.ID {
			t.Errorf("PromptClosed = %+v", got)
		}
		if got := all[CancelPrefetch](o.Effects); len(got) != 1 {
			t.Errorf("prefetch drops = %v", got)
		}
		cancelled(t, h, o)
		noOutput(t, "the withdrawn gate", h.s.Confirm(nc.ID, true))
	})
	t.Run("password", func(t *testing.T) {
		h := newHarness(t, Options{}, gated)
		np := h.toPrompt("q")
		o := h.abort()
		if got := event[PromptClosed](t, o); got.ID != np.ID {
			t.Errorf("PromptClosed = %+v", got)
		}
		cancelled(t, h, o)
		noOutput(t, "the withdrawn prompt", h.s.SubmitPassword(np.ID, "pw"))
	})
	t.Run("probe", func(t *testing.T) {
		h := newHarness(t, Options{}, gated)
		h.start("q")
		cancelled(t, h, h.abort())
		noOutput(t, "the late probe", h.s.Handle(ProbeDone{Cached: true}))
	})
	t.Run("password check", func(t *testing.T) {
		h := newHarness(t, Options{}, gated)
		h.toPrompt("q")
		h.password("pw")
		o := h.abort()
		if got := event[AuthResolved](t, o); got != (AuthResolved{}) {
			t.Errorf("AuthResolved = %+v", got)
		}
		cancelled(t, h, o)
		noOutput(t, "the late verdict", h.s.Handle(AuthDone{OK: true}))
	})
}

// A discovery in flight is stopped; it never planned, so no record.
func TestSessionAbortDuringDiscovery(t *testing.T) {
	h := newHarness(t, Options{}, actions.Action{ID: "d", Discover: func(context.Context, actions.Env) actions.Discovery { return nil }})
	seq := effect[Discover](t, h.start("d")).Seq
	o := h.abort()
	if !reflect.DeepEqual(o.Effects, []Effect{Stop{Seq: seq}}) {
		t.Errorf("effects = %#v, want only Stop{%d}", o.Effects, seq)
	}
	finished(t, o, Finished{Action: "d", Outcome: OutcomeCancelled})
	noOutput(t, "the late discovery", h.s.Handle(Discovered{Seq: seq, Discovery: actions.Notice{Text: "n"}}))
	enginetest.CheckContract(t, h.evs)
}

// A sudo step with its askpass prompt open: the prompt is withdrawn and sudo
// hung up on, then the step is aborted.
func TestSessionAbortDuringAskpass(t *testing.T) {
	h := askpassRun(t, Options{})
	seq := h.seq()
	np := event[NeedsPassword](t, h.handle(ask(7)))
	o := h.abort()
	if got := event[PromptClosed](t, o); got.ID != np.ID {
		t.Errorf("PromptClosed = %+v", got)
	}
	if o.Effects[0] != (Stop{Seq: seq}) {
		t.Errorf("effects = %#v", o.Effects)
	}
	if got := all[HangUpAskpass](o.Effects); !reflect.DeepEqual(got, []HangUpAskpass{{Req: 7}}) {
		t.Errorf("hang-ups = %v", got)
	}
	event[Aborted](t, o)
	finished(t, o, Finished{Action: "q", Outcome: OutcomeAborted, NotRun: 2})
	noOutput(t, "the withdrawn prompt", h.s.SubmitPassword(np.ID, "pw"))
	enginetest.CheckContract(t, h.evs)
}

// A helper step waiting on polkit: the wait resolves (not granted) before
// the step's Aborted. One waiting on its faillock read hasn't started: it
// gets no event.
func TestSessionAbortDuringPolkit(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), child("b", "true")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	o := h.abort()
	ar := slices.IndexFunc(o.Events, func(ev Event) bool { _, ok := ev.(AuthResolved); return ok })
	ab := slices.IndexFunc(o.Events, func(ev Event) bool { _, ok := ev.(Aborted); return ok })
	if ar < 0 || ab < 0 || ar > ab || o.Events[ar] != (AuthResolved{}) {
		t.Errorf("events = %#v, want AuthResolved{} before Aborted", o.Events)
	}
	finished(t, o, Finished{Action: "q", Outcome: OutcomeAborted, NotRun: 1})
	enginetest.CheckContract(t, h.evs)

	h = newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v")))
	h.start("q")
	o = h.abort()
	if _, ok := find[Aborted](o.Events); ok {
		t.Error("Aborted for a step that never started")
	}
	finished(t, o, Finished{Action: "q", Outcome: OutcomeCancelled, NotRun: 1})
	noOutput(t, "the late faillock read", h.s.Handle(FaillockRead{Deny: 3}))
	enginetest.CheckContract(t, h.evs)
}
