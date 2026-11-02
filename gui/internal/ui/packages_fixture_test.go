package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's fixtures: a catalog standing in for *pkgdb.DB, an AUR
// client that answers from a table, a backend that records. Nothing here
// reads pacman's databases or the network.

const markupDesc = "<b>Feature</b> rich AUR helper & more <i>[x]</i>"

type uiCatalog struct{ pkgs []pkgdb.Package }

func (c *uiCatalog) Search(q string, max int) []pkgdb.Package {
	q = strings.ToLower(q)
	var out []pkgdb.Package
	for _, p := range c.pkgs {
		if strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.Description), q) {
			out = append(out, p)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func (c *uiCatalog) Lookup(name string) (pkgdb.Package, bool) {
	for _, p := range c.pkgs {
		if p.Name == name {
			return p, true
		}
	}
	return pkgdb.Package{}, false
}

func (c *uiCatalog) Installed(v pkgdb.View) []pkgdb.Package {
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

func dep(name, desc string) pkgdb.Dep { return pkgdb.Dep{Name: name, Raw: name, Desc: desc} }

func pkgFixtures() []pkgdb.Package {
	mk := func(name, ver, repo, desc string, dl, inst int64) pkgdb.Package {
		return pkgdb.Package{Name: name, Version: ver, Repo: repo, Description: desc, DownloadSize: dl, InstalledSize: inst, Licenses: []string{"MPL-2.0"}}
	}
	inst := func(p pkgdb.Package, r pkgdb.Reason) pkgdb.Package {
		p.Installed, p.InstalledVersion, p.Reason = true, p.Version, r
		return p
	}
	firefox := mk("firefox", "133.0-1", "extra", "Fast, Private & Safe Web Browser", 68<<20, 260<<20)
	firefox.Depends = []pkgdb.Dep{dep("gtk3", ""), dep("libxt", ""), {Name: "nss", Raw: "nss>=3.100"}, dep("dbus-glib", "")}
	firefox.OptDepends = []pkgdb.Dep{dep("hunspell-en_us", "Spell checking, American English"), dep("pulseaudio", "Audio support"), dep("libnotify", "Notification integration")}
	gvim := mk("gvim", "9.1.0-1", "extra", "Vi Improved, with advanced features such as a GUI", 4<<20, 18<<20)
	gvim.Conflicts, gvim.Provides = []pkgdb.Dep{dep("vim", "")}, []pkgdb.Dep{dep("vim", "")}
	vim := inst(mk("vim", "9.1.0-1", "extra", "Vi Improved, a highly configurable, improved version of the vi text editor", 2<<20, 3<<20), pkgdb.ReasonExplicit)
	htop := inst(mk("htop", "3.3.0-1", "extra", "Interactive process viewer", 200<<10, 450<<10), pkgdb.ReasonExplicit)
	htop.InstalledVersion, htop.Update = "3.2.2-1", true
	glibc := inst(mk("glibc", "2.40-1", "core", "GNU C Library", 10<<20, 45<<20), pkgdb.ReasonExplicit)
	cachy := mk("linux-cachyos", "6.12.1-2", "cachyos-v3", "The Linux kernel and modules with CachyOS patches", 130<<20, 160<<20)
	mirror := mk("local-tool", "1.0-1", "enz-mirror", "A tool from the local mirror", 1<<20, 2<<20)
	libfoo := inst(mk("libfoo", "2.1-1", "extra", "Library nothing needs any more", 300<<10, 900<<10), pkgdb.ReasonDep)
	libfoo.Orphan = true
	libbar := inst(mk("libbar", "1.4-2", "extra", "Library the browser needs", 300<<10, 1<<20), pkgdb.ReasonDep)
	libbar.RequiredBy = []string{"firefox"}
	gtk3 := inst(mk("gtk3", "3.24-1", "extra", "GObject-based multi-platform GUI toolkit", 5<<20, 20<<20), pkgdb.ReasonDep)
	gtk3.RequiredBy = []string{"firefox", "gvim"}
	yay := inst(pkgdb.Package{Name: "yay", Version: "12.0-1", Description: "Yet another yogurt, an AUR helper", InstalledSize: 9 << 20, Foreign: true}, pkgdb.ReasonExplicit)
	ffd := mk("firefox-developer-edition", "134.0b3-1", "extra", "Developer Edition of the Firefox web browser", 70<<20, 270<<20)
	fi := mk("firefox-i18n-da", "133.0-1", "extra", "Danish language pack for Firefox", 500<<10, 700<<10)
	return []pkgdb.Package{firefox, ffd, fi, gvim, vim, htop, glibc, cachy, mirror, libfoo, libbar, gtk3, yay}
}

// uiAUR answers the AUR from a table.
type uiAUR struct {
	mu   sync.Mutex
	hits []pkgdb.Package
	err  error
	info map[string]pkgdb.Package
	n    int
}

func (a *uiAUR) Search(ctx context.Context, term string) ([]pkgdb.Package, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return a.hits, a.err
}

func (a *uiAUR) Info(ctx context.Context, names []string) ([]pkgdb.Package, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return nil, a.err
	}
	var out []pkgdb.Package
	for _, n := range names {
		if p, ok := a.info[n]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func aurFixtures() (hits []pkgdb.Package, info map[string]pkgdb.Package) {
	ago := time.Now().AddDate(0, 0, -9)
	nightly := pkgdb.Package{Name: "firefox-nightly-bin", Version: "135.0a1-1", Repo: pkgdb.RepoAUR, Description: "Firefox nightly builds, binary",
		AUR: &pkgdb.AUR{PackageBase: "firefox-nightly-bin", Maintainer: "somebody", Votes: 123, LastModified: ago, OutOfDate: ago}}
	nightlyFull := nightly
	nightlyFull.Depends = []pkgdb.Dep{dep("gtk3", ""), dep("libxt", ""), dep("alsa-lib", "")}
	nightlyFull.AUR = &pkgdb.AUR{PackageBase: "firefox-nightly-bin", Maintainer: "somebody", Votes: 123, LastModified: ago, OutOfDate: ago,
		MakeDepends: []pkgdb.Dep{dep("unzip", "")}}
	dupe := pkgdb.Package{Name: "firefox", Version: "AUR-dup", Repo: pkgdb.RepoAUR, AUR: &pkgdb.AUR{PackageBase: "firefox"}}
	paru := pkgdb.Package{Name: "paru-bin", Version: "2.0.4-1", Repo: pkgdb.RepoAUR, Description: markupDesc,
		AUR: &pkgdb.AUR{PackageBase: "paru-bin", Maintainer: "", Votes: 40, LastModified: ago}}
	return []pkgdb.Package{dupe, nightly, paru}, map[string]pkgdb.Package{"firefox-nightly-bin": nightlyFull, "paru-bin": paru}
}

// uiBackend records what the page asks of the engine.
type uiBackend struct {
	mu      sync.Mutex
	started []steps.PkgRequest
	answers []string
	aborts  int
}

func (b *uiBackend) StartPackages(r steps.PkgRequest) {
	b.mu.Lock()
	b.started = append(b.started, r)
	b.mu.Unlock()
}
func (b *uiBackend) Answer(p int, yes bool) {
	b.mu.Lock()
	b.answers = append(b.answers, answerText(p, yes))
	b.mu.Unlock()
}
func (b *uiBackend) Abort()        { b.mu.Lock(); b.aborts++; b.mu.Unlock() }
func (b *uiBackend) UpdateSystem() {}

func answerText(p int, yes bool) string {
	s := "no"
	if yes {
		s = "yes"
	}
	return strings.Join([]string{"prompt", string(rune('0' + p)), s}, " ")
}

// pkgEnv is the scenario's fixture: made in the Options hook, read by the
// scenario.
type pkgEnv struct {
	cat     *uiCatalog
	aur     *uiAUR
	be      *uiBackend
	clock   *pkgview.FakeClock
	updates overview.Updates
	age     time.Duration
	mu      sync.Mutex
}

var errAURDown = errors.New("aur: dial tcp: no route to host")

func newPkgEnv() *pkgEnv {
	e := &pkgEnv{cat: &uiCatalog{pkgs: pkgFixtures()}, aur: &uiAUR{}, be: &uiBackend{}, clock: pkgview.NewFakeClock(time.Now()), age: 3 * time.Hour}
	e.aur.hits, e.aur.info = aurFixtures()
	return e
}

func (e *pkgEnv) wiring() *PackagesWiring {
	return &PackagesWiring{Catalog: e.cat, AUR: e.aur, Backend: e.be, Clock: e.clock,
		Updates: func() overview.Updates { e.mu.Lock(); defer e.mu.Unlock(); return e.updates },
		DBAge:   func() time.Duration { return e.age }}
}

func (e *pkgEnv) setUpdates(repo int) {
	e.mu.Lock()
	e.updates = overview.Updates{Known: true, Count: repo, Repo: repo, Checked: time.Now().Add(-time.Hour)}
	e.mu.Unlock()
}

func (e *pkgEnv) clearUpdates() {
	e.mu.Lock()
	e.updates = overview.Updates{Known: true, Checked: time.Now()}
	e.mu.Unlock()
}
