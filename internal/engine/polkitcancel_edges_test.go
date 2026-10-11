package engine_test

import (
	"errors"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Edge cases around the polkit Cancel (polkitcancel_test.go has the main
// ones): where in the queue it falls, what races it, what follows it.

// cancelAt drives the queue to the helper step named id: the earlier steps
// are granted and run, then id's dialog is cancelled and its pkexec exits
// 127.
func cancelAt(t *testing.T, qs []steps.Step, id string) (*harness, Output) {
	t.Helper()
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", qs...))
	h.start("q")
	for _, st := range qs {
		var seq uint64
		if st.NeedsPriv {
			h.handle(FaillockRead{Deny: 3})
			seq = h.seq()
		}
		if st.ID == id {
			break
		}
		if st.NeedsPriv {
			h.handle(PrivilegeGranted{Seq: seq})
			h.handle(StepExited{Seq: seq})
		} else {
			h.ok()
		}
	}
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	return h, h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
}

// TestPolkitCancelPosition: the first, a middle and the last root step of a
// queue: the ones before it keep their result, it and the ones behind are
// skipped, nothing is refused, and the record agrees with Finished.
// Cancelled only when nothing ran.
func TestPolkitCancelPosition(t *testing.T) {
	qs := func() []steps.Step {
		return []steps.Step{helper("h1", "v1"), child("c", "true"), helper("h2", "v2"), helper("h3", "v3")}
	}
	cases := []struct {
		at      string
		ok      []string
		skipped []string
		outcome Outcome
		rec     string
	}{
		{"h1", nil, []string{"h1", "c", "h2", "h3"}, OutcomeCancelled, logstore.OutcomeCancelled},
		{"h2", []string{"h1", "c"}, []string{"h2", "h3"}, OutcomeAborted, logstore.OutcomeAborted},
		{"h3", []string{"h1", "c", "h2"}, []string{"h3"}, OutcomeAborted, logstore.OutcomeAborted},
	}
	for _, c := range cases {
		t.Run(c.at, func(t *testing.T) {
			h, o := cancelAt(t, qs(), c.at)
			done := doneOf(o)
			for _, id := range c.skipped {
				if done[id].Status != steps.Skipped {
					t.Errorf("%s: %+v, want Skipped", id, done[id])
				}
			}
			for _, id := range c.ok {
				if _, again := done[id]; again {
					t.Errorf("%s ran and was settled again: %+v", id, done[id])
				}
			}
			fin := event[Finished](t, o)
			if fin.Outcome != c.outcome || fin.Refused != 0 || fin.Errs != 0 || fin.Skipped != len(c.skipped) || fin.NotRun != 0 {
				t.Errorf("Finished = %+v", fin)
			}
			rec := h.lastRun()
			if rec.Outcome != c.rec || rec.StepsRefused != 0 || rec.StepsSkipped != len(c.skipped) || rec.StepsFailed != 0 {
				t.Errorf("record = %+v", rec)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPolkitCancelRaces: the cancel and the step's own end cross. A late
// CancelPrompt (the prompt is gone with the step) changes nothing and never
// marks the next helper step; an exit that is no refusal after a cancel
// stands as it is, and the flag doesn't survive into the next step.
func TestPolkitCancelRaces(t *testing.T) {
	t.Run("late cancel after the step ended", func(t *testing.T) {
		h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), helper("k", "w")))
		h.start("q")
		h.handle(FaillockRead{Deny: 3})
		seq := h.seq()
		np := h.agentPrompt(1)
		// pkexec exits 127 first (the prompt withdrawn), then the click lands.
		o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
		if d := doneOf(o)["h"]; d.Status != steps.AuthRefused {
			t.Fatalf("h: %+v, want AuthRefused (withdrawn prompt)", d)
		}
		if o := h.cancel(np.ID); len(o.Events) != 0 || len(o.Effects) != 0 {
			t.Errorf("late cancel: %#v, want nothing", o)
		}
		h.handle(FaillockRead{Deny: 3})
		o = h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true})
		if d := doneOf(o)["k"]; d.Status != steps.AuthRefused {
			t.Errorf("k: %+v, want AuthRefused: a stale cancel is not k's cancel", d)
		}
		if fin := event[Finished](t, o); fin.Refused != 2 || fin.Outcome != OutcomeCompleted {
			t.Errorf("Finished = %+v", fin)
		}
		enginetest.CheckContract(t, h.evs)
	})
	t.Run("exit fails after cancel", func(t *testing.T) {
		h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), helper("k", "w")))
		h.start("q")
		h.handle(FaillockRead{Deny: 3})
		seq := h.seq()
		h.cancel(h.agentPrompt(1).ID)
		h.handle(PrivilegeGranted{Seq: seq}) // polkit granted just before the click took effect
		o := h.handle(StepExited{Seq: seq, Err: errors.New("exit status 1")})
		if d := doneOf(o)["h"]; d.Status != steps.Failed {
			t.Errorf("h: %+v, want Failed (it ran), not Skipped", d)
		}
		if _, ok := find[Finished](o.Events); ok {
			t.Fatal("the queue ended: the failed step's successor must still run")
		}
		h.handle(FaillockRead{Deny: 3})
		o = h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true})
		if d := doneOf(o)["k"]; d.Status != steps.AuthRefused {
			t.Errorf("k: %+v, want AuthRefused (the flag must not survive h)", d)
		}
		enginetest.CheckContract(t, h.evs)
	})
	t.Run("kept authorisation: no prompt, so a refusal is not a cancel", func(t *testing.T) {
		h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), helper("k", "w")))
		h.start("q")
		seq := h.handle(FaillockRead{Deny: 3})
		h.handle(PrivilegeGranted{Seq: effect[StartStep](t, seq).Seq})
		h.handle(StepExited{Seq: effect[StartStep](t, seq).Seq})
		h.handle(FaillockRead{Deny: 3})
		o := h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true})
		if d := doneOf(o)["k"]; d.Status != steps.AuthRefused {
			t.Errorf("k: %+v, want AuthRefused", d)
		}
		fin := event[Finished](t, o)
		if fin.Outcome != OutcomeCompleted || fin.Refused != 1 {
			t.Errorf("Finished = %+v", fin)
		}
		if rec := h.lastRun(); rec.StepsRefused != 1 {
			t.Errorf("record = %+v", rec)
		}
	})
}

// TestPolkitCancelThenAbort: Abort straight after the cancel (before pkexec's
// exit) ends the run aborted with the step Aborted, never refused; pkexec's
// late exit is ignored; so is a second Abort.
func TestPolkitCancelThenAbort(t *testing.T) {
	for _, before := range []bool{false, true} {
		name := "nothing ran before"
		if before {
			name = "a step ran before"
		}
		t.Run(name, func(t *testing.T) {
			qs := []steps.Step{helper("h", "v"), child("b", "true")}
			if before {
				qs = append([]steps.Step{child("a", "true")}, qs...)
			}
			h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", qs...))
			h.start("q")
			if before {
				h.ok()
			}
			h.handle(FaillockRead{Deny: 3})
			seq := h.seq()
			h.cancel(h.agentPrompt(1).ID)
			o := h.abort()
			if ab := event[Aborted](t, o); ab.ID != "h" {
				t.Errorf("Aborted = %+v", ab)
			}
			if _, ok := find[StepDone](o.Events); ok {
				t.Error("a StepDone after Abort")
			}
			fin := event[Finished](t, o)
			if fin.Outcome != OutcomeAborted || fin.Refused != 0 {
				t.Errorf("Finished = %+v", fin)
			}
			if rec := h.lastRun(); rec.Outcome != logstore.OutcomeAborted || rec.StepsRefused != 0 {
				t.Errorf("record = %+v", rec)
			}
			if o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true}); len(o.Events) != 0 || len(o.Effects) != 0 {
				t.Errorf("pkexec's exit after Abort: %#v", o)
			}
			if o := h.abort(); len(o.Events) != 0 {
				t.Errorf("second Abort: %#v", o)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPolkitCancelNewRunClean: the cancel leaves nothing behind for the next
// run on the same Session: its helper step's refusal is a refusal.
func TestPolkitCancelNewRunClean(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	h.cancel(h.agentPrompt(1).ID)
	h.abort() // the flag is set, pkexec's exit never came
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	o := h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true})
	if d := doneOf(o)["h"]; d.Status != steps.AuthRefused {
		t.Errorf("h: %+v, want AuthRefused in the new run", d)
	}
}

// TestPolkitCancelMixedRun: a step failed, one root step was refused (the
// desktop's agent), then the user cancelled at archmaid's dialog for the
// next. The run is aborted (the failed step ran) and says all three truths:
// 1 failed, 1 refused, the cancelled step and what was behind it skipped.
func TestPolkitCancelMixedRun(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", child("f", "false"), helper("r", "v"), helper("c", "w"), child("b", "true")))
	h.start("q")
	h.exit(errors.New("exit status 1")) // f fails
	h.handle(FaillockRead{Deny: 3})
	h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true}) // r refused (no prompt of ours)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	done := doneOf(o)
	if done["c"].Status != steps.Skipped || done["b"].Status != steps.Skipped {
		t.Errorf("StepDone = %+v", done)
	}
	fin := event[Finished](t, o)
	if fin.Outcome != OutcomeAborted || fin.Errs != 1 || fin.Refused != 1 || fin.Skipped != 3 {
		t.Errorf("Finished = %+v, want aborted, 1 failed, 1 refused, 3 skipped (r, c, b)", fin)
	}
	rec := h.lastRun()
	if rec.Outcome != logstore.OutcomeAborted || rec.StepsFailed != 1 || rec.StepsRefused != 1 || rec.StepsSkipped != 3 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestPolkitCancelAfterRefusalNothingRan: a refused root step (not started),
// then a cancel: nothing ran, so cancelled, and the earlier refusal is still
// counted in both Finished and the record.
func TestPolkitCancelAfterRefusalNothingRan(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("r", "v"), helper("c", "w")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	h.handle(StepExited{Seq: h.seq(), Err: exit127, NotGranted: true})
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	fin := event[Finished](t, o)
	rec := h.lastRun()
	t.Logf("Finished = %+v record = %+v", fin, rec)
	if fin.Outcome != OutcomeCancelled || fin.Refused != 1 || fin.Skipped != 2 {
		t.Errorf("Finished = %+v", fin)
	}
	if rec.Outcome != logstore.OutcomeCancelled || rec.StepsRefused != 1 || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
}

// TestCombinedCancelPartsAlreadyRan: the call's dialog is cancelled but a
// part was already reported done: what ran keeps its result, and only the
// parts that never ran are skipped.
func TestCombinedCancelPartsAlreadyRan(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", part("p"), part("t"), part("j")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	h.handle(PartDone{Seq: seq, Step: "p"})
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	done := doneOf(o)
	if d, ok := done["p"]; ok {
		t.Errorf("p ran and was reported done earlier, but is settled again: %+v", d)
	}
	fin := event[Finished](t, o)
	rec := h.lastRun()
	if fin.Outcome != OutcomeAborted || fin.Refused != 0 || fin.Skipped != 2 {
		t.Errorf("Finished = %+v, want aborted, 2 skipped (t, j): p ran", fin)
	}
	if rec.StepsSkipped != 2 || rec.StepsRefused != 0 {
		t.Errorf("record = %+v", rec)
	}
}

// TestCombinedCancelThenAbort: the same Abort right behind the cancel for
// the combined call.
func TestCombinedCancelThenAbort(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", child("a", "true"), part("p"), part("t")))
	h.start("q")
	h.ok()
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.abort()
	rec := h.lastRun()
	fin := event[Finished](t, o)
	if fin.Outcome != OutcomeAborted || fin.Refused != 0 {
		t.Errorf("Finished = %+v", fin)
	}
	if o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true}); len(o.Events) != 0 {
		t.Errorf("late exit after Abort: %#v", o.Events)
	}
	if rec.StepsRefused != 0 || rec.Outcome != logstore.OutcomeAborted {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}
