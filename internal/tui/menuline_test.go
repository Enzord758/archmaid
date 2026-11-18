package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/Enzord758/archmaid/internal/health"
	"github.com/Enzord758/archmaid/internal/held"
)

func lookReport(n int) health.Report {
	rep := health.Report{At: time.Now()}
	for i := 0; i < n; i++ {
		rep.Checks = append(rep.Checks, health.Check{ID: "c", Title: "C", Group: health.GroupSystem, State: health.Look})
	}
	return rep
}

func TestMenuLabelSaysWhatNeedsAttention(t *testing.T) {
	heldSeams(t, held.Set{}, "paru")
	m := fixedModel()
	var health0 = menu()[len(menu())-5]
	if health0.ID != idHealth {
		t.Fatalf("menu index drifted: %q", health0.ID)
	}
	if got := m.menuLabel(health0); got != "System health" {
		t.Errorf("before a report: %q", got)
	}
	for _, c := range []struct {
		n    int
		want string
	}{{0, "System health"}, {1, "System health · 1 thing needs attention"}, {2, "System health · 2 things need attention"}} {
		m.health.rep, m.health.have = lookReport(c.n), true
		if got := m.menuLabel(health0); got != c.want {
			t.Errorf("%d look: %q, want %q", c.n, got, c.want)
		}
	}
	heldEntry := menu()[len(menu())-4]
	if got := m.menuLabel(heldEntry); got != "Held updates" {
		t.Errorf("nothing held: %q", got)
	}
	m.held.set = heldFixture()
	if got := m.menuLabel(heldEntry); got != "Held updates · 3 held" {
		t.Errorf("held: %q", got)
	}
	if got := m.menuLabel(menu()[0]); got != menu()[0].Label {
		t.Errorf("another entry: %q", got)
	}
}

func TestMenuLineGolden(t *testing.T) {
	heldSeams(t, heldFixture(), "paru")
	m := sizedModel()
	m.health.rep, m.health.have = lookReport(2), true
	m.held.set = heldFixture()
	viewGolden(t, m)
}

// TestStartupFillsTheMenuLines: the program's start runs the checks and reads
// the held lists off the loop, and the menu says what they found.
func TestStartupFillsTheMenuLines(t *testing.T) {
	heldSeams(t, heldFixture(), "paru")
	old := healthRun
	healthRun = func(context.Context) health.Report { return healthFixture() }
	defer func() { healthRun = old }()

	tm := teatest.NewTestModel(t, fixedModel(), teatest.WithInitialTermSize(100, 30))
	// One wait for both: the first would consume the frame holding the second.
	var seen []byte
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		seen = append(seen, b...)
		return strings.Contains(string(seen), "System health · 3 things need attention") &&
			strings.Contains(string(seen), "Held updates · 3 held")
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestStartupRespectsTheSwitchAndNoDriver(t *testing.T) {
	if (model{}).startupChecks() != nil {
		t.Error("a model without a Driver ran the checks")
	}
	healthAtStart = false
	defer func() { healthAtStart = true }()
	if fixedModel().startupChecks() != nil {
		t.Error("the switch did not stop the startup run")
	}
}

// TestStartupResultIsDroppedWhenTheScreenRanItsOwn: a newer run's result wins.
func TestStartupResultIsDroppedWhenTheScreenRanItsOwn(t *testing.T) {
	m := sizedModel()
	m, _ = m.startHealth() // seq 1
	m = update(m, healthDoneMsg{seq: 0, rep: lookReport(5)})
	if m.health.have {
		t.Fatal("the startup result was taken over the screen's own run")
	}
	m = update(m, heldLoadedMsg{set: heldFixture()})
	if len(m.held.set.Audit) != 1 {
		t.Fatal("the held lists were not taken")
	}
	if !strings.Contains(m.menuLabel(menu()[len(menu())-4]), "3 held") && heldCount(m.held.set, time.Now()) != 1 {
		t.Fatal("count")
	}
}
