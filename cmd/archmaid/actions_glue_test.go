package main

import (
	"context"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Helpers over internal/actions for the TUI's tests: the real queues and
// discoveries, built with the Env the TUI hands them (actionEnv).

// actionSteps is the queue the menu action id builds right now.
func actionSteps(id string) []steps.Step {
	a, ok := actions.ByID(id)
	if !ok || a.Steps == nil {
		panic("no steps action " + id)
	}
	return a.Steps(actionEnv())
}

func fullCleanupSteps() []steps.Step { return actionSteps(actions.IDFullCleanup) }
func topgradeSteps() []steps.Step    { return actionSteps(actions.IDTopgrade) }

// orphanProbeStep is Full Cleanup's orphan probe.
func orphanProbeStep() steps.Step {
	for _, s := range fullCleanupSteps() {
		if s.ID == steps.IDOrphansProbe {
			return s
		}
	}
	panic("no orphan probe in Full Cleanup")
}

// findOrphans runs the orphan probe.
func findOrphans(ctx context.Context) steps.Result {
	return orphanProbeStep().Run(ctx, steps.Env{})
}

// discoverNow runs the menu action id's discovery synchronously, with env
// adjusted by tweak (if any), and returns what it found.
func discoverNow(t *testing.T, id string, tweak ...func(*actions.Env)) actions.Discovery {
	t.Helper()
	a, ok := actions.ByID(id)
	if !ok || a.Discover == nil {
		t.Fatalf("no discovery action %q", id)
	}
	env := actionEnv()
	for _, f := range tweak {
		f(&env)
	}
	return a.Discover(context.Background(), env)
}

// discoverLater is the discovery action id with its Env captured here, as
// main captures it for the Session, to run later on another goroutine.
func discoverLater(id string) func() actions.Discovery {
	a, _ := actions.ByID(id)
	env := actionEnv()
	return func() actions.Discovery { return a.Discover(context.Background(), env) }
}

// noticeOf is d's notice text, or "" when d is no Notice.
func noticeOf(d actions.Discovery) string {
	n, _ := d.(actions.Notice)
	return n.Text
}

// menuItemIndex is the index of the menu action id.
func menuItemIndex(t *testing.T, id string) int {
	t.Helper()
	for i, a := range actions.Menu {
		if a.ID == id {
			return i
		}
	}
	t.Fatalf("no menu action %q", id)
	return -1
}

// TestActionEnvIsComplete: the Env the TUI hands the actions sets every
// required field (main refuses to start otherwise).
func TestActionEnvIsComplete(t *testing.T) {
	if err := actionEnv().Validate(); err != nil {
		t.Fatal(err)
	}
}
