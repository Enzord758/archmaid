package aurbuild

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Limits of one AUR run.
const (
	MaxTargets  = 32 // the helper's name cap
	MaxAUR      = 24 // AUR packages in the closure, targets and dependencies
	MaxRepoDeps = 200
)

// Reason says why Resolve refused. The strings are stable (tests, logs).
type Reason string

const (
	ReasonBadRequest     Reason = "bad-request"      // no name, too many, a bad or duplicate name
	ReasonNotFound       Reason = "not-found"        // a target is not in the AUR
	ReasonInRepo         Reason = "in-repo"          // a target is in a sync repo: install it from there
	ReasonUnmet          Reason = "unmet-dependency" // no installed, repo or AUR package by that name
	ReasonAURMakedepends Reason = "aur-makedepends"  // a build dependency only the AUR has
	ReasonVersion        Reason = "dependency-version"
	ReasonCycle          Reason = "cycle"
	ReasonTooLarge       Reason = "too-large"
	ReasonSrcinfo        Reason = "srcinfo-differs" // the fetched .SRCINFO is not what the index said
)

// Refusal is Resolve's (and Verify's) refusal: nothing was fetched or built.
// Detail is one sanitized line for the sheet.
type Refusal struct {
	Reason  Reason
	Package string // the package the reason is about
	Dep     string // the dependency, where there is one
	Detail  string
}

func (r *Refusal) Error() string {
	return "AUR install refused (" + string(r.Reason) + "): " + r.Detail
}

func refuse(reason Reason, pkg, dep, format string, a ...any) *Refusal {
	return &Refusal{Reason: reason, Package: sanitize.OneLine(pkg), Dep: sanitize.OneLine(dep),
		Detail: sanitize.Clip(sanitize.OneLine(fmt.Sprintf(format, a...)), 240)}
}

// Repo is what Resolve reads of the installed and sync packages;
// *pkgdb.DB is one.
type Repo interface {
	// Lookup is the exact name in a sync DB or, failing that, installed
	// (Repo == "" then).
	Lookup(name string) (pkgdb.Package, bool)
	// Installed lists the installed packages of a view.
	Installed(v pkgdb.View) []pkgdb.Package
}

// Skip is a dependency left out because an installed package meets it.
type Skip struct {
	For string // the AUR package that needs it
	Dep string // as written ("glibc>=2.38")
	By  string // the installed package that provides it
}

// Base is one package base to build, with the pkgnames to install from it.
type Base struct {
	Name    string
	Version string   // the AUR index's version at resolve time
	Names   []string // pkgnames to install (requested and needed), sorted
	// Targets is the subset of Names the user asked for.
	Targets []string
	// After are the AUR bases that must be built before this one (runtime
	// dependencies), sorted.
	After []string
}

// Resolution is Resolve's answer.
type Resolution struct {
	// Targets are the requested names, in request order.
	Targets []string
	// Bases is the build order: a dependency's base before its dependants'.
	Bases []Base
	// RepoDeps are the sync packages to install first with --asdeps
	// (dependencies, build and check dependencies not already installed),
	// sorted.
	RepoDeps []string
	Skipped  []Skip

	// seen maps pkgname -> the dependency names the AUR index listed for it
	// (runtime, build, check): Verify compares the fetched .SRCINFO with it.
	seen map[string]map[string]bool
	// wanted: the constraints put on AUR packages not yet fetched.
	wanted map[string][]wantDep
}

// Resolve works out what an install of the AUR packages names needs
// (docs/design/packages-contract.md, section 5). Only runtime dependencies
// may come from the AUR; a build or check dependency that only the AUR has is
// refused in v1. A dependency met by an installed package (by name or by a
// provides entry with a satisfying version) is skipped; one that is not is
// a sync package by exact name (installed first, --asdeps) or an AUR package
// by exact name; anything else, a virtual dependency included, is refused.
func Resolve(ctx context.Context, names []string, repo Repo, aur aursearch.Searcher) (Resolution, error) {
	if len(names) == 0 || len(names) > MaxTargets {
		return Resolution{}, refuse(ReasonBadRequest, "", "", "ask for 1 to %d packages", MaxTargets)
	}
	isTarget := map[string]bool{}
	for _, n := range names {
		if !pkgdb.ValidName(n) {
			return Resolution{}, refuse(ReasonBadRequest, n, "", "%q is not a package name", n)
		}
		if isTarget[n] {
			return Resolution{}, refuse(ReasonBadRequest, n, "", "%s is listed twice", n)
		}
		isTarget[n] = true
		if sp, ok := repo.Lookup(n); ok && sp.Repo != "" && sp.Repo != pkgdb.RepoAUR {
			return Resolution{}, refuse(ReasonInRepo, n, "", "%s is in the %s repository: install it from there", n, sp.Repo)
		}
	}
	res := Resolution{Targets: append([]string(nil), names...), seen: map[string]map[string]bool{}, wanted: map[string][]wantDep{}}
	inst := newInstalledIndex(repo)

	known := map[string]pkgdb.Package{} // AUR packages found, by pkgname
	repoDeps := map[string]bool{}
	level := append([]string(nil), names...)
	for len(level) > 0 {
		found, err := aur.Info(ctx, level)
		if err != nil {
			return Resolution{}, fmt.Errorf("asking the AUR: %w", err)
		}
		byName := map[string]pkgdb.Package{}
		for _, p := range found {
			byName[p.Name] = p
		}
		var next []string
		for _, n := range level {
			p, ok := byName[n]
			if !ok || p.AUR == nil || p.AUR.PackageBase == "" || !pkgdb.ValidName(p.AUR.PackageBase) {
				if isTarget[n] {
					return Resolution{}, refuse(ReasonNotFound, n, "", "%s is not in the AUR", n)
				}
				return Resolution{}, refuse(ReasonUnmet, "", n, "no installed, repository or AUR package is named %s (virtual dependencies are not supported)", n)
			}
			for _, w := range res.wanted[n] {
				if !satisfies(w.raw, n, p.Version) {
					return Resolution{}, refuse(ReasonVersion, w.pkg, w.raw, "%s needs %s, the AUR has %s %s", w.pkg, w.raw, n, p.Version)
				}
			}
			known[n] = p
		}
		if len(known) > MaxAUR {
			return Resolution{}, refuse(ReasonTooLarge, "", "", "the install needs more than %d AUR packages", MaxAUR)
		}
		for _, n := range level {
			p := known[n]
			deps := map[string]bool{}
			res.seen[n] = deps
			for _, group := range []struct {
				list  []pkgdb.Dep
				build bool
			}{{p.Depends, false}, {p.AUR.MakeDepends, true}, {p.AUR.CheckDepends, true}} {
				for _, d := range group.list {
					deps[d.Name] = true
					if err := res.classify(n, d, group.build, repo, inst, repoDeps, known, &next); err != nil {
						return Resolution{}, err
					}
				}
			}
		}
		level = dedupe(next)
		if len(repoDeps) > MaxRepoDeps {
			return Resolution{}, refuse(ReasonTooLarge, "", "", "the install needs more than %d repository packages", MaxRepoDeps)
		}
	}
	for n := range repoDeps {
		res.RepoDeps = append(res.RepoDeps, n)
	}
	sort.Strings(res.RepoDeps)
	if err := res.order(known, isTarget); err != nil {
		return Resolution{}, err
	}
	return res, nil
}

// classify decides what a dependency d of AUR package pkg needs.
func (res *Resolution) classify(pkg string, d pkgdb.Dep, build bool, repo Repo, inst *installedIndex,
	repoDeps map[string]bool, known map[string]pkgdb.Package, next *[]string) error {
	if by := inst.provider(d.Raw); by != "" {
		res.Skipped = append(res.Skipped, Skip{For: pkg, Dep: d.Raw, By: by})
		return nil
	}
	if sp, ok := repo.Lookup(d.Name); ok && sp.Repo != "" && sp.Repo != pkgdb.RepoAUR {
		if !satisfies(d.Raw, sp.Name, sp.Version) {
			return refuse(ReasonVersion, pkg, d.Raw, "%s needs %s, the repositories have %s %s", pkg, d.Raw, sp.Name, sp.Version)
		}
		repoDeps[sp.Name] = true
		return nil
	}
	// not installed, not in a repo: the AUR, by exact name
	if build {
		return refuse(ReasonAURMakedepends, pkg, d.Raw,
			"%s needs %s to build, and only the AUR has it; install %s first, then %s (AUR build dependencies are not supported yet)", pkg, d.Raw, d.Name, pkg)
	}
	if p, ok := known[d.Name]; ok {
		if !satisfies(d.Raw, p.Name, p.Version) {
			return refuse(ReasonVersion, pkg, d.Raw, "%s needs %s, the AUR has %s %s", pkg, d.Raw, p.Name, p.Version)
		}
		return nil
	}
	res.wanted[d.Name] = append(res.wanted[d.Name], wantDep{pkg, d.Raw})
	*next = append(*next, d.Name)
	return nil
}

// order groups the AUR packages by base and sorts the bases so that a base
// comes after the bases of its runtime dependencies. A cycle is refused.
func (res *Resolution) order(known map[string]pkgdb.Package, isTarget map[string]bool) error {
	bases := map[string]*Base{}
	owner := map[string]string{} // pkgname -> base
	for n, p := range known {
		b := p.AUR.PackageBase
		owner[n] = b
		if bases[b] == nil {
			bases[b] = &Base{Name: b, Version: p.Version}
		}
		bases[b].Names = append(bases[b].Names, n)
		if isTarget[n] {
			bases[b].Targets = append(bases[b].Targets, n)
		}
	}
	after := map[string]map[string]bool{}
	for n, p := range known {
		b := owner[n]
		for _, d := range p.Depends {
			if o, ok := owner[d.Name]; ok && o != b {
				if after[b] == nil {
					after[b] = map[string]bool{}
				}
				after[b][o] = true
			}
		}
	}
	names := make([]string, 0, len(bases))
	for b := range bases {
		names = append(names, b)
		sort.Strings(bases[b].Names)
		sort.Strings(bases[b].Targets)
		for a := range after[b] {
			bases[b].After = append(bases[b].After, a)
		}
		sort.Strings(bases[b].After)
	}
	sort.Strings(names)
	done := map[string]bool{}
	for len(done) < len(names) {
		progressed := false
		for _, b := range names {
			if done[b] {
				continue
			}
			ready := true
			for _, a := range bases[b].After {
				if !done[a] {
					ready = false
					break
				}
			}
			if ready {
				done[b] = true
				res.Bases = append(res.Bases, *bases[b])
				progressed = true
			}
		}
		if !progressed {
			var stuck []string
			for _, b := range names {
				if !done[b] {
					stuck = append(stuck, b)
				}
			}
			return refuse(ReasonCycle, stuck[0], "", "these AUR packages depend on each other in a circle: %s", strings.Join(stuck, ", "))
		}
	}
	return nil
}

// Verify checks a fetched .SRCINFO against what Resolve was told by the AUR
// index: the base and every pkgname to install must be there, and every
// dependency (runtime, build, check) it lists must be one the index listed.
// A .SRCINFO with more is a refusal (nothing is built): the resolution, and
// the repo packages installed for it, would not cover it.
func (res Resolution) Verify(b Base, si Srcinfo) error {
	if si.Base != b.Name {
		return refuse(ReasonSrcinfo, b.Name, "", ".SRCINFO is for %s, not %s", si.Base, b.Name)
	}
	for _, n := range b.Names {
		if !si.HasPackage(n) {
			return refuse(ReasonSrcinfo, n, "", "%s is not a package of %s in its .SRCINFO", n, b.Name)
		}
		seen := res.seen[n]
		check := append(append(append([]string(nil), si.EffectiveDepends(n)...), si.Makedepends...), si.Checkdepends...)
		for _, raw := range check {
			if name := DepName(raw); !seen[name] {
				return refuse(ReasonSrcinfo, n, raw, "the fetched .SRCINFO of %s lists %s, which the AUR index did not; nothing was built", n, raw)
			}
		}
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// installedIndex answers "does an installed package meet this dependency".
type installedIndex struct {
	byName map[string]string // name -> version
	prov   map[string][]prov // provided name -> providers
}

type prov struct{ pkg, raw string }

func newInstalledIndex(repo Repo) *installedIndex {
	ix := &installedIndex{byName: map[string]string{}, prov: map[string][]prov{}}
	for _, v := range []pkgdb.View{pkgdb.ViewExplicit, pkgdb.ViewDependencies, pkgdb.ViewOrphans, pkgdb.ViewForeign} {
		for _, p := range repo.Installed(v) {
			if _, dup := ix.byName[p.Name]; dup {
				continue
			}
			ix.byName[p.Name] = p.InstalledVersion
			if p.InstalledVersion == "" {
				ix.byName[p.Name] = p.Version
			}
			for _, pr := range p.Provides {
				ix.prov[pr.Name] = append(ix.prov[pr.Name], prov{p.Name, pr.Raw})
			}
		}
	}
	return ix
}

// provider returns the installed package that meets raw ("foo>=1.2"), or "".
func (ix *installedIndex) provider(raw string) string {
	name := DepName(raw)
	if v, ok := ix.byName[name]; ok && satisfies(raw, name, v) {
		return name
	}
	for _, p := range ix.prov[name] {
		// a provides entry "foo=1.0" carries a version; a bare "foo" does not
		_, pv, _ := strings.Cut(p.raw, "=")
		if satisfies(raw, name, pv) {
			return p.pkg
		}
	}
	return ""
}

// satisfies reports whether version ver of name meets the constraint in raw
// ("foo", "foo>=1.2", "foo=1.0-2"). A bare name always does; a versioned
// constraint is not met by an unknown ("") version, as in pacman.
func satisfies(raw, name, ver string) bool {
	rest := strings.TrimPrefix(raw, name)
	if rest == "" {
		return true
	}
	if ver == "" {
		return false
	}
	var op string
	for _, o := range []string{">=", "<=", "=", ">", "<"} {
		if strings.HasPrefix(rest, o) {
			op = o
			break
		}
	}
	if op == "" {
		return false
	}
	want := rest[len(op):]
	if !strings.Contains(want, "-") {
		if i := strings.LastIndex(ver, "-"); i >= 0 {
			ver = ver[:i] // pacman compares the release only when the constraint names one
		}
	}
	c := pkgdb.Vercmp(ver, want)
	switch op {
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case "=":
		return c == 0
	case ">":
		return c > 0
	}
	return c < 0
}

// IsRefusal reports whether err is a *Refusal and returns it.
func IsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	ok := errors.As(err, &r)
	return r, ok
}

type wantDep struct{ pkg, raw string }
