package pkgview

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// fakeCatalog is a fixture DB: Search matches names containing q, in
// the order given (the ranking is pkgdb's, not under test here).
type fakeCatalog struct {
	pkgs  []pkgdb.Package
	calls []string
}

func (c *fakeCatalog) Search(q string, max int) []pkgdb.Package {
	c.calls = append(c.calls, q)
	var out []pkgdb.Package
	for _, p := range c.pkgs {
		if strings.Contains(p.Name, q) {
			out = append(out, p)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func (c *fakeCatalog) Lookup(name string) (pkgdb.Package, bool) {
	for _, p := range c.pkgs {
		if p.Name == name {
			return p, true
		}
	}
	return pkgdb.Package{}, false
}

func (c *fakeCatalog) Installed(v pkgdb.View) []pkgdb.Package {
	var out []pkgdb.Package
	for _, p := range c.pkgs {
		if !p.Installed {
			continue
		}
		switch v {
		case pkgdb.ViewExplicit:
			if p.Reason == pkgdb.ReasonExplicit {
				out = append(out, p)
			}
		case pkgdb.ViewDependencies:
			if p.Reason == pkgdb.ReasonDep && !p.Orphan {
				out = append(out, p)
			}
		case pkgdb.ViewOrphans:
			if p.Orphan {
				out = append(out, p)
			}
		case pkgdb.ViewForeign:
			if p.Foreign {
				out = append(out, p)
			}
		}
	}
	return out
}

func repoPkg(name, ver string) pkgdb.Package {
	return pkgdb.Package{Name: name, Version: ver, Repo: "extra", Description: name + " description", DownloadSize: 2 << 20, InstalledSize: 8 << 20}
}

func aurPkg(name, ver string) pkgdb.Package {
	return pkgdb.Package{Name: name, Version: ver, Repo: pkgdb.RepoAUR, AUR: &pkgdb.AUR{PackageBase: name, Maintainer: "someone", Votes: 12}}
}

func installed(p pkgdb.Package) pkgdb.Package {
	p.Installed, p.InstalledVersion, p.Reason = true, p.Version, pkgdb.ReasonExplicit
	return p
}

// fakeAUR scripts the AUR client: every call is recorded, and a call
// blocks until released when hold is set. A cancelled context returns its
// error, as the real client does.
type fakeAUR struct {
	mu       sync.Mutex
	searches []string
	infos    [][]string
	ctxs     []context.Context
	hits     []pkgdb.Package
	err      error
	info     map[string]pkgdb.Package
}

func (f *fakeAUR) Search(ctx context.Context, term string) ([]pkgdb.Package, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, term)
	f.ctxs = append(f.ctxs, ctx)
	return f.hits, f.err
}

func (f *fakeAUR) Info(ctx context.Context, names []string) ([]pkgdb.Package, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infos = append(f.infos, slices.Clone(names))
	if f.err != nil {
		return nil, f.err
	}
	var out []pkgdb.Package
	for _, n := range names {
		if p, ok := f.info[n]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

var errAUR = errors.New("aur: Get \"https://aur.archlinux.org/rpc/v5/search/x\": dial tcp: no route to host")

// rig is a Search wired inline on a fake clock.
type rig struct {
	cat     *fakeCatalog
	aur     *fakeAUR
	clock   *FakeClock
	s       *Search
	changed int
}

func newRig(pkgs ...pkgdb.Package) *rig {
	r := &rig{cat: &fakeCatalog{pkgs: pkgs}, aur: &fakeAUR{info: map[string]pkgdb.Package{}}, clock: NewFakeClock(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC))}
	r.s = NewSearch(Deps{Catalog: r.cat, AUR: r.aur, Clock: r.clock,
		Go: func(f func()) { f() }, Post: func(f func()) { f() }, Changed: func() { r.changed++ }})
	return r
}

type fakeBackend struct {
	started []steps.PkgRequest
	answers []string
	aborts  int
	updates int
}

func (b *fakeBackend) StartPackages(r steps.PkgRequest) { b.started = append(b.started, r) }
func (b *fakeBackend) Answer(p int, yes bool) {
	b.answers = append(b.answers, strings.Join([]string{string(rune('0' + p)), map[bool]string{true: "yes", false: "no"}[yes]}, " "))
}
func (b *fakeBackend) Abort()        { b.aborts++ }
func (b *fakeBackend) UpdateSystem() { b.updates++ }

// aurStopCtx is the context of the AUR request started last.
func (s *Search) aurStopCtx() context.Context { return s.aurCtx }
