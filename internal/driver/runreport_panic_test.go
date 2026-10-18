package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A bug in the run report costs the run its report, never the run: a panic
// while a step is measured (here the pacman-conf query behind the cache
// folders) is reported, the report is dropped and the queue goes on to its
// Finished, with a last-run record that has no summary.
func TestRunReportPanicInAStep(t *testing.T) {
	w := newReportWorld(t)
	w.cfg.Query = func(context.Context, string, ...string) ([]byte, error) { panic("pacman-conf blew up") }
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", w.queue()...))
	evs := r.run("q", yes)
	if fin := find[engine.Finished](t, evs); fin.Outcome != engine.OutcomeCompleted || fin.Errs != 0 {
		t.Fatalf("Finished = %+v", fin)
	}
	for _, ev := range evs {
		if _, ok := ev.(engine.RunReport); ok {
			t.Fatal("a report whose measuring panicked was published")
		}
	}
	if got := r.reports.text(); !strings.Contains(got, "the run report panicked: pacman-conf blew up") {
		t.Errorf("reports = %q", got)
	}
	if rec, err := r.log.ReadLastRun(); err != nil || rec.Summary != nil {
		t.Errorf("last run %+v (%v)", rec, err)
	}
}

// The same panic at the end (the cache line's paccache query, after the
// steps measured fine): the report is dropped, the record is written
// without a summary, Finished still comes.
func TestRunReportPanicAtTheEnd(t *testing.T) {
	w := newReportWorld(t)
	q := w.cfg.Query
	w.cfg.Query = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "paccache" {
			panic("paccache blew up")
		}
		return q(ctx, name, args...)
	}
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", w.queue()...))
	evs := r.run("q", yes)
	for _, ev := range evs {
		if _, ok := ev.(engine.RunReport); ok {
			t.Fatal("a report whose end panicked was published")
		}
	}
	if got := r.reports.text(); !strings.Contains(got, "the run report panicked: paccache blew up") {
		t.Errorf("reports = %q", got)
	}
	if rec, err := r.log.ReadLastRun(); err != nil || rec.Outcome != "ok" || rec.Summary != nil {
		t.Errorf("last run %+v (%v)", rec, err)
	}
}

// A QueueStarted while a Collector is still open (the Finished never came)
// starts a new one: nothing of the old run leaks into the new report.
func TestRunReportQueueRestarts(t *testing.T) {
	w := newReportWorld(t)
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", steps.Step{ID: "x", Title: "x…", LogLabel: "x", Argv: []string{"true"}}))
	r.d.reportBegin(engine.Output{Events: []engine.Event{engine.QueueStarted{Action: "old"}}})
	old := r.d.rep
	if old == nil {
		t.Fatal("no Collector after QueueStarted")
	}
	r.d.reportBegin(engine.Output{Events: []engine.Event{engine.QueueStarted{Action: "new"}}})
	if r.d.rep == old || r.d.rep == nil {
		t.Error("a second QueueStarted kept the first run's Collector")
	}
	r.d.reportBegin(engine.Output{Events: []engine.Event{engine.QueueStarted{Action: "dry", DryRun: true}}})
	if r.d.rep != nil {
		t.Error("a dry run's QueueStarted left a Collector")
	}
}
