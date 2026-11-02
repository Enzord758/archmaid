package aurbuild

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// aurPkg is one package of the fake AUR RPC.
type aurPkg struct {
	Name, Base, Version                string
	Depends, Makedepends, Checkdepends []string
}

// aurClient serves aurPkgs as RPC v5 /info and returns a real client on it.
// Every request is counted in *calls.
func aurClient(t *testing.T, calls *int, pkgs ...aurPkg) *aursearch.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		r.ParseForm()
		var res []map[string]any
		for _, want := range r.Form["arg[]"] {
			for _, p := range pkgs {
				if p.Name == want {
					base := p.Base
					if base == "" {
						base = p.Name
					}
					res = append(res, map[string]any{"Name": p.Name, "PackageBase": base, "Version": p.Version,
						"Depends": p.Depends, "MakeDepends": p.Makedepends, "CheckDepends": p.Checkdepends})
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"version": 5, "type": "multiinfo", "resultcount": len(res), "results": res})
	}))
	t.Cleanup(srv.Close)
	return aursearch.New(aursearch.Config{BaseURL: srv.URL + "/rpc/v5"})
}

// fixtureDB is a pkgdb.DB over a core repo and an installed set.
func fixtureDB(t *testing.T, syncPkgs, installed []pkgdb.Package) *pkgdb.DB {
	t.Helper()
	dir := t.TempDir()
	syncDir, localDir := filepath.Join(dir, "sync"), filepath.Join(dir, "local")
	pkgdbtest.WriteSync(t, syncDir, "core", false, append(syncPkgs, syncPkg("filler", "1-1"))...)
	pkgdbtest.WriteLocal(t, localDir, append(installed, instPkg("filler", "1-1"))...)
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), "core")
	db, err := pkgdb.Open(pkgdb.Paths{Sync: syncDir, Local: localDir, Conf: filepath.Join(dir, "pacman.conf")})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func syncPkg(name, ver string) pkgdb.Package { return pkgdb.Package{Name: name, Version: ver} }

func instPkg(name, ver string, provides ...string) pkgdb.Package {
	p := pkgdb.Package{Name: name, Version: ver, InstalledVersion: ver, Installed: true, Reason: pkgdb.ReasonExplicit}
	for _, pr := range provides {
		p.Provides = append(p.Provides, pkgdb.Dep{Name: DepName(pr), Raw: pr})
	}
	return p
}
