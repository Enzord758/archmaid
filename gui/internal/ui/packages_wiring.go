package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/frontend"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/proc"
)

// The Packages page on the real machine (docs/design/packages-contract.md,
// sections 2 and 5): the page reads pacman's databases with pkgdb, asks the
// AUR with aursearch, and plans an AUR install with aurbuild. All of it runs
// as the user and only reads pacman's files; nothing here starts pacman for
// anything but `pacman-conf` (to learn where the databases are).

// pkgSource says where the real page gets its data. The zero value is the
// real machine; a test points every field at a scratch folder.
type pkgSource struct {
	// Paths finds pacman's databases; nil asks `pacman-conf DBPath`.
	Paths func(ctx context.Context) (pkgdb.Paths, error)
	// BaseURL is the AUR RPC root; "" is aursearch.DefaultBaseURL.
	BaseURL string
	// StateDir is archmaid's state folder (the audit's snapshot lives in
	// it); "" is logstore's default.
	StateDir string
	// CacheRoot is where AUR repos are fetched; "" is aurbuild.DefaultRoot.
	CacheRoot string
}

// realPackages is the page's data on the real machine, opened once at
// start: the databases, the AUR client, and the planner of an AUR install
// (Config.PlanAUR of the Driver, built over the same databases).
type realPackages struct {
	DB  *pkgdb.DB
	AUR aursearch.Searcher
	// PlanAUR is realdriver.Config.PlanAUR; nil when the databases could
	// not be opened or there is no cache folder (an AUR run then fails
	// closed with a notice).
	PlanAUR func(ctx context.Context, names []string) (actions.AURPrepared, error)
	// Err says why DB is nil.
	Err string
}

// openRealPackages opens the databases (100-300 ms: before the window is
// up, not on its thread) and builds the AUR planner over them.
func openRealPackages(src pkgSource) *realPackages {
	p := &realPackages{}
	base := src.BaseURL
	if base == "" {
		base = aursearch.DefaultBaseURL
	}
	p.AUR = aursearch.New(aursearch.Config{BaseURL: base})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	find := src.Paths
	if find == nil {
		find = pacmanPaths
	}
	paths, err := find(ctx)
	if err == nil {
		p.DB, err = pkgdb.Open(paths)
	}
	if err != nil {
		p.Err = "pacman's databases could not be read: " + oneLineErr(err)
		return p
	}

	root := src.CacheRoot
	if root == "" {
		if root, err = aurbuild.DefaultRoot(); err != nil {
			return p // no cache folder: the page works, an AUR install says it can't
		}
	}
	state := logstore.New(logstore.Options{Dir: src.StateDir}).Root()
	fetcher := aurbuild.Fetcher{Root: root}
	planner := aurbuild.Planner{Fetcher: fetcher, Repo: p.DB, AUR: p.AUR}
	var reviewed *aurbuild.Reviewed
	if state != "" {
		reviewed = &aurbuild.Reviewed{Path: reviewedPath(state)}
		planner.Reviewed = reviewed
		// The maintainer audit of the AUR packages, over the same snapshot
		// as the menu's audit; its foreign packages come from the database
		// (pacman -Qm's rule), so it starts nothing. It logs nowhere: the
		// run's own store gets the accept (the engine commits it).
		cfg := frontend.AuditConfig(logstore.New(logstore.Options{Dir: state}), frontend.AuditOptions{
			RPCURL: auditRPCURL(base),
			Foreign: func(context.Context) ([]string, error) {
				var names []string
				for _, f := range p.DB.Installed(pkgdb.ViewForeign) {
					names = append(names, f.Name)
				}
				return names, nil
			},
		})
		cfg.Log = nil
		planner.Audit = &cfg
	}
	p.PlanAUR = frontend.AURPlanner(planner, fetcher, reviewed)
	return p
}

// reviewedPath is the AUR reviews' file in the state folder: the same file
// as aurbuild.DefaultReviewed, in the folder the page was given (a test's).
func reviewedPath(state string) string { return filepath.Join(state, aurbuild.ReviewedFile) }

// auditRPCURL is the audit's endpoint for the page's AUR base: the default
// base is the audit's default too; a test's server serves the audit as well.
func auditRPCURL(base string) string {
	if base == aursearch.DefaultBaseURL {
		return "" // the audit's own default
	}
	return base
}

// wiring is the page's PackagesWiring.
func (p *realPackages) wiring() *PackagesWiring {
	if p == nil {
		return nil
	}
	if p.DB == nil {
		return &PackagesWiring{Err: p.Err}
	}
	db := p.DB
	return &PackagesWiring{
		Catalog: db,
		AUR:     p.AUR,
		DBAge: func() time.Duration {
			if db.SyncTime().IsZero() {
				return -1
			}
			return db.SyncAge(time.Now())
		},
		// After a run, a topgrade or when the page is shown: re-read the
		// databases if their files changed since the last read.
		Reload:    func() bool { changed, _ := db.Reload(); return changed },
		Protected: protectedQuery,
	}
}

// pacmanPaths asks `pacman-conf DBPath` where the databases are, as the user
// (it only reads pacman.conf). The sync and local folders are under it; the
// conf is pacman's default.
func pacmanPaths(ctx context.Context) (pkgdb.Paths, error) {
	out, err := proc.Query(ctx, "pacman-conf", "DBPath")
	if err != nil {
		return pkgdb.Paths{}, fmt.Errorf("pacman-conf DBPath: %w", err)
	}
	return pathsFromDBPath(string(out))
}

// pathsFromDBPath turns pacman-conf's DBPath answer into the databases'
// paths. It must be one absolute path.
func pathsFromDBPath(out string) (pkgdb.Paths, error) {
	dbPath := strings.TrimSpace(out)
	if dbPath == "" || strings.ContainsAny(dbPath, "\n\x00") || !filepath.IsAbs(dbPath) {
		return pkgdb.Paths{}, fmt.Errorf("pacman-conf DBPath gave %q, want one absolute path", clipText(out, 60))
	}
	return pkgdb.Paths{Sync: filepath.Join(dbPath, "sync"), Local: filepath.Join(dbPath, "local"), Conf: pacmanConf}, nil
}

// pacmanConf is pacman's own default configuration file.
const pacmanConf = "/etc/pacman.conf"

func oneLineErr(err error) string {
	return clipText(strings.Join(strings.Fields(err.Error()), " "), 160)
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// protectedQuery asks the helper for the packages it never removes
// (helperclient.Protected: no polkit, no root). Nothing is wired yet, so the
// page keeps its built-in list.
var protectedQuery func(ctx context.Context) ([]string, error)
