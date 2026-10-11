package runview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

func queueModel(n int, evs ...engine.Event) *Model {
	plan := make([]engine.PlanStep, n)
	for i := range plan {
		plan[i] = engine.PlanStep{ID: string(rune('a' + i)), NeedsPriv: true}
	}
	m := NewModel()
	m.Apply(append([]engine.Event{engine.QueueStarted{Steps: plan}}, evs...))
	return m
}

func done(id string, i int, st steps.Status) engine.StepDone {
	return engine.StepDone{ID: id, Index: i, Status: st}
}

// TestNotAuthorisedAgreesWithOverview: for 1, 2 and 3 refused steps the Run
// page's status line, its toast and the Overview's issue (and so the DMS
// widget's, which tst_state.qml pins to the same literals) end in the same
// words, singular and plural alike.
func TestNotAuthorisedAgreesWithOverview(t *testing.T) {
	for _, c := range []struct {
		n    int
		tail string
	}{
		{1, "not authorised · nothing ran (1 step not authorised)"},
		{2, "not authorised · nothing ran (2 steps not authorised)"},
		{3, "not authorised · nothing ran (3 steps not authorised)"},
	} {
		var evs []engine.Event
		for i := 0; i < c.n; i++ {
			evs = append(evs, done(string(rune('a'+i)), i, steps.AuthRefused))
		}
		m := queueModel(c.n, evs...)
		s, toast := m.FinishedText(engine.Finished{Outcome: engine.OutcomeCompleted, Refused: c.n, Skipped: c.n})
		rec := logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: logstore.OutcomeCancelled, StepsRefused: c.n, Finished: time.Now()}
		issue := overview.Summarize(rec, time.Now()).Issues[0]
		if s != "Finished: "+c.tail || toast != "Run "+c.tail || issue != "Last run "+c.tail {
			t.Errorf("n=%d: page %q toast %q overview %q, want the tail %q", c.n, s, toast, issue, c.tail)
		}
	}
}

// TestFinishedTextRefusedNotAllOfIt: "not authorised, nothing ran" is only
// for a run where nothing ran. A step that ran, failed or was aborted
// makes it incomplete, failed or aborted, as last-run.json judges it.
func TestFinishedTextRefusedNotAllOfIt(t *testing.T) {
	ok := func() engine.Event { return engine.StepStarted{ID: "a", Index: 0} }
	for _, c := range []struct {
		name string
		m    *Model
		f    engine.Finished
		want string
	}{
		{"ran + refused", queueModel(2, ok(), done("a", 0, steps.OK), done("b", 1, steps.AuthRefused)),
			engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1}, "Finished: incomplete (1 step not authorised)"},
		{"failed + refused", queueModel(3, ok(), done("a", 0, steps.Failed), done("b", 1, steps.AuthRefused), done("c", 2, steps.AuthRefused)),
			engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 1, Refused: 2}, "Finished: failed (1 step) · 2 steps not authorised"},
		{"refused first, then a step ran", queueModel(2, done("a", 0, steps.AuthRefused), engine.StepStarted{ID: "b", Index: 1}, done("b", 1, steps.OK)),
			engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1}, "Finished: incomplete (1 step not authorised)"},
		{"stopped while running", queueModel(2, ok(), done("b", 1, steps.AuthRefused)),
			engine.Finished{Outcome: engine.OutcomeAborted, Refused: 1}, "Finished: aborted · 1 step not authorised"},
		{"aborted row + refused", queueModel(2, ok(), engine.Aborted{ID: "a", Index: 0}, done("b", 1, steps.AuthRefused)),
			engine.Finished{Outcome: engine.OutcomeAborted, Refused: 1}, "Finished: aborted · 1 step not authorised"},
		{"errs counted, nothing started", queueModel(1, done("a", 0, steps.AuthRefused)),
			engine.Finished{Outcome: engine.OutcomeCompleted, Errs: 1, Refused: 1}, "Finished: failed (1 step) · 1 step not authorised"},
	} {
		s, toast := c.m.FinishedText(c.f)
		if s != c.want || strings.Contains(toast, "nothing ran") || strings.Contains(s, "nothing ran") {
			t.Errorf("%s: %q / %q, want %q", c.name, s, toast, c.want)
		}
	}
}

// TestFinishedTextRefusedWithSkippedRows: every step refused or skipped
// (an optional step declined, a step with nothing to do) and none started:
// the engine records cancelled with steps_refused, and the page says not
// authorised. A step still pending when the run was cancelled agrees.
func TestFinishedTextRefusedWithSkippedRows(t *testing.T) {
	m := queueModel(3, done("a", 0, steps.Skipped), done("b", 1, steps.AuthRefused), done("c", 2, steps.Skipped))
	f := engine.Finished{Outcome: engine.OutcomeCompleted, Refused: 1, Skipped: 3}
	want := "Finished: not authorised · nothing ran (1 step not authorised)"
	if s, _ := m.FinishedText(f); s != want {
		t.Errorf("skipped + refused: %q, want %q", s, want)
	}
	p := queueModel(2, done("a", 0, steps.AuthRefused))
	if s, _ := p.FinishedText(engine.Finished{Outcome: engine.OutcomeCancelled, Refused: 1}); s != want {
		t.Errorf("pending + refused, cancelled: %q, want %q", s, want)
	}
	// Skipped rows alone (nothing refused) stay what they were.
	k := queueModel(2, done("a", 0, steps.Skipped), done("b", 1, steps.Skipped))
	if s, _ := k.FinishedText(engine.Finished{Outcome: engine.OutcomeCompleted, Skipped: 2}); s != "Finished: completed" {
		t.Errorf("skipped only: %q", s)
	}
}

// TestSummarizeNotAuthorisedOnlyWhenCancelled: the other outcomes keep their
// own words when steps_refused is set, and a record without the field (an
// older archmaid: zero) is a plain cancel.
func TestSummarizeNotAuthorisedOnlyWhenCancelled(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		outcome string
		refused int
		want    string
	}{
		{logstore.OutcomeAborted, 2, "Last run aborted · 2 steps not authorised"},
		{logstore.OutcomeFailed, 2, "Last run failed (1 step) · 2 steps not authorised"},
		{logstore.OutcomeIncomplete, 2, "Last run incomplete: root steps refused (2)"},
		{logstore.OutcomeCancelled, 0, "Last run cancelled, nothing ran"},
	} {
		rec := logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: c.outcome, StepsRefused: c.refused, StepsFailed: 1, Finished: now}
		if got := overview.Summarize(rec, now).Issues; len(got) != 1 || got[0] != c.want {
			t.Errorf("%s/%d: %q, want %q", c.outcome, c.refused, got, c.want)
		}
		if overview.NotAuthorised(rec) {
			t.Errorf("%s/%d: NotAuthorised", c.outcome, c.refused)
		}
	}
	// A record on disk without steps_refused, as an older archmaid wrote it.
	dir := t.TempDir()
	if err := logstore.New(logstore.Options{Dir: dir}).WriteLastRun(logstore.RunRecord{Action: actions.IDFullCleanup, Outcome: logstore.OutcomeCancelled, Started: now, Finished: now}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, logstore.LastRunFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(raw), `"steps_refused":0,`, "", 1)
	if old == string(raw) {
		t.Fatalf("no steps_refused to drop from %s", raw)
	}
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if lr := overview.ReadLastRun(dir, now); lr.Record == nil || overview.NotAuthorised(*lr.Record) || lr.Issues[0] != "Last run cancelled, nothing ran" {
		t.Errorf("cancelled record read back: %+v", lr)
	}
}
