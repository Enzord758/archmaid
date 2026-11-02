package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// realOptions on the real machine's wiring, with no real pacman: the
// databases are a fixture folder (pkgdbtest), `pacman-conf` is not asked
// (testenv traps it), the AUR is an httptest server and the state and the
// AUR cache are scratch folders.

func writeDBs(t *testing.T) pkgdb.Paths {
	t.Helper()
	dir := t.TempDir()
	core := pkgdb.Package{Name: "glibc", Version: "2.40-1", Description: "GNU C Library"}
	extra := pkgdb.Package{Name: "vim", Version: "9.1-1", Description: "Vi Improved"}
	yay := pkgdb.Package{Name: "yay", Version: "12.0-1", Description: "AUR helper", Installed: true, Reason: pkgdb.ReasonExplicit}
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "core", true, core)
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "extra", false, extra)
	installed := func(p pkgdb.Package, days int) pkgdb.Package {
		p.Installed, p.InstalledVersion, p.Reason = true, p.Version, pkgdb.ReasonExplicit
		p.InstallDate = time.Now().AddDate(0, 0, -days)
		return p
	}
	pkgdbtest.WriteLocal(t, filepath.Join(dir, "local"), installed(core, 30), installed(extra, 2), installed(yay, 1))
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), "core", "extra")
	return pkgdb.Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")}
}

// fakeRPC answers every AUR request with no package, and counts them.
func fakeRPC(t *testing.T) (url string, calls *atomic.Int32) {
	t.Helper()
	calls = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"version": 5, "type": "multiinfo", "resultcount": 0, "results": []any{}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, calls
}

func TestRealOptionsBuildWithoutARealPacman(t *testing.T) {
	paths := writeDBs(t)
	rpc, calls := fakeRPC(t)
	state, cache := t.TempDir(), t.TempDir()
	pk := openRealPackages(pkgSource{
		Paths:   func(context.Context) (pkgdb.Paths, error) { return paths, nil },
		BaseURL: rpc, StateDir: state, CacheRoot: cache,
	})
	if pk.Err != "" || pk.DB == nil || pk.AUR == nil || pk.PlanAUR == nil {
		t.Fatalf("%+v", pk)
	}
	opts := realOptions(engine.Options{Auth: engine.AuthPolkit}, nil, nil, "", nil, pk)
	w := opts.Packages
	if w == nil || w.Catalog == nil || w.AUR == nil || w.Err != "" {
		t.Fatalf("Options.Packages = %+v", w)
	}
	// The catalog is the database: a search, a lookup and the recent installs.
	if hits := w.Catalog.Search("vim", 10); len(hits) != 1 || hits[0].Repo != "extra" {
		t.Fatalf("search: %+v", hits)
	}
	rec := w.Catalog.Recent(2)
	if len(rec) != 2 || rec[0].Name != "yay" || rec[1].Name != "vim" {
		t.Fatalf("recent: %+v", rec)
	}
	// The sync age comes from the files (just written); Reload is quiet.
	if age := w.DBAge(); age < 0 || age > time.Hour {
		t.Fatalf("DBAge = %v", age)
	}
	w.Reload()
	// The planner is over the same database and the same client: an AUR name
	// the (fake) AUR does not know is refused as not found, before any git.
	_, err := pk.PlanAUR(context.Background(), []string{"no-such-aur-package"})
	var ref *steps.PkgRefusal
	if !errors.As(err, &ref) || ref.Reason != steps.RefuseNotFound {
		t.Fatalf("PlanAUR error = %v, want a not-found refusal", err)
	}
	if calls.Load() == 0 {
		t.Fatal("the planner did not ask the AUR client")
	}
	// Nothing was written outside the scratch folders.
	for _, d := range []string{state, cache} {
		if es, _ := os.ReadDir(d); len(es) != 0 {
			t.Errorf("%s: a review alone wrote %v", d, es)
		}
	}
}

// The databases cannot be opened: the page says why and an AUR install fails
// closed (no planner).
func TestRealOptionsWithoutDatabases(t *testing.T) {
	pk := openRealPackages(pkgSource{
		Paths:   func(context.Context) (pkgdb.Paths, error) { return pkgdb.Paths{}, errors.New("pacman-conf: not found") },
		BaseURL: "http://127.0.0.1:1", StateDir: t.TempDir(), CacheRoot: t.TempDir(),
	})
	if pk.DB != nil || pk.PlanAUR != nil || !strings.Contains(pk.Err, "pacman-conf: not found") {
		t.Fatalf("%+v", pk)
	}
	w := realOptions(engine.Options{}, nil, nil, "", nil, pk).Packages
	if w == nil || w.Catalog != nil || !strings.Contains(w.Err, "could not be read") {
		t.Fatalf("%+v", w)
	}
	// And no wiring at all leaves the page "not available".
	if realOptions(engine.Options{}, nil, nil, "", nil, nil).Packages != nil {
		t.Fatal("Packages set without wiring")
	}
}

func TestPathsFromDBPath(t *testing.T) {
	p, err := pathsFromDBPath("/var/lib/pacman/\n")
	if err != nil || p.Sync != "/var/lib/pacman/sync" || p.Local != "/var/lib/pacman/local" || p.Conf != "/etc/pacman.conf" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range []string{"", "\n", "relative/path", "/a\n/b", "/a\x00b"} {
		if _, err := pathsFromDBPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
