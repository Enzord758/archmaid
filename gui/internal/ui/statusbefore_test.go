package ui

// Batch 7 (Dev 2, #102): the status line after every way an action can end
// before its queue starts, with a run's page already there. No display:
// a Window built by hand, with start's and redraw's bookkeeping replayed.

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// afterRun is a Window whose Run page holds a completed run of 3 lines.
func afterRun(t *testing.T) *Window {
	t.Helper()
	w := &Window{model: runview.NewModel()}
	w.model.Apply([]engine.Event{
		engine.QueueStarted{Action: "a", Steps: []engine.PlanStep{{ID: "s1", Title: "S1"}}},
		engine.StepStarted{ID: "s1"},
		engine.OutputChunk{ID: "s1", Lines: []string{"x", "y", "z"}},
		engine.StepDone{ID: "s1", Status: steps.OK},
		engine.Finished{Action: "a", Outcome: engine.OutcomeCompleted},
	})
	w.queued = true
	if got := w.statusText(); got != "Finished: completed · 3 lines" {
		t.Fatalf("after the run: status %q", got)
	}
	return w
}

// startNext replays start's bookkeeping for the next action.
func startNext(w *Window, id string, dry bool) {
	w.queued, w.lastFinished, w.ended = false, w.model.Finished, nil
	w.pagePrev = len(w.model.Rows) > 0
	w.current, w.dry, w.model.Stopped = id, dry, ""
	w.model.StartDry = dry
	w.model.Notice = ""
}

// feedEvents replays redraw's bookkeeping for a batch (no widgets).
func feedEvents(w *Window, evs ...engine.Event) {
	c := w.model.Apply(evs)
	if c.Reset {
		w.queued, w.pagePrev = true, false
	}
	if c.Finished {
		w.ended = w.model.Finished
		if !w.queued {
			w.model.Finished = w.lastFinished
		}
	}
}

// TestStatusTextEndsBeforeQueue: every end before the queue says how this
// action ended, never the last run's "Finished: completed" or its lines.
func TestStatusTextEndsBeforeQueue(t *testing.T) {
	cases := []struct {
		name string
		evs  []engine.Event
		dry  bool
		want string
	}{
		{"cancelled up-front password", []engine.Event{
			engine.PhaseChanged{Phase: engine.PhaseProbing},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
			engine.Finished{Action: "b", Outcome: engine.OutcomeCancelled}}, false,
			"Run cancelled, nothing ran"},
		{"cancelled up-front password, dry", []engine.Event{
			engine.Finished{Action: "b", Outcome: engine.OutcomeCancelled}}, true,
			"Run cancelled, nothing ran · DRY RUN"},
		{"a discovery with nothing to say", []engine.Event{
			engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Looking…"},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
			engine.Finished{Action: "b", Outcome: engine.OutcomeCancelled}}, false,
			"Run cancelled, nothing ran"},
		{"a notice without a discovery page", []engine.Event{
			engine.Notice{Text: "No orphans found."},
			engine.Finished{Action: "b", Outcome: engine.OutcomeNotice}}, false,
			"No orphans found."},
		{"a notice, dry", []engine.Event{
			engine.Notice{Text: "No orphans found."},
			engine.Finished{Action: "b", Outcome: engine.OutcomeNotice}}, true,
			"No orphans found. · DRY RUN"},
		{"a discovery that found nothing", []engine.Event{
			engine.DiscoveryResult{Action: "b", Outcome: engine.DiscoveryNothing, Summary: "Checked 4: none."},
			engine.Notice{Text: "Checked 4: none."},
			engine.Finished{Action: "b", Outcome: engine.OutcomeNotice}}, false,
			"Checked 4: none."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := afterRun(t)
			startNext(w, "b", c.dry)
			feedEvents(w, c.evs...)
			got := w.statusText()
			if got != c.want {
				t.Errorf("status %q, want %q", got, c.want)
			}
			if strings.Contains(got, "Finished") || strings.Contains(got, "line") {
				t.Errorf("status %q carries the previous run's end or lines", got)
			}
		})
	}
}

// TestStatusTextNoPolkitAgent: no polkit agent is a refusal inside a queue
// that started: the status line says what the queue says, "not authorised ·
// nothing ran", and counts its own lines, not the run before.
func TestStatusTextNoPolkitAgent(t *testing.T) {
	w := afterRun(t)
	startNext(w, "b", false)
	feedEvents(w,
		engine.QueueStarted{Action: "b", Steps: []engine.PlanStep{{ID: "r1", Title: "R1", NeedsPriv: true, Verb: "v"}, {ID: "r2", Title: "R2", NeedsPriv: true, Verb: "v"}}},
		engine.StepDone{ID: "r1", Status: steps.AuthRefused},
		engine.StepDone{ID: "r2", Status: steps.AuthRefused},
		engine.Finished{Action: "b", Outcome: engine.OutcomeCompleted, Refused: 2, Skipped: 2})
	got := w.statusText()
	if !strings.HasPrefix(got, "Finished: not authorised · nothing ran") || strings.Contains(got, "3 lines") {
		t.Errorf("status %q", got)
	}
}

// TestStatusTextWhileDiscovering: between Start and the first end, the
// previous run's end must not read as this action's state.
func TestStatusTextWhileDiscovering(t *testing.T) {
	w := afterRun(t)
	startNext(w, "b", false)
	w.model.Apply([]engine.Event{engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Looking…"}})
	got := w.statusText()
	if strings.Contains(got, "Finished") || strings.Contains(got, "3 lines") {
		t.Errorf("while the new action discovers, status %q is the previous run's", got)
	}
}

// TestStatusTextGateOpen: an open gate or the up-front password before the
// queue starts reads "Waiting…", not the previous run's end.
func TestStatusTextGateOpen(t *testing.T) {
	w := afterRun(t)
	startNext(w, "b", false)
	w.model.Apply([]engine.Event{engine.NeedsPassword{ID: 7}})
	if w.model.Prompt == nil {
		t.Fatal("no prompt open")
	}
	if got := w.statusText(); got != "Waiting…" {
		t.Errorf("gate/password open: status %q", got)
	}
}

// TestStatusTextPickAndConfirmWait: a pick or a confirm waiting for the user
// reads "Waiting…", not the discovery's busy line ("Measuring the caches…",
// which stays set while the pick is open) or "Running".
func TestStatusTextPickAndConfirmWait(t *testing.T) {
	w := &Window{model: runview.NewModel()}
	startNext(w, "clearcaches", false)
	w.model.Apply([]engine.Event{
		engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Measuring the caches…"},
	})
	if got := w.statusText(); got != "Measuring the caches…" {
		t.Fatalf("while measuring: %q", got)
	}
	w.model.Apply([]engine.Event{engine.NeedsSelection{ID: 3, Items: []string{"a"}}})
	if got := w.statusText(); got != "Waiting…" {
		t.Errorf("pick open: status %q", got)
	}
	// A step's confirm inside the running queue.
	w2 := &Window{model: runview.NewModel()}
	w2.model.Apply([]engine.Event{
		engine.QueueStarted{Action: "downloads", Steps: []engine.PlanStep{{ID: "d", Title: "D", Confirm: true}}},
		engine.PhaseChanged{Phase: engine.PhaseRunning},
		engine.NeedsConfirm{ID: 4, Kind: engine.ConfirmStep, Step: "d"},
	})
	w2.queued = true
	if got := w2.statusText(); got != "Waiting…" {
		t.Errorf("step confirm open: status %q", got)
	}
}
