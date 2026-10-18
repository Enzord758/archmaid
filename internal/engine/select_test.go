package engine_test

import (
	"context"
	"slices"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// selection starts the services action and answers its discovery with a
// pick list; got collects what the queue builder was handed.
func selection(t *testing.T, got *[]string) (*harness, NeedsSelection) {
	t.Helper()
	a := actions.Action{ID: actions.IDServices, Discover: func(context.Context, actions.Env) actions.Discovery { return nil }}
	h := newHarness(t, Options{}, a)
	seq := effect[Discover](t, h.start(actions.IDServices)).Seq
	o := h.handle(Discovered{Seq: seq, Discovery: actions.NeedsSelection{
		Items: []string{"a.service", "b\x1b[2J.service", "c.service"},
		Steps: func(sel []string) []steps.Step {
			*got = slices.Clone(sel)
			if len(sel) == 0 {
				return nil
			}
			return []steps.Step{sudo(child("disable", append([]string{"sudo", "systemctl", "disable"}, sel...)...))}
		},
	}})
	return h, event[NeedsSelection](t, o)
}

func TestSessionSelection(t *testing.T) {
	var got []string
	h, ns := selection(t, &got)
	shownB := ns.Items[1]
	if ns.ID == 0 || ns.Title != "Enabled but not running — select services to disable" ||
		len(ns.Items) != 3 || ns.Items[0] != "a.service" || shownB == "b\x1b[2J.service" {
		t.Errorf("NeedsSelection = %+v, want the items one line each, escapes made harmless", ns)
	}
	noOutput(t, "a Select for another ID", h.s.Select(ns.ID+1, []string{"a.service"}))
	noOutput(t, "a Confirm on a selection", h.s.Confirm(ns.ID, true))

	// Picks are taken in the list's order, once each, by what was shown;
	// anything else is ignored.
	o := h.do(h.s.Select(ns.ID, []string{"c.service", shownB, "zzz.service", "c.service"}))
	if !slices.Equal(got, []string{"b\x1b[2J.service", "c.service"}) {
		t.Errorf("the queue was built from %q", got)
	}
	effect[Probe](t, o) // the picked queue needs root
	noOutput(t, "the answered selection again", h.s.Select(ns.ID, []string{"a.service"}))
	event[QueueStarted](t, h.handle(ProbeDone{Cached: true}))
	o = h.ok()
	finished(t, o, Finished{Action: actions.IDServices, Outcome: OutcomeCompleted})
	effect[WriteLastRun](t, o)
	enginetest.CheckContract(t, h.evs)
}

// Picking nothing, or only what wasn't offered, runs nothing and ends
// cancelled with no last-run record, as the TUI does; so does cancelling.
func TestSessionSelectionNothingPicked(t *testing.T) {
	for name, answer := range map[string]func(*harness, NeedsSelection) Output{
		"empty":   func(h *harness, ns NeedsSelection) Output { return h.do(h.s.Select(ns.ID, nil)) },
		"unknown": func(h *harness, ns NeedsSelection) Output { return h.do(h.s.Select(ns.ID, []string{"zzz"})) },
		"cancel":  func(h *harness, ns NeedsSelection) Output { return h.cancel(ns.ID) },
	} {
		t.Run(name, func(t *testing.T) {
			var got []string
			h, ns := selection(t, &got)
			o := answer(h, ns)
			finished(t, o, Finished{Action: actions.IDServices, Outcome: OutcomeCancelled})
			if len(o.Effects) != 0 {
				t.Errorf("effects = %#v, want none (no probe, no record)", o.Effects)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

func TestSessionSelectionAborted(t *testing.T) {
	var got []string
	h, ns := selection(t, &got)
	o := h.abort()
	if pc := event[PromptClosed](t, o); pc.ID != ns.ID {
		t.Errorf("PromptClosed = %+v", pc)
	}
	finished(t, o, Finished{Action: actions.IDServices, Outcome: OutcomeCancelled})
	noOutput(t, "the withdrawn selection", h.s.Select(ns.ID, []string{"a.service"}))
	enginetest.CheckContract(t, h.evs)
}
