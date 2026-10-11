package ui

import (
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"os"
	"path/filepath"
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

// TestLastRunFolderIsTheRecordsRun: a discovery-only run followed by an
// audit in the same minute (the audit's folder is <stamp>-02 and it writes
// no last-run.json): LAST RUN's log files are the ones the record's run
// wrote, not the newest folder's.
func TestLastRunFolderIsTheRecordsRun(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 11, 14, 34, 0, 0, time.Local)
	put := func(run string, cat logstore.Category, mtime time.Time) {
		st := logstore.New(logstore.Options{Dir: dir, Run: run, Now: func() time.Time { return at }})
		if err := st.Append(cat, "OK", "x", "out\n"); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(st.Path(cat), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	put("20261011-1434", logstore.Cleanup, at.Add(10*time.Second))
	put("20261011-1434-02", logstore.Audit, at.Add(40*time.Second))
	w := &Window{opts: Options{StateDir: dir}}
	w.lastRun.Record = &logstore.RunRecord{Action: actions.IDServices, Outcome: logstore.OutcomeOK,
		Started: at.Add(5 * time.Second), Finished: at.Add(12 * time.Second)}
	r, ok := w.lastRunFolder()
	if !ok || r.Name != "20261011-1434" {
		t.Errorf("folder %q %v, want the record's run, 20261011-1434", r.Name, ok)
	}
	// When the record's own run wrote no folder, the audit's is not its.
	if err := os.RemoveAll(filepath.Join(dir, "20261011-1434")); err != nil {
		t.Fatal(err)
	}
	if r, ok := w.lastRunFolder(); ok {
		t.Errorf("folder %q for a run that wrote none", r.Name)
	}
}
