package ui

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/logview"
)

// TestRunFolderSameMinute: of several runs started in one minute
// (<stamp>, <stamp>-02, ...) a run's folder is the newest, not the first.
func TestRunFolderSameMinute(t *testing.T) {
	at := time.Date(2026, 10, 11, 14, 34, 0, 0, time.Local)
	runs := []logview.Run{{Name: "20261011-1434-02"}, {Name: "20261011-1434"}, {Name: "20261011-1420"}}
	if r, ok := runFolder(runs, at.Add(20*time.Second), false); !ok || r.Name != "20261011-1434-02" {
		t.Errorf("folder %q %v, want the minute's newest run, 20261011-1434-02", r.Name, ok)
	}
	// The Driver's store is stamped just before the queue: the minute before.
	if r, ok := runFolder(runs[1:], at.Add(time.Minute), false); !ok || r.Name != "20261011-1434" {
		t.Errorf("folder %q %v, want 20261011-1434 (the minute before)", r.Name, ok)
	}
}
