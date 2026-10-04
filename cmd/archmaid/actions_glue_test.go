package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"
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
// adjusted by tweak (if any), and returns it as the TUI would receive it.
func discoverNow(t *testing.T, id string, tweak ...func(*actions.Env)) discoveryMsg {
	t.Helper()
	a, ok := actions.ByID(id)
	if !ok || a.Discover == nil {
		t.Fatalf("no discovery action %q", id)
	}
	env := actionEnv()
	for _, f := range tweak {
		f(&env)
	}
	return discoveryMsg{d: a.Discover(context.Background(), env)}
}

// discoverCmd is the Cmd activate would start for the discovery action id,
// its Env captured here, as on the model's goroutine.
func discoverCmd(id string) tea.Cmd {
	a, _ := actions.ByID(id)
	return discover(a.Discover, actionEnv(), false)
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

// TestReportAcceptReachesTheModel: a Report's Accept is what `a` on the
// audit screen runs. It is typed, so a producer can't hand over a func of
// another type that the model would silently drop (Report.Audit was an any
// the model type-asserted, leaving `a` a no-op on a mismatch).
func TestReportAcceptReachesTheModel(t *testing.T) {
	accepted := 0
	m := update(initialModel(), discoveryMsg{d: actions.Report{Text: "1 high finding",
		Accept: func() error { accepted++; return nil }}})
	if m.screen != screenAudit || m.auditCommit == nil {
		t.Fatalf("screen=%d commit=%v, want the audit screen holding Accept", m.screen, m.auditCommit != nil)
	}
	m = update(m, key("a"))
	if accepted != 1 || m.screen != screenNotice || !strings.Contains(m.notice, "accepted") {
		t.Fatalf("accepted %d times, screen=%d notice=%q", accepted, m.screen, m.notice)
	}
	// Nothing to accept: `a` does nothing.
	m = update(initialModel(), discoveryMsg{d: actions.Report{Text: "clean"}})
	if m = update(m, key("a")); m.screen != screenAudit {
		t.Fatalf("screen=%d after a with nothing to accept, want the audit screen", m.screen)
	}
}
