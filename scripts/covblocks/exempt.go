package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// Exempt is one entry of docs/coverage-exempt.tsv: a block of statements no
// test or run can reach (category E of docs/coverage-map.md).
type Exempt struct {
	File  string // repo-relative, e.g. internal/audit/state.go
	Start int
	End   int
	Func  string
	Stmts int
	Rec   string // keep-exempt, remove, make-testable
	Sec   bool   // in privilege/helper code: Dev 3 reviews a removal
	Why   string
}

// ReadExempt parses the exempt list: tab-separated
// file, start-end, func, stmts, recommendation, security (yes/no), why.
// Blank lines and lines starting with # are skipped.
func ReadExempt(r io.Reader) ([]Exempt, error) {
	var out []Exempt
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("exempt list line %d: want 7 tab-separated fields, got %d", n, len(f))
		}
		var e Exempt
		e.File, e.Func, e.Rec, e.Why = f[0], f[2], f[4], f[6]
		if _, err := fmt.Sscanf(f[1], "%d-%d", &e.Start, &e.End); err != nil {
			return nil, fmt.Errorf("exempt list line %d: bad lines %q", n, f[1])
		}
		s, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("exempt list line %d: bad statement count %q", n, f[3])
		}
		e.Stmts = s
		switch f[5] {
		case "yes":
			e.Sec = true
		case "no":
		default:
			return nil, fmt.Errorf("exempt list line %d: security must be yes or no, got %q", n, f[5])
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Scope names the report scope of a repo-relative file, as cover-merge.sh
// does: helper is cmd/archmaid-helper/, gui is gui/, core is the rest.
func Scope(rel string) string {
	switch {
	case strings.HasPrefix(rel, "gui/"):
		return "gui"
	case strings.HasPrefix(rel, "cmd/archmaid-helper/"):
		return "helper"
	}
	return "core"
}

// ScopeStats is one scope's numbers: Total and Covered statements, and
// Exempt, the uncovered statements the exempt list takes out.
type ScopeStats struct {
	Total, Covered, Exempt int
}

// Reachable is covered / (total - exempt) in percent, one decimal truncated.
func (s ScopeStats) Reachable() string { return Pct(s.Total-s.Exempt, s.Covered) }

// Raw is covered / total.
func (s ScopeStats) Raw() string { return Pct(s.Total, s.Covered) }

// Report applies the exempt list to a merged profile. An entry counts only
// while it still names uncovered blocks of the same statement count; one
// that doesn't (the code moved, or a test now covers it) is returned as
// stale and takes nothing out of the denominator. Stats has core, helper,
// gui and all. rel maps a block's profile file name to its repo-relative
// path ("" for files outside the repo, which are skipped).
func Report(blocks []Block, ex []Exempt, rel func(string) string) (stats map[string]*ScopeStats, stale []Exempt) {
	stats = map[string]*ScopeStats{"core": {}, "helper": {}, "gui": {}, "all": {}}
	type key struct {
		file       string
		start, end int
	}
	unc := map[key][]int{} // indexes of uncovered blocks
	for i, b := range blocks {
		r := rel(b.File)
		if r == "" {
			continue
		}
		for _, s := range []string{Scope(r), "all"} {
			stats[s].Total += b.Stmts
			if b.Covered {
				stats[s].Covered += b.Stmts
			}
		}
		if !b.Covered && b.Stmts > 0 {
			k := key{r, b.StartLine, b.EndLine}
			unc[k] = append(unc[k], i)
		}
	}
	for _, e := range ex {
		if stats[Scope(e.File)].Total == 0 {
			continue // that scope isn't in this profile (check.sh measures core only)
		}
		idx := unc[key{e.File, e.Start, e.End}]
		n := 0
		for _, i := range idx {
			n += blocks[i].Stmts
		}
		if len(idx) == 0 || n != e.Stmts {
			stale = append(stale, e)
			continue
		}
		for _, s := range []string{Scope(e.File), "all"} {
			stats[s].Exempt += n
		}
	}
	return stats, stale
}

// WriteReachable prints the raw and reachable figures per scope.
func WriteReachable(w io.Writer, stats map[string]*ScopeStats) {
	for _, s := range []string{"core", "helper", "gui", "all"} {
		st := stats[s]
		fmt.Fprintf(w, "%-7s raw %s%% (%d/%d)   reachable %s%% (%d/%d, %d exempt)\n",
			s, st.Raw(), st.Covered, st.Total, st.Reachable(), st.Covered, st.Total-st.Exempt, st.Exempt)
	}
}

// relFunc maps profile file names to repo-relative paths through the modules.
func relFunc(root string, mods []Module) func(string) string {
	return func(file string) string {
		p := DiskPath(file, mods)
		if p == "" {
			return ""
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return ""
		}
		return filepath.ToSlash(r)
	}
}
