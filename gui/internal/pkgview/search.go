package pkgview

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// The search's rules (contract section 2).
const (
	RepoDebounce = 120 * time.Millisecond
	AURDebounce  = 500 * time.Millisecond
	MinRunes     = 2  // a shorter term shows the empty state, searches nothing
	RepoMax      = 50 // repo hits asked for and shown
	AURMax       = 40 // AUR hits shown
)

// Catalog is the part of *pkgdb.DB the page reads. All of it is in memory
// and quick; Search may take a few milliseconds on a cold cache, which is
// why it runs on a worker (Deps.Go).
type Catalog interface {
	Search(q string, max int) []pkgdb.Package
	Lookup(name string) (pkgdb.Package, bool)
	Installed(v pkgdb.View) []pkgdb.Package
}

// Deps are what a Search works with. Go runs a function off the main
// thread (a test: inline), Post runs one on it (a test: inline), Changed
// is called on the main thread after Results or Detail changed.
type Deps struct {
	Catalog Catalog
	AUR     aursearch.Searcher // nil: the AUR is not searched
	Clock   Clock
	Go      func(func())
	Post    func(func())
	Changed func()
}

func (d *Deps) fill() {
	if d.Clock == nil {
		d.Clock = RealClock{}
	}
	if d.Go == nil {
		d.Go = func(f func()) { go f() }
	}
	if d.Post == nil {
		d.Post = func(f func()) { f() }
	}
	if d.Changed == nil {
		d.Changed = func() {}
	}
}

// AURState is the AUR section's state.
type AURState int

const (
	AURIdle    AURState = iota // no term, or the term is too short or too long
	AURWaiting                 // typing: the debounce is running
	AURLoading                 // the request is out
	AURReady
	AURFailed // "AUR unreachable": the repo results are unaffected
	AURDisabled
)

// Results is what the list shows for the current term.
type Results struct {
	Query string
	// Active: the term is long enough to search; false shows the empty state.
	Active bool
	// Searching: the repo search is pending or running.
	Searching bool
	Repo      []pkgdb.Package
	AUR       []pkgdb.Package
	AURState  AURState
	// AURNote is the AUR failure in a few plain words ("" otherwise).
	AURNote string
}

// Search is the page's search state: the term, the debounce, the two result
// lists and the detail pane's package.
type Search struct {
	d        Deps
	query    string
	repoGen  int
	aurGen   int
	repoT    Stopper
	aurT     Stopper
	aurStop  context.CancelFunc
	aurCtx   context.Context // the request in flight (tests)
	res      Results
	aurRaw   []pkgdb.Package
	detail   Detail
	detGen   int
	detStop  context.CancelFunc
	repoRuns int // searches started, for tests
	inflight atomic.Int32
	aurRuns  int
}

// NewSearch makes a Search.
func NewSearch(d Deps) *Search {
	d.fill()
	s := &Search{d: d}
	if d.AUR == nil {
		s.res.AURState = AURDisabled
	}
	return s
}

// work runs f off the main thread (Deps.Go); its result is delivered by
// post, which pairs with it. Busy counts the pairs not yet delivered.
func (s *Search) work(f func()) {
	s.inflight.Add(1)
	s.d.Go(f)
}

// post delivers a worker's result on the main thread.
func (s *Search) post(f func()) {
	s.d.Post(func() {
		defer s.inflight.Add(-1)
		f()
	})
}

// posted runs f on the main thread, counted by Busy (a debounce timer
// that fired).
func (s *Search) posted(f func()) {
	s.inflight.Add(1)
	s.post(f)
}

// Busy reports whether a search, an AUR request or a timer's follow-up is
// still on its way to the main thread (the headless harness waits on it).
func (s *Search) Busy() bool { return s.inflight.Load() > 0 }

// RepoRuns and AURRuns are the number of searches started (tests).
func (s *Search) RepoRuns() int { return s.repoRuns }

// AURRuns: see RepoRuns.
func (s *Search) AURRuns() int { return s.aurRuns }

// Results is the current results (a copy of the header; the slices are not
// to be changed).
func (s *Search) Results() Results { return s.res }

// SetQuery is the entry's text changed. The repo search runs after
// RepoDebounce, the AUR search after AURDebounce when the term has at
// least MinRunes runes; a newer term supersedes both, and the AUR request
// in flight is cancelled.
func (s *Search) SetQuery(q string) {
	q = cleanTerm(q)
	if q == s.query {
		return
	}
	s.query = q
	s.stopRepo()
	s.stopAUR()
	s.aurRaw = nil
	s.res = Results{Query: q, AURState: s.res.AURState}
	if s.d.AUR == nil {
		s.res.AURState = AURDisabled
	} else {
		s.res.AURState = AURIdle
	}
	if utf8.RuneCountInString(q) < MinRunes {
		s.d.Changed()
		return
	}
	s.res.Active, s.res.Searching = true, true
	repoGen := s.repoGen
	s.repoT = s.d.Clock.AfterFunc(RepoDebounce, func() { s.posted(func() { s.runRepo(repoGen, q) }) })
	if s.d.AUR != nil && len(q) <= aursearch.MaxTerm {
		s.res.AURState = AURWaiting
		aurGen := s.aurGen
		s.aurT = s.d.Clock.AfterFunc(AURDebounce, func() { s.posted(func() { s.runAUR(aurGen, q) }) })
	}
	s.d.Changed()
}

// Retry searches the AUR again for the current term at once (the
// "AUR unreachable" row's button). It does nothing unless the last
// request failed.
func (s *Search) Retry() {
	if s.res.AURState != AURFailed || s.d.AUR == nil || s.query == "" {
		return
	}
	s.stopAUR()
	s.runAUR(s.aurGen, s.query)
}

// cleanTerm trims the term and drops control characters.
func cleanTerm(q string) string {
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	return strings.TrimSpace(q)
}

func (s *Search) stopRepo() {
	s.repoGen++
	if s.repoT != nil {
		s.repoT.Stop()
		s.repoT = nil
	}
}

func (s *Search) stopAUR() {
	s.aurGen++
	if s.aurT != nil {
		s.aurT.Stop()
		s.aurT = nil
	}
	if s.aurStop != nil {
		s.aurStop()
		s.aurStop = nil
	}
}

func (s *Search) runRepo(gen int, q string) {
	if gen != s.repoGen {
		return
	}
	s.repoRuns++
	cat := s.d.Catalog
	s.work(func() {
		var hits []pkgdb.Package
		if cat != nil {
			hits = cat.Search(q, RepoMax)
		}
		s.post(func() {
			if gen != s.repoGen {
				return
			}
			if len(hits) > RepoMax {
				hits = hits[:RepoMax]
			}
			s.res.Repo, s.res.Searching = hits, false
			s.merge()
			s.d.Changed()
		})
	})
}

func (s *Search) runAUR(gen int, q string) {
	if gen != s.aurGen {
		return
	}
	s.aurRuns++
	ctx, cancel := context.WithCancel(context.Background())
	s.aurStop, s.aurCtx = cancel, ctx
	s.res.AURState = AURLoading
	s.d.Changed()
	aur := s.d.AUR
	s.work(func() {
		hits, err := aur.Search(ctx, q)
		s.post(func() {
			if gen != s.aurGen || ctx.Err() != nil {
				return
			}
			s.aurStop = nil
			cancel()
			if err != nil {
				s.aurRaw = nil
				s.res.AURState, s.res.AURNote = AURFailed, plainNote(err.Error())
			} else {
				s.aurRaw = hits
				s.res.AURState, s.res.AURNote = AURReady, ""
			}
			s.merge()
			s.d.Changed()
		})
	})
}

// plainNote is an error text cut to one short plain line.
func plainNote(s string) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

// merge rebuilds the AUR list from the raw hits and the repo list: a hit
// whose name is in a sync repo is dropped (the repo wins); one that is
// installed shows the installed state; at most AURMax stay.
func (s *Search) merge() {
	s.res.AUR = MergeAUR(s.aurRaw, s.res.Repo, s.d.Catalog)
}

// MergeAUR is the AUR section for the raw hits: repo names dropped (both
// those shown and any the whole sync DBs hold, via cat), installed state
// merged in, capped at AURMax, the order kept.
func MergeAUR(raw, repo []pkgdb.Package, cat Catalog) []pkgdb.Package {
	inRepo := map[string]bool{}
	for _, p := range repo {
		inRepo[p.Name] = true
	}
	var out []pkgdb.Package
	for _, h := range raw {
		if inRepo[h.Name] {
			continue
		}
		if cat != nil {
			if p, ok := cat.Lookup(h.Name); ok {
				if p.Repo != "" && p.Repo != pkgdb.RepoAUR {
					continue
				}
				h = withInstalled(h, p)
			}
		}
		out = append(out, h)
		if len(out) == AURMax {
			break
		}
	}
	return out
}

// withInstalled copies the installed state of l (the local entry) onto the
// AUR entry h, and sets Update when the AUR version is newer.
func withInstalled(h, l pkgdb.Package) pkgdb.Package {
	if !l.Installed {
		return h
	}
	h.Installed, h.InstalledVersion, h.Reason = true, l.InstalledVersion, l.Reason
	if h.InstalledVersion == "" {
		h.InstalledVersion = l.Version
	}
	h.RequiredBy, h.OptionalFor, h.Orphan, h.Foreign = l.RequiredBy, l.OptionalFor, l.Orphan, l.Foreign
	h.Update = VerCmp(h.InstalledVersion, h.Version) < 0
	return h
}

// AllHits are the shown results in order: repo first, then AUR.
func (r Results) AllHits() []pkgdb.Package {
	out := make([]pkgdb.Package, 0, len(r.Repo)+len(r.AUR))
	out = append(out, r.Repo...)
	return append(out, r.AUR...)
}
