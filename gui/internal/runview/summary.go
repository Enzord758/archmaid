package runview

import (
	"fmt"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The Run page's finished state, as the owner's variant C of #187 has it
// (the TUI draws the same report in internal/tui/summary.go): a hero with
// one headline and the things that need the user, then the packages and the
// space freed, then the steps and output folded below. This file turns a
// report.Report into the words and numbers of that page; the window
// (ui/summary.go) only draws them. Nothing in it runs a command: the one
// action, copying a command to the clipboard, is the window's.

// CacheMenuLabel is the menu's name for the action that empties the package
// cache; the "still holds" line points at it. It must read as the menu
// entry does (internal/actions, IDClearCaches); a test holds them together.
const CacheMenuLabel = "Clear caches"

// PacdiffCommand is what "Copy pacdiff command" puts on the clipboard: the
// merge tool from pacman-contrib, as root because /etc is. archmaid never
// runs it (the docs don't prefer another spelling).
const PacdiffCommand = "sudo pacdiff"

// Summary is what the finished Run page says about the run's report.
type Summary struct {
	// Kicker is "NEEDS YOUR ATTENTION", "ALL QUIET" or, for a run that
	// didn't end cleanly with nothing else to say, "CHECK THE STEPS".
	// Tone is "warn" for the first and third, "ok" for the second.
	Kicker, Tone string
	// Headline is the first attention item's short text, or "Everything is
	// up to date" (or "The run did not finish cleanly").
	Headline string
	// Sub is "47 packages upgraded · 3.1 GiB freed · 14 m 52 s".
	Sub string
	// Attention are the rows under the headline.
	Attention []AttentionRow
	// Packages and Freed are the two columns; nil when the report holds
	// nothing for them.
	Packages *PackagesBlock
	Freed    *FreedBlock
	// Cache is the "package cache still holds" line, "" for none.
	Cache string
	// Audit is the AUR audit line, "" unless it had findings.
	Audit string
	// Notes say what could not be determined.
	Notes []string
}

// AttentionRow is one thing that needs the user.
type AttentionRow struct {
	Kind  string // report.Kind*
	Title string // the short text, bold
	Text  string // the full line, without the title it starts with
	// Command is what the row's one button copies to the clipboard; ""
	// for a row without a button.
	Command string
	// Button is that button's label.
	Button string
}

// PackagesBlock is the Packages column.
type PackagesBlock struct {
	// Head is "47 upgraded (6 AUR)".
	Head string
	// Top is the first names, "linux-cachyos, systemd, nvidia-utils +44
	// more"; "" when nothing was upgraded. Rows are every upgrade, the
	// first MaxPackageRows, for the expander.
	Top  string
	Rows []PackageRow
	// Hidden counts the upgrades Rows leaves out ("… +41 more in the log").
	Hidden int
	// Lines are the Installed, Removed (orphans, without the log) and
	// Downgraded lines, only those that are not zero.
	Lines []LabelledLine
}

// PackageRow is one upgrade: the name and "old → new".
type PackageRow struct{ Name, Change string }

// LabelledLine is "Installed" and "3 · a, b, c".
type LabelledLine struct{ Label, Text string }

// MaxPackageRows is how many upgrades the expander lists.
const MaxPackageRows = 60

// FreedBlock is the Space freed column.
type FreedBlock struct {
	Rows []FreedRow
	// Total is "3.1 GiB", or "at least 3.1 GiB" when some row is unknown.
	Total string
}

// FreedRow is one cleaned area.
type FreedRow struct {
	Label string
	// Value is "1.83 GiB", "0 B" or "unknown".
	Value string
	// Fraction is the bar's fill: the row against the largest row.
	Fraction float64
	// Zero: the step ran and nothing was there (muted); Unknown: it
	// couldn't be measured.
	Zero, Unknown bool
}

// Summary is the model's report as the finished page shows it, or nil for
// a run that has none to show: a run still going, a dry run, an action
// with no report, a report without data. Those keep the page as it was.
func (m *Model) Summary() *Summary {
	r := m.Report
	if r == nil || m.Finished == nil || m.DryRun || m.Stopped != "" || m.Discovery != nil || !r.HasData() {
		return nil
	}
	f := m.Finished
	clean := f.Outcome == engine.OutcomeCompleted && f.Errs == 0 && f.Refused == 0
	s := BuildSummary(*r, clean)
	return &s
}

// BuildSummary is r's summary; clean says the queue ended without a failed
// or refused step and without an abort.
func BuildSummary(r report.Report, clean bool) Summary {
	var s Summary
	switch {
	case len(r.Attention) > 0:
		s.Kicker, s.Tone, s.Headline = "NEEDS YOUR ATTENTION", "warn", r.Attention[0].Short
	case !clean:
		s.Kicker, s.Tone, s.Headline = "CHECK THE STEPS", "warn", "The run did not finish cleanly"
	default:
		s.Kicker, s.Tone, s.Headline = quietHeadline(r.Action)
	}
	for _, a := range r.Attention {
		s.Attention = append(s.Attention, attentionRow(a))
	}
	s.Sub = subLine(r)
	s.Packages = packagesBlock(r)
	s.Freed = freedBlock(r)
	s.Cache = cacheLine(r.Cache)
	if a := r.Audit; a != nil && a.Findings > 0 {
		s.Audit = plural.N(a.Findings, "finding", "findings")
		if a.High > 0 {
			s.Audit += fmt.Sprintf(" (%d high)", a.High)
		}
		if len(a.Names) > 0 {
			s.Audit += ": " + nameList(a.Names, 3)
		}
	}
	for _, n := range r.Notes {
		s.Notes = append(s.Notes, sanitize.OneLine(n))
	}
	return s
}

// doneHeadline is what a clean run of an action that is not an update says;
// the wording is shared with the TUI (actions.DoneHeadlines).
var doneHeadline = actions.DoneHeadlines

// quietHeadline is the kicker, tone and headline of a clean run with nothing
// that needs the user.
func quietHeadline(action string) (kicker, tone, headline string) {
	if h, ok := doneHeadline[action]; ok {
		return "DONE", "ok", h
	}
	return "ALL QUIET", "ok", "Everything is up to date"
}

func attentionRow(a report.Attention) AttentionRow {
	row := AttentionRow{Kind: a.Kind, Title: sanitize.OneLine(a.Short)}
	// Every text starts with its short form and a colon ("Restart
	// recommended: ..."): the title says that already.
	text := sanitize.OneLine(a.Text)
	text = strings.TrimPrefix(text, a.Short+": ")
	if text == a.Short {
		text = ""
	}
	row.Text = text
	switch a.Kind {
	case report.KindPacnew, report.KindPacsave:
		row.Command, row.Button = PacdiffCommand, "Copy pacdiff command"
	}
	return row
}

// subLine is "47 packages upgraded · 3.1 GiB freed · 14 m 52 s": each part
// only when the report has it.
func subLine(r report.Report) string {
	var parts []string
	if r.Packages != nil {
		parts = append(parts, plural.N(len(r.Packages.Upgraded), "package", "packages")+" upgraded")
	}
	if len(r.Freed) > 0 {
		switch {
		case r.FreedPartial && r.FreedBytes == 0:
			parts = append(parts, "freed space unknown")
		case r.FreedPartial:
			parts = append(parts, "at least "+report.HumanBytes(r.FreedBytes)+" freed")
		default:
			parts = append(parts, report.HumanBytes(r.FreedBytes)+" freed")
		}
	}
	if r.Elapsed > 0 {
		parts = append(parts, Duration(r.Elapsed))
	}
	return strings.Join(parts, " · ")
}

// Duration writes a run's length as "52 s", "6 m 08 s" or "1 h 02 m", as
// the TUI's summary does.
func Duration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%d h %02d m", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%d m %02d s", s/60, s%60)
	}
	return fmt.Sprintf("%d s", s)
}

func packagesBlock(r report.Report) *PackagesBlock {
	p := r.Packages
	if p == nil {
		if o := r.Orphans; o != nil && len(o.Names) > 0 {
			return &PackagesBlock{Head: plural.N(len(o.Names), "orphan", "orphans") + " removed",
				Lines: []LabelledLine{{"Removed", nameList(o.Names, 3)}}}
		}
		return nil
	}
	b := &PackagesBlock{Head: fmt.Sprintf("%d upgraded", len(p.Upgraded))}
	if p.AURKnown && len(p.Upgraded) > 0 {
		b.Head += fmt.Sprintf(" (%d AUR)", p.AURUpgraded)
	}
	if len(p.Upgraded) > 0 {
		b.Top = nameList(topNames(p.Upgraded, r), 3)
	}
	for i, u := range p.Upgraded {
		if i == MaxPackageRows {
			b.Hidden = len(p.Upgraded) - MaxPackageRows
			break
		}
		b.Rows = append(b.Rows, PackageRow{Name: safeName(u.Name), Change: change(u)})
	}
	line := func(label string, ps []report.Pkg) {
		if len(ps) > 0 {
			b.Lines = append(b.Lines, LabelledLine{label, fmt.Sprintf("%d · %s", len(ps), nameList(pkgNames(ps), 3))})
		}
	}
	line("Installed", p.Installed)
	line("Removed", p.Removed)
	line("Downgraded", p.Downgraded)
	return b
}

func change(p report.Pkg) string {
	return sanitize.Field(p.Old, 40) + " → " + sanitize.Field(p.New, 40)
}

// topNames puts the packages a restart item names (the kernel, systemd, the
// driver) first, then the rest in the order pacman upgraded them.
func topNames(ps []report.Pkg, r report.Report) []string {
	named := map[string]bool{}
	for _, it := range r.Attention {
		if it.Kind == report.KindRestart {
			for _, n := range it.Detail {
				named[n] = true
			}
		}
	}
	var first, rest []string
	for _, p := range ps {
		if named[p.Name] {
			first = append(first, safeName(p.Name))
		} else {
			rest = append(rest, safeName(p.Name))
		}
	}
	return append(first, rest...)
}

func pkgNames(ps []report.Pkg) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = safeName(p.Name)
	}
	return out
}

// safeName is a name from outside archmaid, safe on one line.
func safeName(n string) string { return sanitize.QuoteName(n, 40) }

// nameList is the first max names, then "+N more".
func nameList(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(names[:max], ", "), len(names)-max)
}

func freedBlock(r report.Report) *FreedBlock {
	if len(r.Freed) == 0 {
		return nil
	}
	var biggest uint64
	for _, a := range r.Freed {
		if a.Known {
			biggest = max(biggest, a.Bytes)
		}
	}
	b := &FreedBlock{Total: report.HumanBytes(r.FreedBytes)}
	if r.FreedPartial {
		b.Total = "at least " + b.Total
	}
	for _, a := range r.Freed {
		row := FreedRow{Label: sanitize.OneLine(a.Label), Value: report.HumanBytes(a.Bytes)}
		switch {
		case !a.Known:
			row.Value, row.Unknown = "unknown", true
		case a.Bytes == 0:
			row.Zero = true
		default:
			row.Fraction = float64(a.Bytes) / float64(biggest)
		}
		b.Rows = append(b.Rows, row)
	}
	return b
}

// cacheLine is what the package cache still holds, and the menu entry
// that frees it.
func cacheLine(c *report.CacheHold) string {
	switch {
	case c == nil:
		return ""
	case c.Bytes == 0 && c.Packages == 0:
		return "The package cache is empty."
	}
	return fmt.Sprintf("The package cache still holds %s (%s). %q in the menu frees it.",
		report.HumanBytes(c.Bytes), plural.N(c.Packages, "package", "packages"), CacheMenuLabel)
}
