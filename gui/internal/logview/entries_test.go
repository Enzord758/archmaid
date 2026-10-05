package logview

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// topgradeBody is what topgrade printed, as the engine logs it: a line
// before the first step, then its separators (U+2015 bars).
const topgradeBody = "starting\n" +
	"―― 00:45:36 - System update ――\n" +
	":: Synchronizing package databases...\n" +
	"\x1b[1m -> cooldown: holding back topgrade\x1b[0m\n" +
	"―― 00:45:44 - Flatpak User Packages ――\n" +
	"Nothing to update.\n" +
	"―――― oh-my-zsh ――――\n" +
	"Oh My Zsh is already at the latest version.\n"

// readBack writes entries with the real logstore and reads them as the
// Logs page does.
func readBack(t *testing.T) []Entry {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	at := time.Date(2026, 10, 5, 0, 45, 0, 0, time.Local)
	st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return at }})
	must(t, st.Append(logstore.Topgrade, "OK", "Topgrade completed", topgradeBody))
	at = at.Add(3 * time.Minute)
	must(t, st.Append(logstore.Topgrade, "FAIL", "Topgrade failed", ""))
	runs, err := Runs(dir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v %v", runs, err)
	}
	lines, err := runs[0].Read(logstore.Topgrade)
	if err != nil {
		t.Fatal(err)
	}
	return Parse(lines)
}

func TestParseEntries(t *testing.T) {
	es := readBack(t)
	if len(es) != 2 {
		t.Fatalf("%d entries: %+v", len(es), es)
	}
	ok, fail := es[0], es[1]
	if ok.Kind != "OK" || ok.Label != "Topgrade completed" || !ok.Time.Equal(time.Date(2026, 10, 5, 0, 45, 0, 0, time.Local)) {
		t.Errorf("first header: %+v", ok)
	}
	want := []Section{
		{Lines: []string{"starting"}},
		{Title: "System update", Time: "00:45:36", Lines: []string{":: Synchronizing package databases...", " -> cooldown: holding back topgrade"}},
		{Title: "Flatpak User Packages", Time: "00:45:44", Lines: []string{"Nothing to update."}},
		{Title: "oh-my-zsh", Lines: []string{"Oh My Zsh is already at the latest version."}},
	}
	if !reflect.DeepEqual(ok.Sections, want) {
		t.Errorf("sections\n%+v\nwant\n%+v", ok.Sections, want)
	}
	if ok.Lines() != 5 {
		t.Errorf("lines %d", ok.Lines())
	}
	if fail.Kind != "FAIL" || fail.Label != "Topgrade failed" || fail.Sections != nil {
		t.Errorf("an entry without output: %+v", fail)
	}
}

// TestParseOldLog: a log from an older version holds raw escapes and may
// start without a header; it reads plain, nothing is dropped, and a
// header-like line inside output stays output only when it doesn't match.
func TestParseOldLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	run := filepath.Join(dir, "20261001-0930")
	must(t, os.MkdirAll(run, 0o700))
	raw := "stray \x1b]0;title\x07line\n[2026-10-01 09:30:00] OK: Clearing \x1b[31mcache\x1b[0m\n" +
		"--- Clearing cache output ---\n\x1b[1mremoved\x1b[0m 3 files\n--- end output ---\n"
	must(t, os.WriteFile(filepath.Join(run, "cleanup.log"), []byte(raw), 0o600))
	runs, err := Runs(dir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v %v", runs, err)
	}
	lines, err := runs[0].Read(logstore.Cleanup)
	if err != nil {
		t.Fatal(err)
	}
	es := Parse(lines)
	for _, e := range es {
		for _, s := range e.Sections {
			for _, l := range s.Lines {
				if strings.ContainsRune(l, '\x1b') || strings.ContainsRune(l, '\a') {
					t.Errorf("escape left in %q", l)
				}
			}
		}
	}
	if len(es) != 2 || es[0].Kind != "" || es[0].Lines() != 1 {
		t.Fatalf("entries %+v", es)
	}
	if e := es[1]; e.Kind != "OK" || e.Label != "Clearing cache" || len(e.Sections) != 1 || !reflect.DeepEqual(e.Sections[0].Lines, []string{"removed 3 files"}) {
		t.Errorf("entry %+v", e)
	}
}

func TestFilter(t *testing.T) {
	es := readBack(t)
	if got := Filter(es, "  "); !reflect.DeepEqual(got, es) {
		t.Error("an empty query changed the entries")
	}
	got := Filter(es, "COOLDOWN")
	if len(got) != 1 || len(got[0].Sections) != 1 || got[0].Sections[0].Title != "System update" ||
		!reflect.DeepEqual(got[0].Sections[0].Lines, []string{" -> cooldown: holding back topgrade"}) {
		t.Errorf("cooldown: %+v", got)
	}
	if got := Filter(es, "flatpak"); len(got) != 1 || len(got[0].Sections) != 1 || len(got[0].Sections[0].Lines) != 1 {
		t.Errorf("a section title match keeps its section: %+v", got)
	}
	if got := Filter(es, "fail"); len(got) != 1 || got[0].Kind != "FAIL" {
		t.Errorf("a header match keeps the entry: %+v", got)
	}
	if got := Filter(es, "nothing like it"); got != nil {
		t.Errorf("no match: %+v", got)
	}
	if es[0].Sections[1].Lines[1] != " -> cooldown: holding back topgrade" {
		t.Error("Filter changed its input")
	}
}
