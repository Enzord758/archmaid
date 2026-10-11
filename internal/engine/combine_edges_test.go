package engine_test

import (
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestCombinedPartDoneEdges: a PartDone that doesn't belong to the running
// part changes nothing: one for another call, one for a part that isn't the
// running one, and a second one for a part already ended. The real one still
// settles the row.
func TestCombinedPartDoneEdges(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	noOutput(t, "a PartDone of another call", h.s.Handle(PartDone{Seq: seq + 9, Step: "o"}))
	noOutput(t, "a PartDone of a part that isn't running", h.s.Handle(PartDone{Seq: seq, Step: "p"}))
	if d := doneOf(h.handle(PartDone{Seq: seq, Step: "o"}))["o"]; d.Status != steps.OK {
		t.Fatalf("the running part: %+v", d)
	}
	noOutput(t, "a second PartDone with no part running", h.s.Handle(PartDone{Seq: seq, Step: "o"}))
}

// TestCombinedExitLeavesUnstartedPartsFailed: a call that exits cleanly
// (no error) before it started the later parts has not run them: each of
// those is Failed with the reason, not marked OK.
func TestCombinedExitLeavesUnstartedPartsFailed(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok()
	h.confirm(true)
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	if d := doneOf(h.handle(PartDone{Seq: seq, Step: "o"}))["o"]; d.Status != steps.OK {
		t.Errorf("o ran: %+v", d)
	}
	done := doneOf(h.handle(StepExited{Seq: seq}))
	if _, again := done["o"]; again {
		t.Error("o was settled a second time")
	}
	for _, id := range []string{"p", "w", "t", "j"} {
		if d := done[id]; d.Status != steps.Failed || d.Err != "the root helper ended before this step ran" {
			t.Errorf("%s: %+v, want Failed with the not-run reason", id, d)
		}
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedEveryConfirmDeclined: when the user declines every combined
// step there is no call: no faillock read, no StartStep for the verb, and the
// queue goes on with the next user step.
func TestCombinedEveryConfirmDeclined(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", confirmed("a"), confirmed("b"), child("after", "true")))
	o := h.start("q")
	if nc := event[NeedsConfirm](t, o); nc.Step != "a" {
		t.Fatalf("first confirm for %q", nc.Step)
	}
	o = h.confirm(false)
	if nc := event[NeedsConfirm](t, o); nc.Step != "b" {
		t.Fatalf("second confirm for %q", nc.Step)
	}
	o = h.confirm(false)
	if _, ok := find[ReadFaillock](o.Effects); ok {
		t.Error("read the faillock count with nothing to run")
	}
	if s := effect[StartStep](t, o); s.Step.ID != "after" || s.Step.Verb != "" {
		t.Errorf("started %+v, want the user step after the declined run", s.Step)
	}
	h.ok()
	if fin := event[Finished](t, h.out); fin.Outcome != OutcomeCompleted {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}
