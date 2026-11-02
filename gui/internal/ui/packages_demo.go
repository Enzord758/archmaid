package ui

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// The demo data of the Packages page: --stub shows it, and the headless
// tests use it as their fixtures. A catalog standing in for *pkgdb.DB and
// an AUR client that answers from a table; neither reads pacman's databases
// or the network.

// markupDesc is a description that looks like markup: it must show as typed.
const markupDesc = "<b>Feature</b> rich AUR helper & more <i>[x]</i>"

type demoCatalog struct{ pkgs []pkgdb.Package }

func (c *demoCatalog) Search(q string, max int) []pkgdb.Package {
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

func (c *demoCatalog) Lookup(name string) (pkgdb.Package, bool) {
	for _, p := range c.pkgs {
		if p.Name == name {
			return p, true
		}
	}
	return pkgdb.Package{}, false
}

// Recent is the installed packages with a date, newest first.
func (c *demoCatalog) Recent(n int) []pkgdb.Package {
	var out []pkgdb.Package
	for _, p := range c.pkgs {
		if p.Installed && !p.InstallDate.IsZero() {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].InstallDate.After(out[j].InstallDate) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (c *demoCatalog) Installed(v pkgdb.View) []pkgdb.Package {
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

func demoPackages() []pkgdb.Package {
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
	now := time.Now()
	for i, p := range []*pkgdb.Package{&yay, &htop, &vim, &gtk3, &libbar, &libfoo, &glibc} {
		p.InstallDate = now.Add(-time.Duration(2+i*i*9) * time.Hour)
	}
	return []pkgdb.Package{firefox, ffd, fi, gvim, vim, htop, glibc, cachy, mirror, libfoo, libbar, gtk3, yay}
}

// demoAUR answers the AUR from a table.
type demoAUR struct {
	mu   sync.Mutex
	hits []pkgdb.Package
	err  error
	info map[string]pkgdb.Package
	n    int
	// byTerm: Search answers only the hits whose name or description has
	// the term (the demo); off, it answers its table whatever the term.
	byTerm bool
}

func (a *demoAUR) Search(ctx context.Context, term string) ([]pkgdb.Package, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	if !a.byTerm {
		return a.hits, a.err
	}
	var out []pkgdb.Package
	for _, h := range a.hits {
		if strings.Contains(strings.ToLower(h.Name+" "+h.Description), strings.ToLower(term)) {
			out = append(out, h)
		}
	}
	return out, a.err
}

func (a *demoAUR) Info(ctx context.Context, names []string) ([]pkgdb.Package, error) {
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

func demoAURPackages() (hits []pkgdb.Package, info map[string]pkgdb.Package) {
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

// demoWiring is the page --stub shows: the demo catalog, an AUR that answers
// from a table (with a package whose audit has a high finding, "risky-bin"),
// a backend that is the window's own Driver (the stub's Packages scripts).
func demoWiring() *PackagesWiring {
	hits, info := demoAURPackages()
	ago := time.Now().AddDate(0, 0, -2)
	risky := pkgdb.Package{Name: "risky-bin", Version: "2.0.4-1", Repo: pkgdb.RepoAUR, Description: "A package whose audit has a high finding (demo)",
		AUR: &pkgdb.AUR{PackageBase: "risky-bin", Maintainer: "newcomer", Votes: 1, LastModified: ago}}
	hits, info["risky-bin"] = append(hits, risky), risky
	return &PackagesWiring{
		Catalog: &demoCatalog{pkgs: demoPackages()},
		AUR:     &demoAUR{hits: hits, info: info, byTerm: true},
		DBAge:   func() time.Duration { return 3 * time.Hour },
	}
}
