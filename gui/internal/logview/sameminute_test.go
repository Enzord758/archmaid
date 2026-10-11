package logview

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestSameMinuteRuns: runs started in one minute are separate folders
// (<stamp>, <stamp>-02), each listed newest first with its own logs and
// the minute's time, and each reads its own log.
func TestSameMinuteRuns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	at := time.Date(2026, 10, 11, 14, 34, 0, 0, time.Local)
	for _, c := range []logstore.Category{logstore.Cleanup, logstore.Audit} {
		st := logstore.New(logstore.Options{Dir: dir, Fresh: true, Now: func() time.Time { return at }})
		if err := st.Append(c, "OK", string(c)+" ran", ""); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := Runs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range runs {
		got = append(got, r.Name)
		if !r.Time.Equal(at) {
			t.Errorf("%s: time %v, want the minute %v", r.Name, r.Time, at)
		}
	}
	if want := []string{"20261011-1434-02", "20261011-1434"}; !slices.Equal(got, want) {
		t.Fatalf("runs %q, want %q", got, want)
	}
	if !slices.Equal(runs[0].Categories, []logstore.Category{logstore.Audit}) || !slices.Equal(runs[1].Categories, []logstore.Category{logstore.Cleanup}) {
		t.Errorf("categories %q and %q: each run holds only its own log", runs[0].Categories, runs[1].Categories)
	}
	lines, err := runs[0].Read(logstore.Audit)
	if err != nil || len(lines) == 0 {
		t.Errorf("reading the -02 run: %q %v", lines, err)
	}
}
