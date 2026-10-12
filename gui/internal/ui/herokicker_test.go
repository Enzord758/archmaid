package ui

import (
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/overview"
)

// TestHeroKickerCountsRootSteps: the kicker's count is the root-tagged
// steps, not all of them: topgrade (sudo) isn't in the one authorisation.
func TestHeroKickerCountsRootSteps(t *testing.T) {
	p := overview.Preview{Steps: make([]overview.PlanItem, 10), Root: 6, Sudo: 1}
	if got, want := heroKicker(p, true, false), "Recommended · 10 steps · 6 root steps in one authorisation"; got != want {
		t.Errorf("kicker %q, want %q", got, want)
	}
	if got, want := heroKicker(p, true, true), "Recommended · 10 steps\n6 root steps in one authorisation"; got != want {
		t.Errorf("narrow kicker %q, want %q", got, want)
	}
	p.Root = 1
	if got, want := heroKicker(p, true, false), "Recommended · 10 steps · 1 root step in one authorisation"; got != want {
		t.Errorf("kicker %q, want %q", got, want)
	}
}
