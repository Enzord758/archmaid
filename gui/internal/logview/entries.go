package logview

import (
	"regexp"
	"strings"
	"time"
)

// Entry is one log entry as the Logs page shows it (P8, A7): logstore's
// header line "[2026-10-05 00:45:53] OK: Topgrade completed" and the output
// it carries, in sections.
type Entry struct {
	// Time is the header's timestamp (local time); zero for lines before
	// the first header (a log from an older version, or a torn one).
	Time time.Time
	// Kind is the header's kind: OK, FAIL, SKIP, DRYRUN, ACCEPT, WARN,
	// ABORT, AUDIT… ("" for lines before the first header). Label is the
	// rest of the header ("Topgrade completed").
	Kind, Label string
	// Sections are the entry's output: split at topgrade's step separators
	// ("―― 00:45:36 - System update ――"), so a topgrade entry reads as
	// one section per step. Output before the first separator, and any
	// output that has none, is an untitled section first.
	Sections []Section
}

// Section is part of an entry's output.
type Section struct {
	// Title is the separator's name ("System update"), "" for the untitled
	// first section; Time its clock time ("00:45:36"), "" without one.
	Title, Time string
	Lines       []string
}

// Lines counts the entry's output lines.
func (e Entry) Lines() int {
	n := 0
	for _, s := range e.Sections {
		n += len(s.Lines)
	}
	return n
}

var (
	// header is logstore.Append's entry line.
	header = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\] ([A-Z]+): (.*)$`)
	// separator is topgrade's step separator: U+2015 bars around an
	// optional "HH:MM:SS - " and the step's name.
	separator = regexp.MustCompile(`^―+ (?:(\d{2}:\d{2}:\d{2}) - )?(.+?) ―+$`)
)

// output's frame lines, written by logstore.Append around an entry's body.
const (
	outputOpen  = "--- "
	outputOpenZ = " output ---"
	outputEnd   = "--- end output ---"
)

// Parse splits a log (Run.Read's lines) into its entries, in order. It
// never drops a line: anything that isn't a header or an output frame is
// output of the entry it follows.
func Parse(lines []string) []Entry {
	var out []Entry
	cur := -1
	add := func(l string) {
		if cur < 0 {
			out = append(out, Entry{})
			cur = 0
		}
		e := &out[cur]
		if m := separator.FindStringSubmatch(l); m != nil {
			e.Sections = append(e.Sections, Section{Title: m[2], Time: m[1]})
			return
		}
		if len(e.Sections) == 0 {
			e.Sections = append(e.Sections, Section{})
		}
		s := &e.Sections[len(e.Sections)-1]
		s.Lines = append(s.Lines, l)
	}
	for _, l := range lines {
		if m := header.FindStringSubmatch(l); m != nil {
			t, _ := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local)
			out = append(out, Entry{Time: t, Kind: m[2], Label: m[3]})
			cur = len(out) - 1
			continue
		}
		if cur >= 0 && isFrame(l, out[cur].Label) {
			continue
		}
		add(l)
	}
	return out
}

// isFrame: l is logstore's "--- label output ---" or "--- end output ---"
// line around label's body.
func isFrame(l, label string) bool {
	return l == outputEnd || l == outputOpen+label+outputOpenZ
}

// Filter keeps, of entries, the lines that contain query (case-insensitive),
// and the sections and entries that still hold one; an entry whose header
// matches is kept whole. An empty query keeps everything.
func Filter(entries []Entry, query string) []Entry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries
	}
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), q) }
	var out []Entry
	for _, e := range entries {
		if has(e.Kind + ": " + e.Label) {
			out = append(out, e)
			continue
		}
		f := Entry{Time: e.Time, Kind: e.Kind, Label: e.Label}
		for _, s := range e.Sections {
			if has(s.Title) {
				f.Sections = append(f.Sections, s)
				continue
			}
			var keep []string
			for _, l := range s.Lines {
				if has(l) {
					keep = append(keep, l)
				}
			}
			if keep != nil {
				f.Sections = append(f.Sections, Section{Title: s.Title, Time: s.Time, Lines: keep})
			}
		}
		if f.Sections != nil {
			out = append(out, f)
		}
	}
	return out
}
