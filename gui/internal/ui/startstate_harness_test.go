package ui

// Task #250: what the next action's status and Stop say. A dry run's "DRY
// RUN" must not carry into a real action's discovery or plan phase, and Stop
// is hidden while any action's gate, confirm or pick is up (nothing has run;
// the sheet has its own Cancel), not only Packages'. Real widgets, a Driver
// that takes every command and answers none: the scenario feeds the events.

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// takeDriver takes every command and answers none.
type takeDriver struct{ engine.Driver }

func (takeDriver) Send(engine.Command) bool { return true }

func TestGUICovStartState(t *testing.T) { covChild(t, "cov-startstate") }

func init() {
	covScenarios["cov-startstate"] = covStartState
	covOptions["cov-startstate"] = func(o *Options, rep *report) {
		orig := o.NewDriver
		o.NewDriver = func(e engine.Options) engine.Driver { return takeDriver{orig(e)} }
	}
}

func covStartState(c *covEnv) {
	w := c.w
	c.quiet()
	dry := func(when string) {
		for _, s := range []string{w.statusText(), w.status.Text()} {
			c.ok(!strings.Contains(s, "DRY RUN"), "%s: status %q still says DRY RUN", when, s)
		}
	}
	stop := func(when string, want bool) {
		c.ok(w.stopBtn.IsVisible() == want && w.stopBtn.Sensitive() == want, "%s: Stop visible %v, want %v", when, w.stopBtn.IsVisible(), want)
	}
	step := []engine.PlanStep{{ID: "s1", Title: "S1"}}
	c.seq(60,
		func() { // a dry run of Full Cleanup, then it ends
			w.startDry(actions.IDFullCleanup)
			c.feed(engine.QueueStarted{Action: actions.IDFullCleanup, DryRun: true, Steps: step})
			c.ok(strings.Contains(w.status.Text(), "DRY RUN"), "a real dry run lost its DRY RUN: %q", w.status.Text())
			c.feed(engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted})
			c.ok(!w.busy, "the dry run did not end")
		},
		func() { // a real Repair with the switch off: no DRY RUN in any phase
			w.startAction(actions.IDRepair)
			dry("right after Start")
			c.feed(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Looking…"})
			dry("discovery")
			stop("Repair discovery", true)
			c.feed(engine.NeedsConfirm{ID: 1, Kind: engine.ConfirmGate, Title: "Repair?"})
			dry("the confirm")
			stop("Repair confirm", false)
			c.feed(engine.PromptClosed{ID: 1}, engine.Finished{Action: actions.IDRepair, Outcome: engine.OutcomeCancelled})
		},
		func() { // Full Cleanup's gate, a password prompt, a running step
			w.startAction(actions.IDFullCleanup)
			dry("Full Cleanup start")
			stop("Full Cleanup before the gate", true)
			c.feed(engine.NeedsPassword{ID: 1})
			stop("the up-front password", true)
			c.feed(engine.PromptClosed{ID: 1})
			c.feed(engine.NeedsConfirm{ID: 2, Kind: engine.ConfirmGate, Title: "Proceed?"})
			stop("Full Cleanup gate", false)
			c.feed(engine.PromptClosed{ID: 2}, engine.QueueStarted{Action: actions.IDFullCleanup, Steps: step})
			dry("the plan")
			stop("a running step", true)
		},
		func() {
			c.feed(engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted})
			w.startAction(actions.IDClearCaches) // its pick
			c.feed(engine.NeedsSelection{ID: 1, Title: "Caches", Items: []string{"a"}})
			stop("the caches pick", false)
			c.feed(engine.PromptClosed{ID: 1}, engine.Finished{Action: actions.IDClearCaches, Outcome: engine.OutcomeCancelled})
			c.ok(!w.busy, "busy after the end; dialogs %d", len(w.dialogs))
		},
	)
}
