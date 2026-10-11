package runview

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestLinesAreThisRunsOnly: the status line's line count starts from zero
// with every run, a discovery result's page included (the VM round saw
// 83, then 225, 225 after a one-line run, then 308).
func TestLinesAreThisRunsOnly(t *testing.T) {
	m := NewModel()
	disc := func(n int) engine.DiscoveryResult {
		d := engine.DiscoveryResult{Action: "services", Outcome: engine.DiscoveryNothing, Summary: "x", Category: logstore.Services}
		for range n {
			d.Details = append(d.Details, "a.service  disabled")
		}
		return d
	}
	m.Apply([]engine.Event{disc(83)})
	if m.Lines != 83 {
		t.Fatalf("first discovery: %d lines, want 83", m.Lines)
	}
	m.Apply([]engine.Event{disc(142)})
	if m.Lines != 142 {
		t.Errorf("second discovery: %d lines, want 142 (this run's)", m.Lines)
	}
	m.Apply([]engine.Event{engine.QueueStarted{Action: "cache", Steps: []engine.PlanStep{{ID: "c", Title: "C"}}}})
	m.Apply([]engine.Event{engine.OutputChunk{ID: "c", Lines: []string{"one"}, Dropped: 2}})
	if m.Lines != 3 || m.Dropped != 2 {
		t.Errorf("a queue run: %d lines %d dropped, want 3 and 2", m.Lines, m.Dropped)
	}
	m.Apply([]engine.Event{disc(5)})
	if m.Lines != 5 || m.Dropped != 0 {
		t.Errorf("after a queue run: %d lines %d dropped, want 5 and 0", m.Lines, m.Dropped)
	}
}
