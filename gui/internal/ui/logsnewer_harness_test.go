package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// logsNewer is the logs-newer scenario: it opens the Logs page (one run in
// the state dir), has newer runs written while the window is open, and
// notes which run the page shows after each visit in rep.LogRows: the
// first run; then, after a newer run appeared, the second; then, after
// the user picked the older run and a third appeared, still the older one.
func logsNewer(w *Window, rep *report) {
	dir := os.Getenv("ARCHMAID_GUI_STATE")
	at := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	write := func(d time.Duration, text string) {
		st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return at.Add(d) }})
		if err := st.Append(logstore.Audit, "OK", text, text+"\n"); err != nil {
			rep.Errors = append(rep.Errors, err.Error())
		}
	}
	seen := func() { rep.LogRows = append(rep.LogRows, w.logs.run.Name) }
	write(0, "first")
	w.showLogs()
	seen()
	write(time.Hour, "second")
	w.showLogs()
	seen()
	// The user picks the older run (its sidebar row), then a third appears.
	row := w.logs.runs.RowAtIndex(1)
	w.logs.runs.SelectRow(row)
	row.Activate()
	write(2*time.Hour, "third")
	w.showLogs()
	seen()
	w.win.Close()
}

// TestGUILogsNewer: a run that appears while the Logs page isn't looked at
// is shown on the next visit (the VM round: the page kept the 14:27 run
// when the 14:29 one had appeared); a run the user picked in the visit
// stays shown over a newer one.
func TestGUILogsNewer(t *testing.T) {
	needDisplay(t)
	dir := filepath.Join(t.TempDir(), "state")
	rep, _ := child(t, "logs-newer", "ARCHMAID_GUI_STATE="+dir)
	at := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	name := func(d time.Duration) string { return at.Add(d).Format("20060102-1504") }
	want := []string{name(0), name(time.Hour), name(0)}
	if !slices.Equal(rep.LogRows, want) {
		t.Errorf("runs shown on the three visits %q, want %q (newest after a new run, the user's pick over a newer one)", rep.LogRows, want)
	}
}
