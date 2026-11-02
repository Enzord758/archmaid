package pkgdbtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

func open(t *testing.T, dir string, repos ...string) *pkgdb.DB {
	t.Helper()
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), repos...)
	db, err := pkgdb.Open(pkgdb.Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// TestRoundTrip: what the fixture writer puts in, pkgdb reads back, through
// both compressions, with the installed state merged from the local tree.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	built := time.Unix(1760000000, 0)
	full := pkgdb.Package{
		Name: "app", Version: "2.0-1", Description: "An app", URL: "https://example.org", Licenses: []string{"MIT", "GPL"},
		Groups: []string{"tools"}, Packager: "Dev", BuildDate: built, DownloadSize: 100, InstalledSize: 400,
		Depends:    []pkgdb.Dep{{Name: "lib", Raw: "lib>=1"}},
		OptDepends: []pkgdb.Dep{{Name: "gui", Raw: "gui", Desc: "for the GUI"}, {Name: "plain", Raw: "plain"}},
		Conflicts:  []pkgdb.Dep{{Name: "oldapp", Raw: "oldapp"}},
		Replaces:   []pkgdb.Dep{{Name: "appold", Raw: "appold"}},
		Provides:   []pkgdb.Dep{{Name: "application", Raw: "application=2"}},
	}
	lib := pkgdb.Package{Name: "lib", Version: "1-1", Description: "A library"}
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "core", true, full)
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "extra", false, lib, pkgdb.Package{Name: "app", Version: "1.0-1"})
	inst := lib
	inst.Reason, inst.InstallDate, inst.InstalledSize = pkgdb.ReasonDep, built, 99
	old := full
	old.Version, old.Reason = "1.5-1", pkgdb.ReasonExplicit
	old.InstalledVersion = "1.5-1"
	pkgdbtest.WriteLocal(t, filepath.Join(dir, "local"), inst, old)

	db := open(t, dir, "core", "extra")
	app, ok := db.Lookup("app")
	if !ok {
		t.Fatal("app missing")
	}
	if app.Repo != "core" || len(app.AlsoIn) != 1 || app.AlsoIn[0] != "extra" || app.Description != "An app" ||
		strings.Join(app.Licenses, ",") != "MIT,GPL" || strings.Join(app.Groups, ",") != "tools" ||
		app.Packager != "Dev" || !app.BuildDate.Equal(built) || app.DownloadSize != 100 || app.InstalledSize != 400 {
		t.Fatalf("app = %+v", app)
	}
	if app.OptDepends[0].Desc != "for the GUI" || app.OptDepends[0].Name != "gui" || app.OptDepends[1].Desc != "" ||
		app.Depends[0].Raw != "lib>=1" || app.Depends[0].Name != "lib" || app.Provides[0].Name != "application" ||
		app.Conflicts[0].Name != "oldapp" || app.Replaces[0].Name != "appold" {
		t.Fatalf("app deps = %+v", app)
	}
	if !app.Installed || app.InstalledVersion != "1.5-1" || !app.Update || app.Reason != pkgdb.ReasonExplicit {
		t.Fatalf("app installed state = %+v", app)
	}
	l, _ := db.Lookup("lib")
	if l.Reason != pkgdb.ReasonDep || l.Orphan || len(l.RequiredBy) != 1 || l.RequiredBy[0] != "app" || !l.InstallDate.Equal(built) {
		t.Fatalf("lib = %+v, want a dependency required by app", l)
	}
	// Search: a provides name finds app, ranked below a name hit.
	if got := db.Search("application", 10); len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("provides search = %+v", got)
	}
	// Both repos hold lib: pacman.conf order decides, and the reverse flips it.
	if got := db.Search("lib", 10); len(got) == 0 || got[0].Name != "lib" || got[0].Repo != "extra" {
		t.Fatalf("lib search = %+v", got)
	}
}

func TestSearchTiesFollowRepoOrderThenName(t *testing.T) {
	dir := t.TempDir()
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "a", false, pkgdb.Package{Name: "zz-tool", Version: "1"}, pkgdb.Package{Name: "aa-tool", Version: "1"})
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "b", false, pkgdb.Package{Name: "mm-tool", Version: "1"})
	var got []string
	for _, p := range open(t, dir, "b", "a").Search("tool", 10) {
		got = append(got, p.Repo+"/"+p.Name)
	}
	if strings.Join(got, " ") != "b/mm-tool a/aa-tool a/zz-tool" {
		t.Fatalf("order = %v, want repo order then name", got)
	}
}

func TestCapsOnLists(t *testing.T) {
	dir := t.TempDir()
	var groups []string
	for i := 0; i < 600; i++ {
		groups = append(groups, "g")
	}
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "r", false, pkgdb.Package{Name: "x", Version: "1", Groups: groups})
	p, _ := open(t, dir, "r").Lookup("x")
	if len(p.Groups) != 512 {
		t.Fatalf("groups = %d, want the list capped at 512", len(p.Groups))
	}
}
