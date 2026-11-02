package pkgview

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func TestRecentRows(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	at := func(name string, ago time.Duration) pkgdb.Package {
		p := installed(repoPkg(name, "1-1"))
		p.InstallDate = now.Add(-ago)
		return p
	}
	cat := &fakeCatalog{pkgs: []pkgdb.Package{
		at("old", 9*24*time.Hour), at("new", 30*time.Second), at("mid", 5*time.Hour), repoPkg("never", "1-1"), at("future", -time.Hour),
	}}
	rows := RecentRows(cat, now, 3)
	var got []string
	for _, r := range rows {
		got = append(got, r.Pkg.Name+" "+r.Ago)
	}
	want := []string{"future just now", "new just now", "mid 5 h ago"}
	if len(got) != len(want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows %q, want %q", got, want)
		}
	}
	if RecentRows(cat, now, 0) != nil || RecentRows(nil, now, 3) != nil {
		t.Fatal("no rows asked for, none expected")
	}
	if rows := RecentRows(cat, now, 10); len(rows) != 4 || rows[3].Pkg.Name != "old" || rows[3].Ago != "9 days ago" {
		t.Fatalf("all rows: %+v (a package with no install date is left out)", rows)
	}
}
