package pkgview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
)

// TestDetailDepsMetByProvides (B3): soname and virtual dependencies are met
// by the installed package that provides them; only a dependency nothing
// meets is "pulled in".
func TestDetailDepsMetByProvides(t *testing.T) {
	dir := t.TempDir()
	libcap := pkgdb.Package{Name: "libcap", Version: "2.77-1", Repo: "core", Installed: true,
		Provides: []pkgdb.Dep{{Name: "libcap.so", Raw: "libcap.so=2-64"}}}
	ncurses := pkgdb.Package{Name: "ncurses", Version: "6.5-3", Repo: "core", Installed: true,
		Provides: []pkgdb.Dep{{Name: "libncursesw.so", Raw: "libncursesw.so=6-64"}}}
	bash := pkgdb.Package{Name: "bash", Version: "5.3-1", Repo: "core", Installed: true,
		Provides: []pkgdb.Dep{{Name: "sh", Raw: "sh"}}}
	htop := pkgdb.Package{Name: "htop", Version: "3.4-1", Repo: "extra",
		Depends: []pkgdb.Dep{
			{Name: "libcap.so", Raw: "libcap.so=2-64"},
			{Name: "libncursesw.so", Raw: "libncursesw.so=6-64"},
			{Name: "sh", Raw: "sh"},
			{Name: "libnl", Raw: "libnl>=3"},
		}}
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "core", false, libcap, ncurses, bash)
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "extra", false, htop)
	pkgdbtest.WriteLocal(t, filepath.Join(dir, "local"), libcap, ncurses, bash)
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), "core", "extra")
	db, err := pkgdb.Open(pkgdb.Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := db.Lookup("htop")
	d := BuildDetail(p, db, time.Now())
	got := map[string]DepRow{}
	for _, r := range d.Deps {
		got[r.Name] = r
	}
	for _, n := range []string{"libcap.so", "libncursesw.so", "sh"} {
		if !got[n].Installed || got[n].PulledIn {
			t.Errorf("%s should be met by a provider: %+v", n, got[n])
		}
	}
	if !got["libnl"].PulledIn {
		t.Errorf("libnl is not installed and must be pulled in: %+v", got["libnl"])
	}
}
