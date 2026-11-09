package logview

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// A run's logs include packages.log, listed with the other categories (the
// Logs page's chips, the Overview's file line), and an older run folder
// with cleanup.log still opens (#268).
func TestRunsListPackagesLogAndKeepOldCleanupLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	old := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)
	write(t, dir, old, false, logstore.Cleanup, "Packages installed") // before packages.log existed
	write(t, dir, now, false, logstore.Packages, "Packages installed")

	runs, err := Runs(dir)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %v %v", runs, err)
	}
	if got := runs[0].Categories; !slices.Equal(got, []logstore.Category{logstore.Packages}) {
		t.Errorf("the new run's logs %v, want packages", got)
	}
	if got := runs[1].Categories; !slices.Equal(got, []logstore.Category{logstore.Cleanup}) {
		t.Errorf("the old run's logs %v, want cleanup", got)
	}
	if text, err := runs[1].Read(logstore.Cleanup); err != nil || len(text) == 0 {
		t.Errorf("the old cleanup.log does not open: %q %v", text, err)
	}
	if text, err := runs[0].Read(logstore.Packages); err != nil || len(text) == 0 {
		t.Errorf("packages.log does not open: %q %v", text, err)
	}
	if logstore.Packages.Label() != "Packages" {
		t.Errorf("label %q", logstore.Packages.Label())
	}
}
