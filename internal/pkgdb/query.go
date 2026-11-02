package pkgdb

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxSearch is the most Search ever returns.
const MaxSearch = 100

// Rank classes, best first.
const (
	rankExact = iota
	rankPrefix
	rankContains
	rankProvides
	rankDesc
	rankNone
)

// Counts is the size of each installed view, for the tab badges.
type Counts struct{ Explicit, Dependencies, Orphans, Foreign int }

// Lookup returns the named package with full detail: the sync entry (first
// repo wins) with its installed state, or the installed-only entry (Foreign).
func (db *DB) Lookup(name string) (Package, bool) {
	s := db.cur.Load()
	if e, ok := s.byName[name]; ok {
		return e.pkg, true
	}
	if p, ok := s.local[name]; ok {
		return *p, true
	}
	return Package{}, false
}

// Search finds repo packages for q, case-insensitive, at most max (1..MaxSearch;
// anything else means MaxSearch). Every whitespace-separated term must match;
// a package ranks by its weakest term: exact name, name prefix, name contains,
// a provides name contains it, then the description contains it. Ties go to
// repo order, then name. A query under two runes finds nothing (the page shows
// the installed list then).
func (db *DB) Search(q string, max int) []Package {
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 || (len(terms) == 1 && utf8.RuneCountInString(terms[0]) < 2) {
		return nil
	}
	if max <= 0 || max > MaxSearch {
		max = MaxSearch
	}
	type hit struct {
		e    *entry
		rank int
	}
	var hits []hit
	for _, e := range db.cur.Load().sync {
		worst := rankExact
		for _, t := range terms {
			if r := e.rank(t); r > worst {
				worst = r
			}
		}
		if worst != rankNone {
			hits = append(hits, hit{e, worst})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.e.repoRank != b.e.repoRank:
			return a.e.repoRank < b.e.repoRank
		}
		return a.e.pkg.Name < b.e.pkg.Name
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]Package, len(hits))
	for i, h := range hits {
		out[i] = h.e.pkg
	}
	return out
}

func (e *entry) rank(t string) int {
	switch {
	case e.lname == t:
		return rankExact
	case strings.HasPrefix(e.lname, t):
		return rankPrefix
	case strings.Contains(e.lname, t):
		return rankContains
	}
	for _, p := range e.lprov {
		if strings.Contains(p, t) {
			return rankProvides
		}
	}
	if strings.Contains(e.ldesc, t) {
		return rankDesc
	}
	return rankNone
}

// Installed lists the installed packages of a view, sorted by name. Orphan
// follows pacman -Qdt (installed as a dependency, required by nothing and
// optional for nothing; "required" is provides-aware), Foreign pacman -Qm. A
// version constraint is not compared, which can only hide an orphan, never
// invent one. ViewDependencies leaves the orphans out.
func (db *DB) Installed(v View) []Package {
	var out []Package
	for _, p := range db.installedAll() {
		if inView(p, v) {
			out = append(out, p)
		}
	}
	return out
}

func inView(p Package, v View) bool {
	switch v {
	case ViewExplicit:
		return p.Reason == ReasonExplicit
	case ViewDependencies:
		return p.Reason == ReasonDep && !p.Orphan
	case ViewOrphans:
		return p.Orphan
	case ViewForeign:
		return p.Foreign
	}
	return false
}

// installedAll is every installed package, sorted by name, with the sync
// entry's detail where there is one.
func (db *DB) installedAll() []Package {
	s := db.cur.Load()
	out := make([]Package, 0, len(s.local))
	for name, l := range s.local {
		if e, ok := s.byName[name]; ok {
			out = append(out, e.pkg)
		} else {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Count is the size of each installed view.
func (db *DB) Count() Counts {
	var c Counts
	for _, p := range db.cur.Load().local {
		switch {
		case p.Reason == ReasonExplicit:
			c.Explicit++
		case p.Orphan:
			c.Orphans++
		default:
			c.Dependencies++
		}
		if p.Foreign {
			c.Foreign++
		}
	}
	return c
}

// Recent lists the n most recently installed packages, newest first (the
// empty state's "recently installed"); packages without an install date are
// left out.
func (db *DB) Recent(n int) []Package {
	var out []Package
	for _, p := range db.installedAll() {
		if !p.InstallDate.IsZero() {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].InstallDate.After(out[j].InstallDate) })
	if n >= 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
