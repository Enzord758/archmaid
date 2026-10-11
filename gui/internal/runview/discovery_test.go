package runview

import (
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestDiscoveryResult: an action that ended at its discovery is the run of
// one "Discovery" step: the Summary its line, the Details its output, ok
// for nothing found and failed for a look that failed; the page is this
// action's, with the discovery's own header word. A Found result changes
// nothing: its queue is the run.
func TestDiscoveryResult(t *testing.T) {
	start := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		outcome engine.DiscoveryOutcome
		state   RowState
		kicker  string
	}{
		{"nothing", engine.DiscoveryNothing, RowOK, "Nothing found"},
		{"failed", engine.DiscoveryFailed, RowFailed, "Failed: the check didn't finish"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := NewModel()
			m.Apply([]engine.Event{engine.QueueStarted{Action: "fullcleanup", Steps: []engine.PlanStep{{ID: "topgrade", Title: "Topgrade"}}}})
			m.Apply([]engine.Event{engine.Finished{Outcome: engine.OutcomeCompleted}})
			ch := m.Apply([]engine.Event{
				engine.DiscoveryResult{Action: "services", Outcome: c.outcome, Summary: "Checked 3 services.",
					Details: []string{"a.service  disabled", "b.service  static"}, Category: logstore.Services, Started: start, Elapsed: time.Second},
				engine.Notice{Text: "Checked 3 services."},
				engine.Finished{Outcome: engine.OutcomeNotice},
			})
			if !ch.Reset || !ch.Finished {
				t.Errorf("change %+v: want a reset and the end", ch)
			}
			if m.Action != "services" || len(m.Rows) != 1 || m.Rows[0].ID != DiscoveryStepID || m.Rows[0].Title != "Discovery" || m.Rows[0].State != c.state {
				t.Fatalf("action %q rows %+v", m.Action, m.Rows)
			}
			if got := m.Rows[0].Summary(); got != "Checked 3 services." {
				t.Errorf("the step's line %q", got)
			}
			if got := m.StepText(DiscoveryStepID); strings.Join(got, "|") != "a.service  disabled|b.service  static" {
				t.Errorf("details %q", got)
			}
			if h := m.Header(); h.Kicker != c.kicker || h.Badge != "done" {
				t.Errorf("header %+v", h)
			}
			if m.Rows[0].Category != logstore.Services {
				t.Errorf("the step's log %q", m.Rows[0].Category)
			}
		})
	}

	m := NewModel()
	m.Apply([]engine.Event{engine.QueueStarted{Action: "fullcleanup", Steps: []engine.PlanStep{{ID: "topgrade", Title: "Topgrade"}}}})
	ch := m.Apply([]engine.Event{engine.DiscoveryResult{Action: "repair", Outcome: engine.DiscoveryFound, Summary: "x"}})
	if ch.Reset || m.Action != "fullcleanup" || len(m.Rows) != 1 || m.Rows[0].ID != "topgrade" {
		t.Errorf("a Found result changed the page: %+v %q %+v", ch, m.Action, m.Rows)
	}
}

// TestShorten: a long notice is cut for the toast and the status line.
func TestShorten(t *testing.T) {
	if Shorten("short", 10) != "short" {
		t.Error("a short text was cut")
	}
	if got := Shorten(strings.Repeat("ä", 200), 10); got != strings.Repeat("ä", 9)+"…" {
		t.Errorf("cut to %q", got)
	}
	m := NewModel()
	m.Notice = strings.Repeat("x", 500)
	if got := m.EndToast(engine.Finished{Outcome: engine.OutcomeNotice}); len([]rune(got)) != ShortMax {
		t.Errorf("toast of %d runes, want %d", len([]rune(got)), ShortMax)
	}
}
