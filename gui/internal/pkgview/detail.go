package pkgview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// AURNotice is the tag on every AUR package's detail.
const AURNotice = "AUR · not reviewed by Arch"

// DepRow is one dependency of the detail pane.
type DepRow struct {
	Name string
	Raw  string // with the version constraint
	Desc string // optional dependencies only
	// Installed: an installed package meets it (by name or by what it
	// provides). PulledIn: none does, so installing this package brings it in
	// (the plan is pacman's and has the last word).
	Installed, PulledIn bool
	Build               bool // an AUR make/check dependency
}

// Satisfier is what a Catalog may add: whether an installed package meets a
// dependency by name or by what it provides (sonames, virtuals), at a
// satisfying version. The page's *pkgdb.DB has it; without it the detail
// falls back to the exact name.
type Satisfier interface{ Satisfied(raw string) bool }

var _ Satisfier = (*pkgdb.DB)(nil)

// Stat is one cell of the detail's stats.
type Stat struct{ Label, Value string }

// Detail is the detail pane's content.
type Detail struct {
	Valid   bool
	Pkg     pkgdb.Package
	Loading bool   // the AUR info request is out
	Err     string // the AUR info failed (plain, short)
	Stats   []Stat
	Deps    []DepRow
	Opt     []DepRow
	// AUR facts, set for an AUR package only.
	AUR     bool
	Notice  string // AURNotice
	AURRows []Stat // maintainer, votes, last modified
	Flagged string // "" or "Flagged out of date on 2026-10-01"
	Link    string // the AUR page
}

// Select shows p in the detail pane. A repo or installed package is looked
// up for its full entry at once; an AUR one shows what the search gave and
// asks the AUR for the rest (makedepends) once.
func (s *Search) Select(p pkgdb.Package) {
	if s.detStop != nil {
		s.detStop()
		s.detStop = nil
	}
	s.detGen++
	gen := s.detGen
	if p.Repo != pkgdb.RepoAUR && s.d.Catalog != nil {
		if full, ok := s.d.Catalog.Lookup(p.Name); ok {
			p = full
		}
	}
	s.detail = BuildDetail(p, s.d.Catalog, s.d.Clock.Now())
	if p.Repo == pkgdb.RepoAUR && s.d.AUR != nil {
		s.detail.Loading = true
		ctx, cancel := context.WithCancel(context.Background())
		s.detStop = cancel
		aur := s.d.AUR
		name := p.Name
		s.work(func() {
			got, err := aur.Info(ctx, []string{name})
			s.post(func() {
				if gen != s.detGen || ctx.Err() != nil {
					return
				}
				s.detStop = nil
				cancel()
				d := s.detail
				d.Loading = false
				switch {
				case err != nil:
					d.Err = plainNote(err.Error())
				case len(got) == 0:
					d.Err = "The AUR has no details for it"
				default:
					full := got[0]
					if s.d.Catalog != nil {
						if l, ok := s.d.Catalog.Lookup(name); ok {
							full = withInstalled(full, l)
						}
					}
					d = BuildDetail(full, s.d.Catalog, s.d.Clock.Now())
				}
				s.detail = d
				s.d.Changed()
			})
		})
	}
	s.d.Changed()
}

// ClearDetail empties the detail pane.
func (s *Search) ClearDetail() {
	if s.detStop != nil {
		s.detStop()
		s.detStop = nil
	}
	s.detGen++
	s.detail = Detail{}
	s.d.Changed()
}

// Detail is the detail pane's content.
func (s *Search) Detail() Detail { return s.detail }

// BuildDetail derives the pane's rows from p; cat tells which dependencies
// are installed.
func BuildDetail(p pkgdb.Package, cat Catalog, now time.Time) Detail {
	d := Detail{Valid: true, Pkg: p}
	d.Stats = []Stat{{"Version", versionText(p)}, {"Repository", repoText(p)}}
	if p.Repo != pkgdb.RepoAUR {
		if p.DownloadSize > 0 {
			d.Stats = append(d.Stats, Stat{"Download", Size(p.DownloadSize)})
		}
		if p.InstalledSize > 0 {
			d.Stats = append(d.Stats, Stat{"Installed size", Size(p.InstalledSize)})
		}
	}
	if len(p.Licenses) > 0 {
		d.Stats = append(d.Stats, Stat{"Licence", strings.Join(p.Licenses, ", ")})
	}
	if p.Installed {
		d.Stats = append(d.Stats, Stat{"Reason", reasonText(p.Reason)})
		req := "nothing"
		if n := len(p.RequiredBy); n > 0 {
			req = N(n, "package", "packages")
		}
		d.Stats = append(d.Stats, Stat{"Required by", req})
	}
	row := func(dep pkgdb.Dep, build bool) DepRow {
		r := DepRow{Name: dep.Name, Raw: dep.Raw, Desc: dep.Desc, Build: build}
		if r.Raw == "" {
			r.Raw = dep.Name
		}
		if cat != nil {
			if sat, ok := cat.(Satisfier); ok {
				r.Installed = sat.Satisfied(r.Raw)
			} else if l, ok := cat.Lookup(dep.Name); ok && l.Installed {
				r.Installed = true
			}
		}
		r.PulledIn = !r.Installed
		return r
	}
	for _, dep := range p.Depends {
		d.Deps = append(d.Deps, row(dep, false))
	}
	for _, dep := range p.OptDepends {
		d.Opt = append(d.Opt, row(dep, false))
	}
	if a := p.AUR; a != nil {
		d.AUR, d.Notice = true, AURNotice
		for _, dep := range a.MakeDepends {
			d.Deps = append(d.Deps, row(dep, true))
		}
		for _, dep := range a.CheckDepends {
			d.Deps = append(d.Deps, row(dep, true))
		}
		who := a.Maintainer
		if who == "" {
			who = "orphaned"
		}
		d.AURRows = []Stat{{"Maintainer", who}, {"Votes", fmt.Sprint(a.Votes)}}
		if !a.LastModified.IsZero() {
			d.AURRows = append(d.AURRows, Stat{"Last modified", a.LastModified.UTC().Format("2006-01-02")})
		}
		if !a.OutOfDate.IsZero() {
			d.Flagged = "Flagged out of date on " + a.OutOfDate.UTC().Format("2006-01-02")
		}
	}
	if p.Repo == pkgdb.RepoAUR {
		d.Link = "aur.archlinux.org/packages/" + p.Name
	}
	return d
}

func versionText(p pkgdb.Package) string {
	if p.Installed && p.InstalledVersion != "" && p.InstalledVersion != p.Version {
		return p.InstalledVersion + " → " + p.Version
	}
	return p.Version
}

func repoText(p pkgdb.Package) string {
	switch p.Repo {
	case "":
		return "none (foreign)"
	case pkgdb.RepoAUR:
		return "AUR"
	}
	return p.Repo
}

func reasonText(r pkgdb.Reason) string {
	switch r {
	case pkgdb.ReasonExplicit:
		return "explicitly installed"
	case pkgdb.ReasonDep:
		return "installed as a dependency"
	}
	return ""
}

// Tone is a tag's colour class suffix: only states carry colour.
type Tone string

const (
	ToneNeutral Tone = "neutral"
	ToneOK      Tone = "ok"
	ToneWarn    Tone = "warn"
)

// Tag is a small outlined label on a row.
type Tag struct {
	Text string
	Tone Tone
}

// RowTags are a result row's tags: the repo (neutral, whatever the repo),
// then "installed" and "update" for state.
func RowTags(p pkgdb.Package) []Tag {
	var tags []Tag
	switch p.Repo {
	case "":
		if p.Foreign {
			tags = append(tags, Tag{"foreign", ToneNeutral})
		}
	case pkgdb.RepoAUR:
		tags = append(tags, Tag{"AUR", ToneNeutral})
	default:
		tags = append(tags, Tag{p.Repo, ToneNeutral})
	}
	if p.Installed {
		tags = append(tags, Tag{"installed", ToneOK})
	}
	if p.Update {
		tags = append(tags, Tag{"update", ToneWarn})
	}
	return tags
}
