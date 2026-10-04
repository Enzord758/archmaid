package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
)

// report starts the audit action and answers its discovery with r.
func report(t *testing.T, r actions.Report) (*harness, OutputReport) {
	t.Helper()
	a := actions.Action{ID: actions.IDAURAudit, Discover: func(context.Context, actions.Env) actions.Discovery { return nil }}
	h := newHarness(t, Options{}, a)
	seq := effect[Discover](t, h.start(actions.IDAURAudit)).Seq
	return h, event[OutputReport](t, h.handle(Discovered{Seq: seq, Discovery: r}))
}

var takeover = &audit.Result{Total: 3, Findings: []audit.Finding{
	{Name: "yay-bin\x1b[2J", High: true, Flags: []string{"maintainer changed: jguer → mallory"}, Reason: "maintainer jguer → mallory"},
	{Name: "foo-git", Flags: []string{"orphaned", "0 votes"}},
}}

func TestSessionReport(t *testing.T) {
	h, rep := report(t, actions.Report{Text: "2 checked\n\x1b[31m✖ yay-bin\x1b[0m", Audit: takeover})
	want := OutputReport{ID: rep.ID, Text: "2 checked\n✖ yay-bin", Audit: &AuditReport{
		Total: 3,
		Findings: []AuditFinding{
			{Name: rep.Audit.Findings[0].Name, High: true, Flags: []string{"maintainer changed: jguer → mallory"}, Reason: "maintainer jguer → mallory"},
			{Name: "foo-git", Flags: []string{"orphaned", "0 votes"}},
		},
	}}
	if rep.ID == 0 || !reflect.DeepEqual(rep, want) {
		t.Errorf("OutputReport = %+v\nwant %+v", rep, want)
	}
	if n := rep.Audit.Findings[0].Name; n == "yay-bin\x1b[2J" {
		t.Errorf("finding name %q kept its escape", n)
	}
	noOutput(t, "a Select on a report", h.s.Select(rep.ID, nil))
	noOutput(t, "a Confirm for another ID", h.s.Confirm(rep.ID+1, false))

	// Nothing to accept: yes dismisses, as no does.
	o := h.do(h.s.Confirm(rep.ID, true))
	finished(t, o, Finished{Action: actions.IDAURAudit, Outcome: OutcomeReport})
	if len(o.Effects) != 0 {
		t.Errorf("effects = %#v, want none (no hook, no last-run record)", o.Effects)
	}
	enginetest.CheckContract(t, h.evs)

	// A report that isn't an audit carries no Audit.
	_, rep = report(t, actions.Report{Text: "r"})
	if rep.Audit != nil {
		t.Errorf("Audit = %+v, want nil", rep.Audit)
	}
}

func TestSessionReportDismissed(t *testing.T) {
	for name, answer := range map[string]func(*harness, PromptID) Output{
		"no":     func(h *harness, id PromptID) Output { return h.do(h.s.Confirm(id, false)) },
		"cancel": func(h *harness, id PromptID) Output { return h.cancel(id) },
		"abort": func(h *harness, id PromptID) Output {
			o := h.abort()
			if pc := event[PromptClosed](h.t, o); pc.ID != id {
				h.t.Errorf("PromptClosed = %+v", pc)
			}
			return o
		},
	} {
		t.Run(name, func(t *testing.T) {
			accepted := false
			h, rep := report(t, actions.Report{Text: "r", Audit: takeover, Accept: func() error { accepted = true; return nil }})
			if !rep.Audit.CanAccept {
				t.Error("CanAccept false with an Accept")
			}
			o := answer(h, rep.ID)
			finished(t, o, Finished{Action: actions.IDAURAudit, Outcome: OutcomeReport})
			if _, ok := find[RunHook](o.Effects); ok || accepted {
				t.Error("a dismissed report accepted its findings")
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// Yes on a report with an Accept: the hook runs, then ReportAccepted says
// how the save went, then Finished.
func TestSessionReportAccepted(t *testing.T) {
	for _, c := range []struct {
		name  string
		err   error
		saved bool
		text  string
	}{
		{"saved", nil, true, "Maintainer changes accepted: they won't be flagged again."},
		{"not saved", errors.New("open snapshot: \x1b[2Jpermission denied"), false,
			"Snapshot not saved (open snapshot: permission denied): the changes stay flagged."},
	} {
		t.Run(c.name, func(t *testing.T) {
			ran := false
			h, rep := report(t, actions.Report{Text: "r", Audit: takeover, Accept: func() error { ran = true; return c.err }})
			o := h.do(h.s.Confirm(rep.ID, true))
			if len(o.Events) != 0 || len(o.Effects) != 1 {
				t.Fatalf("Confirm = %#v, want the RunHook alone", o)
			}
			hook := effect[RunHook](t, o)
			if hook.Step != "" || hook.Seq == 0 {
				t.Errorf("RunHook = %+v", hook)
			}
			noOutput(t, "a second yes", h.s.Confirm(rep.ID, true))
			noOutput(t, "a stale HookDone", h.s.Handle(HookDone{Seq: hook.Seq + 1}))
			o = h.handle(HookDone{Seq: hook.Seq, Err: hook.Fn()})
			if !ran {
				t.Error("RunHook's Fn isn't the report's Accept")
			}
			if len(o.Events) < 2 || o.Events[0] != (ReportAccepted{ID: rep.ID, Saved: c.saved, Text: c.text}) {
				t.Errorf("events = %#v, want ReportAccepted first", o.Events)
			}
			finished(t, o, Finished{Action: actions.IDAURAudit, Outcome: OutcomeReport})
			enginetest.CheckContract(t, h.evs)
		})
	}

	// An Abort while the hook runs ends the report; its HookDone is late.
	h, rep := report(t, actions.Report{Text: "r", Accept: func() error { return nil }})
	hook := effect[RunHook](t, h.do(h.s.Confirm(rep.ID, true)))
	o := h.abort()
	finished(t, o, Finished{Action: actions.IDAURAudit, Outcome: OutcomeReport})
	noOutput(t, "the late HookDone", h.s.Handle(HookDone{Seq: hook.Seq}))
	enginetest.CheckContract(t, h.evs)
}
