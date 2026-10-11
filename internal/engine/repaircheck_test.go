package engine_test

import (
	"errors"
	"reflect"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// repairCheck runs Repair's polkit recheck over foo and bar up to its exit:
// the faillock read, the helper started and granted. It returns the step's
// Seq.
func repairCheck(t *testing.T) (*harness, uint64) {
	t.Helper()
	a := queue(actions.IDRepair, actions.RepairCheckStep([]string{"foo", "bar"}))
	a.Record = logstore.Repair // as the menu's Repair
	h := newHarness(t, Options{Auth: AuthPolkit}, a)
	h.start(actions.IDRepair)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	return h, seq
}

// The helper's check event decides what follows the recheck: the reinstall
// of what it found broken, inserted with its own confirm.
func TestSessionRepairCheckFoundBroken(t *testing.T) {
	h, seq := repairCheck(t)
	noOutput(t, "a stale check", h.s.Handle(HelperCheck{Seq: seq + 1, Broken: []string{"foo"}}))
	noOutput(t, "the check event", h.handle(HelperCheck{Seq: seq, Broken: []string{"bar"}}))
	o := h.handle(StepExited{Seq: seq})
	if got := event[StepDone](t, o); got.Status != steps.OK {
		t.Errorf("StepDone = %+v", got)
	}
	want := StepInserted{Index: 1, Step: QueueStepOf(actions.RepairStep([]string{"bar"}))}
	if got := event[StepInserted](t, o); !reflect.DeepEqual(got, want) {
		t.Errorf("StepInserted = %+v\nwant %+v", got, want)
	}
	if nc := event[NeedsConfirm](t, o); nc.Step != steps.IDRepair || nc.Msg != "Reinstall 1 broken package(s)?" {
		t.Errorf("NeedsConfirm = %+v", nc)
	}
	o = h.confirm(true)
	if st := effect[ReadFaillock](t, o); st != (ReadFaillock{}) {
		t.Errorf("the reinstall didn't go through the polkit path: %#v", o.Effects)
	}
	h.handle(FaillockRead{Deny: 3})
	o = h.ok()
	finished(t, o, Finished{Action: actions.IDRepair, Outcome: OutcomeCompleted})
	enginetest.CheckContract(t, h.evs)
}

// Nothing broken, a failed or silent check, or one naming packages it
// wasn't asked about: the action ends in Repair's notice, with no last-run
// record (as the sudo rescan's notices).
func TestSessionRepairCheckNotices(t *testing.T) {
	for _, c := range []struct {
		name   string
		check  *HelperCheck
		err    error
		status steps.Status
		notice string
	}{
		{"none broken", &HelperCheck{}, nil, steps.OK, "No broken packages: the 2 flagged as the user (foo, bar) are complete as root."},
		{"failed", &HelperCheck{Broken: []string{"foo"}}, errors.New("exit status 1"), steps.Failed,
			"Couldn't scan for broken packages: exit status 1"},
		{"no check event", nil, nil, steps.OK,
			"Couldn't scan for broken packages: the root helper reported no result"},
		{"unasked names", &HelperCheck{Broken: []string{"foo", "evil"}}, nil, steps.OK,
			"Couldn't scan for broken packages: the root helper reported packages it wasn't asked about"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, seq := repairCheck(t)
			if c.check != nil {
				in := *c.check
				in.Seq = seq
				h.handle(in)
			}
			o := h.handle(StepExited{Seq: seq, Err: c.err})
			if got := event[StepDone](t, o); got.Status != c.status {
				t.Errorf("StepDone = %+v", got)
			}
			if _, ok := find[StepInserted](o.Events); ok {
				t.Error("inserted a reinstall")
			}
			if got := event[Notice](t, o); got.Text != c.notice {
				t.Errorf("Notice = %q, want %q", got.Text, c.notice)
			}
			errs := 0
			if c.err != nil {
				errs = 1
			}
			finished(t, o, Finished{Action: actions.IDRepair, Outcome: OutcomeNotice, Errs: errs})
			want, outcome := logstore.OutcomeFailed, DiscoveryFailed
			if c.name == "none broken" {
				want, outcome = logstore.OutcomeNothingFound, DiscoveryNothing
			}
			if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != want {
				t.Errorf("record = %+v, want %s", rec, want)
			}
			dr := event[DiscoveryResult](t, o)
			if dr.Outcome != outcome || dr.Summary != c.notice || dr.Category != logstore.Repair {
				t.Errorf("DiscoveryResult = %+v", dr)
			}
			if lw := event[LogWritten](t, o); lw.Category != logstore.Repair {
				t.Errorf("the record went to %q, want the discovery log", lw.Category)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// A check event for any other step is ignored, and the step goes on as
// before; so is the recheck in a dry run, which runs nothing.
func TestSessionHelperCheckElsewhere(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", helper("h", steps.VerbCheckFiles)))
	h.start("q")
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	noOutput(t, "a check for another step", h.s.Handle(HelperCheck{Seq: seq, Broken: []string{"x"}}))
	finished(t, h.handle(StepExited{Seq: seq}), Finished{Action: "q", Outcome: OutcomeCompleted})

	h = newHarness(t, Options{Auth: AuthPolkit, DryRun: true}, queue(actions.IDRepair, actions.RepairCheckStep([]string{"foo"})))
	h.start(actions.IDRepair)
	finished(t, h.ok(), Finished{Action: actions.IDRepair, Outcome: OutcomeCompleted})
	enginetest.CheckContract(t, h.evs)
}
