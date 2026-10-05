package logview

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

func TestRows(t *testing.T) {
	es := readBack(t)
	var got []string
	for _, r := range Rows(es, nil) {
		got = append(got, map[RowKind]string{RowEntry: "E", RowSection: "S", RowLine: "L"}[r.Kind]+" "+r.Text+" | "+r.Aside)
	}
	want := []string{
		"E Topgrade completed | OK · 00:45:00",
		"L starting | ",
		"S SYSTEM UPDATE | 00:45:36",
		"L :: Synchronizing package databases... | ",
		"L  -> cooldown: holding back topgrade | ",
		"S FLATPAK USER PACKAGES | 00:45:44",
		"L Nothing to update. | ",
		"S OH-MY-ZSH | ",
		"L Oh My Zsh is already at the latest version. | ",
		"E Topgrade failed | FAIL · 00:48:00",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows\n%q\nwant\n%q", got, want)
	}

	// Collapsing a titled section hides its lines and counts them; the
	// untitled first section can't collapse.
	rows := Rows(es, map[SectionKey]bool{{0, 1}: true, {0, 0}: true})
	if len(rows) != len(want)-2 {
		t.Fatalf("%d rows collapsed", len(rows))
	}
	if r := rows[2]; r.Kind != RowSection || r.Aside != "2 lines" || r.Entry != 0 || r.Section != 1 {
		t.Errorf("collapsed section row %+v", r)
	}
	if r := rows[1]; r.Kind != RowLine || r.Text != "starting" {
		t.Errorf("the untitled section collapsed: %+v", r)
	}
}

func TestRunDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	write(t, dir, time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local), false, logstore.Topgrade, "x")
	write(t, dir, time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local), true, logstore.Topgrade, "x")
	runs, err := Runs(dir)
	if err != nil || len(runs) != 2 {
		t.Fatalf("%v %v", runs, err)
	}
	if runs[0].Dir() != filepath.Join(dir, "20261001-0930") || runs[1].Dir() != filepath.Join(dir, DryRunName) {
		t.Errorf("dirs %q %q", runs[0].Dir(), runs[1].Dir())
	}
	if (Run{}).Dir() != "" {
		t.Error("a zero Run has a dir")
	}
}
