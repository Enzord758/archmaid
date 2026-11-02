package pkgview

import (
	"fmt"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func TestInstalledViews(t *testing.T) {
	dep := installed(repoPkg("libx", "1"))
	dep.Reason = pkgdb.ReasonDep
	orphan := installed(repoPkg("liborph", "1"))
	orphan.Reason, orphan.Orphan = pkgdb.ReasonDep, true
	foreign := installed(pkgdb.Package{Name: "myapp", Version: "1", Foreign: true})
	foreign.Repo = ""
	cat := &fakeCatalog{pkgs: []pkgdb.Package{installed(repoPkg("vim", "9")), installed(repoPkg("glibc", "2")), dep, orphan, foreign, repoPkg("nano", "1")}}
	v := NewInstalled(cat, NewAdvisoryProtected())
	if v.Count(pkgdb.ViewExplicit) != 3 || v.Count(pkgdb.ViewDependencies) != 1 || v.Count(pkgdb.ViewOrphans) != 1 || v.Count(pkgdb.ViewForeign) != 1 {
		t.Fatalf("counts %d %d %d %d", v.Count(pkgdb.ViewExplicit), v.Count(pkgdb.ViewDependencies), v.Count(pkgdb.ViewOrphans), v.Count(pkgdb.ViewForeign))
	}
	rows, total := v.Rows()
	if total != 3 {
		t.Fatalf("explicit total %d", total)
	}
	by := map[string]InstalledRow{}
	for _, r := range rows {
		by[r.Pkg.Name] = r
	}
	if !by["vim"].Removable {
		t.Error("vim not removable")
	}
	if g := by["glibc"]; g.Removable || g.Why != ProtectedReason {
		t.Errorf("glibc: %+v", g)
	}
	v.SetView(pkgdb.ViewOrphans)
	rows, _ = v.Rows()
	if len(rows) != 1 || rows[0].Removable || rows[0].Why == "" {
		t.Errorf("orphans must not be removable here: %+v", rows)
	}
	v.SetView(pkgdb.ViewForeign)
	if rows, _ = v.Rows(); len(rows) != 1 || !rows[0].Removable {
		t.Errorf("foreign: %+v", rows)
	}
}

func TestInstalledFilterAndCap(t *testing.T) {
	var ps []pkgdb.Package
	for i := 0; i < InstalledShown+30; i++ {
		ps = append(ps, installed(repoPkg(fmt.Sprintf("pkg%03d", i), "1")))
	}
	ps = append(ps, installed(func() pkgdb.Package { p := repoPkg("zzz", "1"); p.Description = "A Terminal thing"; return p }()))
	v := NewInstalled(&fakeCatalog{pkgs: ps}, nil)
	rows, total := v.Rows()
	if len(rows) != InstalledShown || total != InstalledShown+31 {
		t.Fatalf("rows %d total %d", len(rows), total)
	}
	v.SetFilter("  TERMINAL ")
	rows, total = v.Rows()
	if len(rows) != 1 || rows[0].Pkg.Name != "zzz" || total != 1 {
		t.Fatalf("filter by description: %+v", rows)
	}
	v.SetFilter("pkg01")
	if rows, _ = v.Rows(); len(rows) != 10 {
		t.Fatalf("filter by name: %d rows", len(rows))
	}
}

func TestOrphansNote(t *testing.T) {
	if OrphansNote(0) != "" || OrphansNote(1) != "1 orphan. Full Cleanup removes them; they are listed here to look at." {
		t.Fatal(OrphansNote(1))
	}
}

func TestVerCmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0-1", "1.0-1", 0},
		{"1.0-1", "1.0-2", -1},
		{"1.10-1", "1.9-1", 1},
		{"1:1.0-1", "2.0-1", 1},
		{"1.0a-1", "1.0-1", -1},
		{"1.0-1", "1.0.1-1", -1},
		{"2.0rc1-1", "2.0-1", -1},
		{"1.0-1", "1.0", 0},
		{"20260101-1", "20251231-1", 1},
		{"1.0+r12.gabc-1", "1.0+r9.gdef-1", 1},
	}
	for _, c := range cases {
		if got := VerCmp(c.a, c.b); got != c.want {
			t.Errorf("VerCmp(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := VerCmp(c.b, c.a); got != -c.want {
			t.Errorf("VerCmp(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestSizeAndAge(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536 * 1024: "1.5 MiB", 300 << 20: "300 MiB", 5 << 30: "5.0 GiB"} {
		if got := Size(in); got != want {
			t.Errorf("Size(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[time.Duration]string{10 * time.Second: "just now", 5 * time.Minute: "5 min", 3 * time.Hour: "3 h", 72 * time.Hour: "3 days"} {
		if got := Age(in); got != want {
			t.Errorf("Age(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFakeClockOrder(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	var got []int
	c.AfterFunc(2*time.Second, func() { got = append(got, 2) })
	c.AfterFunc(time.Second, func() { got = append(got, 1) })
	t3 := c.AfterFunc(3*time.Second, func() { got = append(got, 3) })
	t3.Stop()
	c.Advance(5 * time.Second)
	if fmt.Sprint(got) != "[1 2]" || c.Pending() != 0 {
		t.Fatalf("%v pending %d", got, c.Pending())
	}
}
