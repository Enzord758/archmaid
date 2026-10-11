package engine_test

import (
	"errors"
	"reflect"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A Cancel in archmaid's own polkit dialog (the agent's prompt,
// AskpassRequest.Polkit, cancelled with CancelPrompt) is the user's no, not
// a refusal: pkexec still exits 127 (NotGranted), but the step(s) the
// dialog covered end Skipped, the rest of the queue is skipped as behind a
// declined required step, and the action ends cancelled when nothing ran
// (aborted otherwise), with Refused 0 and steps_refused 0 in last-run.json.
// A refusal the user didn't make (no prompt of ours: a wrong password at
// the desktop's agent, its Cancel, no agent; three wrong passwords at
// ours) stays AuthRefused.

var exit127 = errors.New("exit status 127")

// agentPrompt has polkit's agent ask for the running helper step.
func (h *harness) agentPrompt(req uint64) NeedsPassword {
	h.t.Helper()
	return event[NeedsPassword](h.t, h.handle(AskpassRequest{Req: req, Prompt: "Password: ", Polkit: true,
		Requester: "polkit's agent (pkttyagent)", Action: "org.archmaid.helper.disable-unit"}))
}

// lastRun is the record the last call's WriteLastRun carried.
func (h *harness) lastRun() logstore.RunRecord {
	h.t.Helper()
	return effect[WriteLastRun](h.t, h.out).Record
}

// TestSessionPolkitCancelNothingRan: Disable unused services' one helper
// step, its dialog cancelled: cancelled, as a cancelled sudo password
// prompt or a declined gate, not "not authorised".
func TestSessionPolkitCancelNothingRan(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "disable-unit")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	np := h.agentPrompt(1)
	o := h.cancel(np.ID)
	if !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 1}}) {
		t.Errorf("cancel: effects %#v, want only the hang-up", o.Effects)
	}
	if len(o.Events) != 0 {
		t.Errorf("cancel: events %#v, want none (no polkit wait reopened: nothing waits for authorisation any more)", o.Events)
	}
	o = h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	if _, ok := find[AuthResolved](o.Events); ok {
		t.Error("the polkit wait, set aside under the prompt, was resolved a second time")
	}
	if d := doneOf(o)["h"]; d.Status != steps.Skipped || d.Err != "" {
		t.Errorf("StepDone = %+v, want Skipped (the user's cancel), not AuthRefused", d)
	}
	if !hasLog(o, "SKIP: h done (authorisation cancelled)") {
		t.Errorf("logs = %+v", all[LogWritten](o.Events))
	}
	fin := event[Finished](t, o)
	if fin.Outcome != OutcomeCancelled || fin.Refused != 0 || fin.Errs != 0 || fin.Skipped != 1 || fin.NotRun != 0 {
		t.Errorf("Finished = %+v, want cancelled with nothing refused", fin)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeCancelled || rec.StepsRefused != 0 || rec.StepsSkipped != 1 || rec.StepsFailed != 0 {
		t.Errorf("record = %+v, want cancelled with steps_refused 0", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestSessionPolkitCancelAfterSteps: a cancel after another step ran ends
// the queue as a declined required step does: the steps behind it are
// skipped and the run is aborted, still with nothing refused.
func TestSessionPolkitCancelAfterSteps(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", child("t", "topgrade"), helper("h", "v"), child("b", "true")))
	h.start("q")
	h.ok() // t
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	done := doneOf(o)
	if done["h"].Status != steps.Skipped || done["b"].Status != steps.Skipped {
		t.Errorf("StepDone = %+v, want h and b skipped", done)
	}
	if _, ok := find[StartStep](o.Effects); ok {
		t.Error("a step started after the user cancelled the dialog")
	}
	fin := event[Finished](t, o)
	if fin.Outcome != OutcomeAborted || fin.Refused != 0 || fin.Skipped != 2 {
		t.Errorf("Finished = %+v, want aborted, nothing refused", fin)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeAborted || rec.StepsRefused != 0 || rec.StepsSkipped != 2 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunCancelled: Full Cleanup's one dialog cancelled: every
// step it covered is Skipped (not AuthRefused), and so is everything
// behind it; the user step before it ran, so the run is aborted.
func TestCombinedRunCancelled(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", combinedQueue()...))
	h.start("q")
	h.ok() // before
	h.confirm(true)
	h.confirm(false) // w declined
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	done := doneOf(o)
	for _, id := range []string{"o", "p", "t", "j", "after"} {
		if done[id].Status != steps.Skipped {
			t.Errorf("%s: %+v, want Skipped", id, done[id])
		}
	}
	if _, ok := done["w"]; ok {
		t.Error("the declined step was settled again")
	}
	if !hasLog(o, "SKIP: j done (authorisation cancelled)") || hasLog(o, "SKIP: after done (authorisation cancelled)") {
		t.Errorf("logs = %+v (the parts the dialog covered, only)", all[LogWritten](o.Events))
	}
	if _, ok := find[StartStep](o.Effects); ok {
		t.Error("the queue went on after the user cancelled the dialog")
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeAborted || fin.Refused != 0 {
		t.Errorf("Finished = %+v", fin)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeAborted || rec.StepsRefused != 0 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestCombinedRunCancelledNothingRan: the same with nothing run before
// the call: cancelled.
func TestCombinedRunCancelledNothingRan(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", part("p"), part("t")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled || fin.Refused != 0 || fin.Skipped != 2 {
		t.Errorf("Finished = %+v", fin)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeCancelled || rec.StepsRefused != 0 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestSessionPolkitRefusalsStayRefused: what the user didn't cancel in
// archmaid's dialog is still "not authorised": a refusal with no prompt of
// ours (the desktop agent's wrong password or Cancel, no agent at all),
// three wrong passwords at ours, and a prompt the step's own end withdrew.
func TestSessionPolkitRefusalsStayRefused(t *testing.T) {
	for _, c := range []string{"no prompt", "three wrong", "withdrawn"} {
		t.Run(c, func(t *testing.T) {
			h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), child("b", "true")))
			h.start("q")
			h.handle(FaillockRead{Deny: 3})
			seq := h.seq()
			switch c {
			case "three wrong":
				h.agentPrompt(1)
				for i := 0; i < 3; i++ {
					h.password("wrong")
					h.handle(AskpassAnswered{Req: 1})
				}
				if !reflect.DeepEqual(h.out.Effects, []Effect{HangUpAskpass{Req: 1}}) {
					t.Fatalf("third wrong: %#v", h.out.Effects)
				}
			case "withdrawn":
				h.agentPrompt(1) // still open when pkexec exits
			}
			o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
			if d := doneOf(o)["h"]; d.Status != steps.AuthRefused {
				t.Errorf("StepDone = %+v, want AuthRefused", d)
			}
			if s := effect[StartStep](t, o); s.Step.ID != "b" {
				t.Errorf("next %q, want the queue to go on", s.Step.ID)
			}
			if fin := event[Finished](t, h.ok()); fin.Refused != 1 || fin.Outcome != OutcomeCompleted {
				t.Errorf("Finished = %+v", fin)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestSessionPolkitCancelThenGranted: should polkit grant the step anyway
// (the cancel came too late to stop it), the step's result stands, and a
// later helper step's refusal is a refusal again.
func TestSessionPolkitCancelThenGranted(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v"), helper("k", "w")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	h.handle(PrivilegeGranted{Seq: seq})
	o := h.handle(StepExited{Seq: seq})
	if d := doneOf(o)["h"]; d.Status != steps.OK {
		t.Errorf("h: %+v", d)
	}
	o = h.handle(FaillockRead{Deny: 3})
	o = h.handle(StepExited{Seq: effect[StartStep](t, o).Seq, Err: exit127, NotGranted: true})
	if d := doneOf(o)["k"]; d.Status != steps.AuthRefused {
		t.Errorf("k: %+v, want AuthRefused (no cancel of its own)", d)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestSessionPolkitCancelHangsUpLaterPrompts: once the user cancelled the
// step's dialog, another prompt for the same step isn't shown: it is hung
// up on at once.
func TestSessionPolkitCancelHangsUpLaterPrompts(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", "v")))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.cancel(h.agentPrompt(1).ID)
	o := h.handle(AskpassRequest{Req: 2, Prompt: "Password: ", Polkit: true})
	if _, ok := find[NeedsPassword](o.Events); ok || !reflect.DeepEqual(o.Effects, []Effect{HangUpAskpass{Req: 2}}) {
		t.Errorf("a second prompt after the cancel: %#v", o)
	}
	h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	enginetest.CheckContract(t, h.evs)
}
