// Package healthview is the GUI's side of the System health check (#270),
// without GTK: the types the sheet and the Overview cell are drawn from, how
// they are ordered and worded, and the bookkeeping of when a check may run.
//
// The checks themselves are internal/health (#269). The window talks to them
// through Source, so the GUI is built and tested with fakes, and the real
// functions are one small adapter (health.go in cmd/archmaid-gui) over this
// package's types.
package healthview

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// State is a check's result.
type State int

const (
	OK State = iota
	Look
	Unknown
)

// Check is one row of the report.
type Check struct {
	ID, Title string
	State     State
	// Detail is one plain-text line; Copy a command to paste ("" for no
	// button); Link an https archlinux.org page ("" for no button).
	Detail, Copy, Link string
	// Group orders rows within a state: "system", "packages", "hardware".
	Group string
}

// Report is what a run of the checks found.
type Report struct {
	Checks []Check
	At     time.Time
}

// Attention is the number of checks that say Look. Unknown is not counted.
func (r Report) Attention() int {
	n := 0
	for _, c := range r.Checks {
		if c.State == Look {
			n++
		}
	}
	return n
}

var groupOrder = map[string]int{"system": 0, "packages": 1, "hardware": 2}

func groupRank(g string) int {
	if r, ok := groupOrder[g]; ok {
		return r
	}
	return len(groupOrder)
}

// stateRank puts Look first, then Unknown, then OK.
func stateRank(s State) int {
	switch s {
	case Look:
		return 0
	case Unknown:
		return 1
	}
	return 2
}

// Sorted is the checks as the sheet lists them: Look rows, then Unknown,
// then OK; within a state in the Group order, else as given.
func (r Report) Sorted() []Check {
	out := append([]Check(nil), r.Checks...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		return groupRank(a.Group) < groupRank(b.Group)
	})
	return out
}

// Section is a run of the sheet's rows under one header.
type Section struct {
	Header string
	Checks []Check
}

// AttentionHeader is the first section's header.
const AttentionHeader = "Needs your attention"

var groupNames = map[string]string{"system": "System", "packages": "Packages", "hardware": "Hardware"}

// Sections is the sheet's layout: the Look rows first under "Needs your
// attention", then the rest (Unknown before OK) under a header per Group
// (System, Packages, Hardware, in that order), as given inside a group. A
// section with no rows is left out.
func (r Report) Sections() []Section {
	var out []Section
	var look []Check
	rest := map[string][]Check{}
	var groups []string
	for _, c := range r.Sorted() {
		if c.State == Look {
			look = append(look, c)
			continue
		}
		if _, seen := rest[c.Group]; !seen {
			groups = append(groups, c.Group)
		}
		rest[c.Group] = append(rest[c.Group], c)
	}
	if len(look) > 0 {
		out = append(out, Section{Header: AttentionHeader, Checks: look})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groupRank(groups[i]) < groupRank(groups[j]) })
	for _, g := range groups {
		name, ok := groupNames[g]
		if !ok {
			name = "Other"
		}
		out = append(out, Section{Header: name, Checks: rest[g]})
	}
	return out
}

// NewsItem is one Arch news post.
type NewsItem struct {
	Title, Link string
	Date        time.Time
}

// Unread is the news since the last full system update.
type Unread struct {
	Items     []NewsItem // newest first
	Since     time.Time
	FromCache bool
	CacheAge  time.Duration
}

// Source runs the checks and fetches the unread news. Both may take a
// while; the window calls them off the main thread.
type Source interface {
	Run(ctx context.Context) Report
	UnreadNews(ctx context.Context) (Unread, error)
}

// Cell is what the Overview's System health cell shows.
type Cell struct {
	// Emphasised: something needs attention (warn tint, edge, tag, line).
	Emphasised bool
	// Line is the cell's third line: "N things need your attention",
	// "Checking…" while the first run goes, or "".
	Line string
	// Tag is the corner tag ("look" when emphasised).
	Tag string
}

// CellFor is the cell for the latest report (nil: none yet) while a check
// is running or not.
func CellFor(r *Report, running bool) Cell {
	if r == nil {
		if running {
			return Cell{Line: "Checking…"}
		}
		return Cell{}
	}
	if n := r.Attention(); n > 0 {
		return Cell{Emphasised: true, Line: Attention(n), Tag: "look"}
	}
	return Cell{}
}

// Attention is "N things need your attention" ("1 thing needs...").
func Attention(n int) string {
	if n == 1 {
		return "1 thing needs your attention"
	}
	return fmt.Sprintf("%d things need your attention", n)
}

// Ago is a short "how long ago": "just now", "5 min ago", "3 h ago",
// "2 d ago".
func Ago(then, now time.Time) string {
	d := now.Sub(then)
	switch {
	case d < 90*time.Second:
		return "just now"
	case d < 90*time.Minute:
		return fmt.Sprintf("%d min ago", int((d+30*time.Second)/time.Minute))
	case d < 36*time.Hour:
		return fmt.Sprintf("%d h ago", int((d+30*time.Minute)/time.Hour))
	}
	return fmt.Sprintf("%d d ago", int(d/(24*time.Hour)))
}

// Subtitle is the sheet's line under its title.
func Subtitle(r *Report, now time.Time) string {
	s := "Read-only: nothing is changed and no password is asked."
	if r != nil && !r.At.IsZero() {
		s += " Checked " + Ago(r.At, now) + "."
	}
	return s
}

// SafeLink says whether link may be opened: https://archlinux.org/ only,
// whatever a source hands over.
func SafeLink(link string) bool {
	const pfx = "https://archlinux.org/"
	if len(link) <= len(pfx) || link[:len(pfx)] != pfx {
		return false
	}
	for _, r := range link {
		if r <= ' ' || r > '~' {
			return false
		}
	}
	return true
}

// Runner is the bookkeeping of when to run: one run at a time, and when the
// last one ended. Safe for use from several goroutines.
type Runner struct {
	mu      sync.Mutex
	running bool
	last    time.Time
}

// Begin claims the run: false when one is going already.
func (r *Runner) Begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

// End releases the run, which finished at now.
func (r *Runner) End(now time.Time) {
	r.mu.Lock()
	r.running, r.last = false, now
	r.mu.Unlock()
}

// Running says whether a run is going.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Stale says whether the last run is over age old at now (never run: yes).
func (r *Runner) Stale(now time.Time, age time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last.IsZero() || now.Sub(r.last) > age
}
