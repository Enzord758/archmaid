package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// logLines is the Logs page's list as shown: one string per row.
func logLines(w *Window) []string {
	p := w.logs
	var out []string
	for i := range p.listLen {
		out = append(out, p.list.String(i))
	}
	return out
}

// searchLog types query into the Logs page's search and returns the
// list, then clears the search again. The entry's search-changed comes
// after a delay, so the list is laid out at once here, as it then is.
func searchLog(w *Window, query string) []string {
	if query == "" {
		return nil
	}
	p := w.logs
	p.bar.SetSearchMode(true)
	p.search.SetText(query)
	w.layoutLog()
	out := logLines(w)
	p.bar.SetSearchMode(false) // clears the text
	w.layoutLog()
	return out
}

// foldFirstSection folds the first titled section, returns the list, and
// unfolds it again.
func foldFirstSection(w *Window) []string {
	for i, r := range w.logs.rows {
		if r.Kind == logview.RowSection {
			w.toggleSection(i)
			out := logLines(w)
			w.toggleSection(i)
			return out
		}
	}
	return nil
}

// TestGUILogsPage: the Logs page lists the runs, shows a topgrade log as
// its entry and step sections, filters it by a search, folds a section,
// and Open folder asks the Launcher for the run's folder; the state dir
// is unchanged after.
func TestGUILogsPage(t *testing.T) {
	needDisplay(t)
	dir := filepath.Join(t.TempDir(), "state")
	at := time.Now().Add(-time.Hour).Truncate(time.Minute)
	st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return at }})
	body := "―― 00:45:36 - System update ――\n:: Synchronizing package databases...\n -> cooldown: holding back topgrade\n" +
		"―― 00:45:44 - Flatpak User Packages ――\nNothing to update.\n"
	if err := st.Append(logstore.Topgrade, "OK", "Topgrade completed", body); err != nil {
		t.Fatal(err)
	}
	if err := st.Append(logstore.Audit, "OK", "AUR audit", "3 foreign packages checked\n"); err != nil {
		t.Fatal(err)
	}
	// The fake clock stamped the folder an hour back; the files' own
	// times say when they were written, which the last run cell matches
	// against the record.
	for _, c := range []logstore.Category{logstore.Topgrade, logstore.Audit} {
		if err := os.Chtimes(st.Path(c), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WriteLastRun(logstore.RunRecord{Action: "topgrade", Outcome: logstore.OutcomeOK, Started: at, Finished: at}); err != nil {
		t.Fatal(err)
	}
	before := stateTree(t, dir)
	run := at.Format("20060102-1504")
	rep, _ := child(t, "logs", "ARCHMAID_GUI_STATE="+dir, "ARCHMAID_GUI_LOG="+run+"/topgrade", "ARCHMAID_GUI_LOGSEARCH=COOLDOWN")

	// The Overview's last run cell names the run's logs, and Open in Logs
	// shows that run on the Logs page.
	if rep.LastFiles != "topgrade.log · audit.log" || rep.LastOpened != "logs "+run+"/topgrade" {
		t.Errorf("last run files %q opened %q", rep.LastFiles, rep.LastOpened)
	}
	if want := []string{run + "/audit", run + "/topgrade"}; rep.Page != "logs" || !slices.Equal(rep.LogRows, want) {
		t.Errorf("page %q rows %q, want logs and %q", rep.Page, rep.LogRows, want)
	}
	lines := []string{"Topgrade completed", "SYSTEM UPDATE", ":: Synchronizing package databases...",
		" -> cooldown: holding back topgrade", "FLATPAK USER PACKAGES", "Nothing to update."}
	if rep.LogShown != run+"/topgrade" || !slices.Equal(rep.LogLines, lines) {
		t.Errorf("shown %q lines\n%q\nwant\n%q", rep.LogShown, rep.LogLines, lines)
	}
	if want := []string{"Topgrade completed", "SYSTEM UPDATE", " -> cooldown: holding back topgrade"}; !slices.Equal(rep.LogSearch, want) {
		t.Errorf("search %q, want %q", rep.LogSearch, want)
	}
	if want := []string{"Topgrade completed", "SYSTEM UPDATE", "FLATPAK USER PACKAGES", "Nothing to update."}; !slices.Equal(rep.LogFolded, want) {
		t.Errorf("folded %q, want %q", rep.LogFolded, want)
	}
	if want := []string{"folder " + filepath.Join(dir, run)}; !slices.Equal(rep.Launched, want) {
		t.Errorf("launched %q, want %q", rep.Launched, want)
	}
	if after := stateTree(t, dir); !slices.Equal(before, after) {
		t.Errorf("the GUI changed the state dir:\n%q\n%q", before, after)
	}
}
