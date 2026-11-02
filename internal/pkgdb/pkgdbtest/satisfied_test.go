package pkgdbtest_test

import (
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
)

// TestSatisfied: a dependency is met by a package of that name at a
// satisfying version, or by one that provides it (a soname, a virtual).
func TestSatisfied(t *testing.T) {
	dir := t.TempDir()
	libcap := pkgdb.Package{Name: "libcap", Version: "2.77-1", Repo: "core",
		Provides: []pkgdb.Dep{{Name: "libcap.so", Raw: "libcap.so=2-64"}, {Name: "libpsx.so", Raw: "libpsx.so=2-64"}}}
	bash := pkgdb.Package{Name: "bash", Version: "5.3.3-1", Repo: "core",
		Provides: []pkgdb.Dep{{Name: "sh", Raw: "sh"}}}
	glibc := pkgdb.Package{Name: "glibc", Version: "2.42-1", Repo: "core"}
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "core", false, libcap, bash, glibc)
	for _, p := range []pkgdb.Package{libcap, bash, glibc} {
		p.Installed = true
		pkgdbtest.WriteLocal(t, filepath.Join(dir, "local"), p)
	}
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), "core")
	db, err := pkgdb.Open(pkgdb.Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")})
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]bool{
		"glibc":            true,
		"glibc>=2.38":      true,
		"glibc>=3":         false,
		"libcap.so=2-64":   true, // a soname, provided
		"libcap.so=3-64":   false,
		"libcap.so":        true,
		"libpsx.so>=2-64":  true,
		"sh":               true,  // a virtual
		"sh>=1":            false, // unversioned provide, versioned dep
		"libncursesw.so=6": false,
		"nosuch":           false,
		"":                 false,
	} {
		if got := db.Satisfied(raw); got != want {
			t.Errorf("Satisfied(%q) = %v, want %v", raw, got, want)
		}
	}
}
