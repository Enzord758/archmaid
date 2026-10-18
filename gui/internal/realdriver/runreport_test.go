package realdriver

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/report"
)

// reportsOf plays action on a fresh Driver for o and returns the
// RunReport events it sent and whether they came right before Finished.
func reportsOf(t *testing.T, w *Wiring, o engine.Options, action string) (reps []engine.RunReport, before bool) {
	t.Helper()
	d := w.New(o)
	defer d.Close()
	d.Send(engine.Start{Action: action})
	var last engine.Event
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			switch ev := ev.(type) {
			case engine.NeedsConfirm:
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
			case engine.RunReport:
				reps = append(reps, ev)
			case engine.Finished:
				_, before = last.(engine.RunReport)
				return reps, before
			}
			last = ev
		case <-timeout:
			t.Fatalf("%s: no Finished", action)
		}
	}
}

// TestRunReportWired: Config.RunReport reaches the Driver, so a real run
// sends engine.RunReport right before Finished; the default (nil, what
// every test config has) sends none, and neither does a dry run.
func TestRunReportWired(t *testing.T) {
	shimTopgrade(t)
	// A config made of nothing reads no log and runs no command
	// (report.TestZeroConfig); the report says what it couldn't do.
	w := Start(fakes(t, Config{NoAskpass: true, RunReport: &report.Config{}}))
	defer w.Close()
	reps, before := reportsOf(t, w, w.Options(), actions.IDTopgrade)
	if len(reps) != 1 || !before {
		t.Fatalf("real run: %d reports, right before Finished %v, want 1 and true", len(reps), before)
	}
	if reps[0].Report.Action != actions.IDTopgrade {
		t.Errorf("report for %q", reps[0].Report.Action)
	}
	if reps, _ := reportsOf(t, w, dryOptions(w), actions.IDTopgrade); len(reps) != 0 {
		t.Errorf("a dry run sent %d reports", len(reps))
	}

	none := Start(fakes(t, Config{NoAskpass: true}))
	defer none.Close()
	if reps, _ := reportsOf(t, none, none.Options(), actions.IDTopgrade); len(reps) != 0 {
		t.Errorf("a Wiring with no RunReport config sent %d reports", len(reps))
	}
}
