package runview

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A run where one step failed, one root step was refused (the desktop's
// agent) and the user then cancelled archmaid's own polkit dialog for the
// next. The numbers are the engine's, from TestPolkitCancelMixedRun and
// TestPolkitCancelAfterRefusalNothingRan (internal/engine): every surface
// that reports the run must tell the same, true story, and the Run page's
// chips must add up to the plan.

// TestCancelMixedRunSurfacesAgree: failed + refused + cancelled. The engine
// ends it aborted (the failed step ran), Finished{Errs 1, Refused 1,
// Skipped 3}, last-run.json aborted/steps_failed 1/steps_skipped 3/
// steps_refused 1.
func TestCancelMixedRunSurfacesAgree(t *testing.T) {
	m := queueModel(4,
		engine.StepStarted{ID: "a", Index: 0}, done("a", 0, steps.Failed),
		done("b", 1, steps.AuthRefused),
		done("c", 2, steps.Skipped), // the user's cancel
		done("d", 3, steps.Skipped), // behind it
	)
	fin := engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeAborted, Errs: 1, Refused: 1, Skipped: 3}
	m.Apply([]engine.Event{fin})
	status, toast := m.FinishedText(fin)
	rec := logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: logstore.OutcomeAborted,
		StepsFailed: 1, StepsSkipped: 3, StepsRefused: 1, Finished: time.Now()}
	issues := overview.Summarize(rec, rec.Finished).Issues
	if len(issues) != 1 {
		t.Fatalf("Overview issues %q", issues)
	}
	// Run page and toast: aborted, the failure and the refusal, never "nothing ran" or "cancelled".
	if want := "Finished: aborted (1 step failed) · 1 step not authorised"; status != want {
		t.Errorf("status %q, want %q", status, want)
	}
	if want := "Run aborted, 1 step failed · 1 step not authorised"; toast != want {
		t.Errorf("toast %q, want %q", toast, want)
	}
	// Overview (and so the widget, whose literal tst_state.qml pins) says
	// aborted and the refusal. It does not repeat the failed count: the
	// Run page does.
	if want := "Last run aborted · " + plural.Refused(1); issues[0] != want {
		t.Errorf("Overview %q, want %q", issues[0], want)
	}
	if overview.NotAuthorised(rec) {
		t.Error("an aborted run is not 'not authorised'")
	}
	// The chips add up to the plan: 0 done, 1 failed, 2 skipped, 1 refused.
	h := m.Header()
	if h.Done != 0 || h.Failed != 1 || h.Skipped != 2 || h.Refused != 1 || h.Stopped != 0 || h.NotRun != 0 ||
		h.Done+h.Failed+h.Skipped+h.Refused+h.Stopped+h.NotRun != h.Of {
		t.Errorf("header counts %+v", h)
	}
	if h.Kicker != status {
		t.Errorf("kicker %q != status %q", h.Kicker, status)
	}
	if h.Skipped+h.Refused != fin.Skipped {
		t.Errorf("chips skipped+refused = %d, engine Finished.Skipped = %d (a refused step is part of it)", h.Skipped+h.Refused, fin.Skipped)
	}
}

// TestCancelAfterRefusalNothingRanSurfacesAgree: a refused step, then the
// user's cancel, nothing started. The engine records cancelled with
// steps_refused 1, which every front end has always read as "not
// authorised · nothing ran". It is true (nothing ran, one step was not
// authorised) and the same everywhere; the user's own cancel of the second
// step is not named. The cancel is not shown as a failure or as a second
// refusal.
func TestCancelAfterRefusalNothingRanSurfacesAgree(t *testing.T) {
	m := queueModel(2, done("a", 0, steps.AuthRefused), done("b", 1, steps.Skipped))
	fin := engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCancelled, Skipped: 2, Refused: 1}
	m.Apply([]engine.Event{fin})
	status, toast := m.FinishedText(fin)
	rec := logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: logstore.OutcomeCancelled, StepsSkipped: 2, StepsRefused: 1, Finished: time.Now()}
	issue := overview.Summarize(rec, rec.Finished).Issues[0]
	const tail = "not authorised · nothing ran (1 step not authorised)"
	if status != "Finished: "+tail || toast != "Run "+tail || issue != "Last run "+tail {
		t.Errorf("page %q toast %q overview %q, want the tail %q", status, toast, issue, tail)
	}
	h := m.Header()
	if h.Refused != 1 || h.Skipped != 1 || h.Failed != 0 || h.Done != 0 {
		t.Errorf("header counts %+v", h)
	}
}

// TestCancelledByUserSurfacesAgree: the plain cancel in our dialog (nothing
// refused): cancelled everywhere, no "not authorised" anywhere, and a
// cancel after a step ran is aborted with no refusal text.
func TestCancelledByUserSurfacesAgree(t *testing.T) {
	m := queueModel(2, done("a", 0, steps.Skipped), done("b", 1, steps.Skipped))
	fin := engine.Finished{Action: actions.IDServices, Outcome: engine.OutcomeCancelled, Skipped: 2}
	status, toast := m.FinishedText(fin)
	rec := logstore.RunRecord{V: 1, Action: actions.IDServices, Outcome: logstore.OutcomeCancelled, StepsSkipped: 2, Finished: time.Now()}
	issue := overview.Summarize(rec, rec.Finished).Issues[0]
	if status != "Finished: cancelled" || toast != "Run cancelled, nothing ran" || issue != "Last run cancelled, nothing ran" {
		t.Errorf("page %q toast %q overview %q", status, toast, issue)
	}

	m = queueModel(3, engine.StepStarted{ID: "a", Index: 0}, done("a", 0, steps.OK), done("b", 1, steps.Skipped), done("c", 2, steps.Skipped))
	fin = engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeAborted, Skipped: 2}
	status, toast = m.FinishedText(fin)
	rec = logstore.RunRecord{V: 1, Action: actions.IDFullCleanup, Outcome: logstore.OutcomeAborted, StepsSkipped: 2, Finished: time.Now()}
	issue = overview.Summarize(rec, rec.Finished).Issues[0]
	if status != "Finished: aborted" || toast != "Run aborted" || issue != "Last run aborted" {
		t.Errorf("page %q toast %q overview %q", status, toast, issue)
	}
}
