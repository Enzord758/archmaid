package logview

import (
	"strings"

	"github.com/Enzord758/archmaid/gui/internal/plural"
)

// RowKind is what a row of the Logs page's list is.
type RowKind int

const (
	// RowEntry is an entry's header: Text its label, Aside its kind and
	// time ("OK · 00:45:53").
	RowEntry RowKind = iota
	// RowSection is a titled section's header (a topgrade step): Text the
	// title, Aside its time, or its line count while collapsed. Clicking
	// it collapses or expands the section.
	RowSection
	// RowLine is a line of output.
	RowLine
)

// Row is one row of the Logs page's list. Entry and Section say which
// entry and section it belongs to (Section is -1 on an entry row).
type Row struct {
	Kind           RowKind
	Text, Aside    string
	Entry, Section int
}

// SectionKey names a section for Collapsed: entry and section index.
type SectionKey struct{ Entry, Section int }

// Rows lays entries out as the Logs page's list: each entry's header (if
// it has one), then its sections, a titled one under its own header row
// and without its lines while collapsed holds it.
func Rows(entries []Entry, collapsed map[SectionKey]bool) []Row {
	var out []Row
	for ei, e := range entries {
		if e.Kind != "" {
			aside := e.Kind
			if !e.Time.IsZero() {
				aside += " · " + e.Time.Format("15:04:05")
			}
			out = append(out, Row{Kind: RowEntry, Text: e.Label, Aside: aside, Entry: ei, Section: -1})
		}
		for si, s := range e.Sections {
			shut := s.Title != "" && collapsed[SectionKey{ei, si}]
			if s.Title != "" {
				aside := s.Time
				if shut {
					aside = plural.N(len(s.Lines), "line", "lines")
				}
				out = append(out, Row{Kind: RowSection, Text: strings.ToUpper(s.Title), Aside: aside, Entry: ei, Section: si})
			}
			if shut {
				continue
			}
			for _, l := range s.Lines {
				out = append(out, Row{Kind: RowLine, Text: l, Entry: ei, Section: si})
			}
		}
	}
	return out
}

// Dir is the run's directory on disk ("" for a Run not from Runs), for
// "Open folder".
func (r Run) Dir() string {
	if r.store == nil {
		return ""
	}
	return r.store.RunDir()
}
