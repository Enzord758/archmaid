package runview

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/report"
)

// busyReport is the owner's mockup (#187) busy run: a restart, a .pacnew,
// 47 upgrades (6 AUR), 3 installs, 2 orphans, five freed areas and the
// cache still holding 4.2 GiB.
func busyReport() report.Report {
	var up []report.Pkg
	for i, n := range []string{"linux-cachyos", "systemd", "nvidia-utils", "mesa", "firefox"} {
		up = append(up, report.Pkg{Name: n, Old: fmt.Sprintf("1.%d-1", i), New: fmt.Sprintf("2.%d-1", i)})
	}
	for i := len(up); i < 47; i++ {
		up = append(up, report.Pkg{Name: fmt.Sprintf("pkg%02d", i), Old: "1-1", New: "2-1", AUR: i < 11})
	}
	return report.Report{
		Action:  "fullcleanup",
		Elapsed: 14*time.Minute + 52*time.Second,
		Steps:   report.StepTally{Done: 8, Skipped: 1},
		Packages: &report.Packages{Upgraded: up, AURUpgraded: 6, AURKnown: true,
			Installed: []report.Pkg{{Name: "cachyos-gaming-meta", New: "1-1"}, {Name: "zenity", New: "4-1"}, {Name: "x", New: "1"}, {Name: "y", New: "1"}},
			Removed:   []report.Pkg{{Name: "gtk2", Old: "2-1"}}},
		Freed: []report.Area{
			{ID: report.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 1966 << 20, Known: true},
			{ID: report.AreaPkgUninstalled, Label: "Uninstalled packages' cache", Bytes: 212 << 20, Known: true},
			{ID: report.AreaYay, Label: "AUR build cache", Bytes: 648 << 20, Known: true},
			{ID: report.AreaJournal, Label: "Journal vacuum", Bytes: 381 << 20, Known: true},
			{ID: report.AreaTmpfiles, Label: "Temp files", Bytes: 54 << 20, Known: true},
		},
		FreedBytes: 3261 << 20,
		Cache:      &report.CacheHold{Bytes: 4509715661, Packages: 312, Source: "paccache"},
		Attention: []report.Attention{
			{Kind: report.KindRestart, Short: "Restart recommended", Text: "Restart recommended: linux-cachyos, systemd upgraded (running 7.2.10, installed 7.2.12)", Detail: []string{"linux-cachyos", "systemd"}},
			{Kind: report.KindPacnew, Short: "1 new .pacnew", Text: "1 new .pacnew: /etc/pacman.d/mirrorlist.pacnew (merge it with pacdiff)", Detail: []string{"/etc/pacman.d/mirrorlist.pacnew"}},
		},
	}
}

// quietReport is the mockup's quiet run: nothing needs the user, one row
// freed nothing.
func quietReport() report.Report {
	return report.Report{
		Elapsed:  6*time.Minute + 8*time.Second,
		Packages: &report.Packages{Upgraded: []report.Pkg{{Name: "ripgrep", Old: "14.1.1-1", New: "15.0.0-1"}}, AURKnown: true},
		Freed: []report.Area{
			{ID: report.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 286 << 20, Known: true},
			{ID: report.AreaJournal, Label: "Journal vacuum", Known: true},
		},
		FreedBytes: 286 << 20,
		Cache:      &report.CacheHold{Bytes: 4 << 30, Packages: 298},
	}
}

func TestSummaryBusy(t *testing.T) {
	s := BuildSummary(busyReport(), true)
	if s.Kicker != "NEEDS YOUR ATTENTION" || s.Tone != "warn" || s.Headline != "Restart recommended" {
		t.Errorf("hero %q %q %q", s.Kicker, s.Tone, s.Headline)
	}
	if want := "47 packages upgraded · 3.18 GiB freed · 14 m 52 s"; s.Sub != want {
		t.Errorf("sub %q, want %q", s.Sub, want)
	}
	if len(s.Attention) != 2 {
		t.Fatalf("attention %+v", s.Attention)
	}
	if a := s.Attention[0]; a.Title != "Restart recommended" || a.Command != "" || a.Button != "" ||
		a.Text != "linux-cachyos, systemd upgraded (running 7.2.10, installed 7.2.12)" {
		t.Errorf("restart row %+v: no button, the title not repeated in the text", a)
	}
	if a := s.Attention[1]; a.Command != "sudo pacdiff" || a.Button != "Copy pacdiff command" || !strings.Contains(a.Text, "mirrorlist.pacnew") {
		t.Errorf("pacnew row %+v", a)
	}
	p := s.Packages
	if p == nil || p.Head != "47 upgraded (6 AUR)" {
		t.Fatalf("packages %+v", p)
	}
	// The restart item's packages come first.
	if want := "linux-cachyos, systemd, nvidia-utils +44 more"; p.Top != want {
		t.Errorf("top %q, want %q", p.Top, want)
	}
	if len(p.Rows) != 47 || p.Hidden != 0 || p.Rows[0].Change != "1.0-1 → 2.0-1" {
		t.Errorf("rows %d hidden %d first %+v", len(p.Rows), p.Hidden, p.Rows[0])
	}
	want := []LabelledLine{{"Installed", "4 · cachyos-gaming-meta, zenity, x +1 more"}, {"Removed", "1 · gtk2"}}
	if !slices.Equal(p.Lines, want) {
		t.Errorf("lines %+v, want %+v", p.Lines, want)
	}
	f := s.Freed
	if f == nil || f.Total != "3.18 GiB" || len(f.Rows) != 5 || f.Rows[0].Fraction != 1 || f.Rows[0].Value != "1.92 GiB" {
		t.Fatalf("freed %+v", f)
	}
	if want := `The package cache still holds 4.2 GiB (312 packages). "Delete entire package cache" in the menu frees it.`; s.Cache != want {
		t.Errorf("cache %q, want %q", s.Cache, want)
	}
	if s.Audit != "" {
		t.Errorf("audit line %q without findings", s.Audit)
	}
}

func TestSummaryQuiet(t *testing.T) {
	s := BuildSummary(quietReport(), true)
	if s.Kicker != "ALL QUIET" || s.Tone != "ok" || s.Headline != "Everything is up to date" || len(s.Attention) != 0 {
		t.Errorf("hero %q %q %q, %d rows", s.Kicker, s.Tone, s.Headline, len(s.Attention))
	}
	if want := "1 package upgraded · 286 MiB freed · 6 m 08 s"; s.Sub != want {
		t.Errorf("sub %q, want %q", s.Sub, want)
	}
	if p := s.Packages; p.Head != "1 upgraded (0 AUR)" || len(p.Lines) != 0 {
		t.Errorf("installed/removed lines only when not zero: %+v", p)
	}
	if r := s.Freed.Rows[1]; !r.Zero || r.Value != "0 B" || r.Fraction != 0 {
		t.Errorf("zero row %+v", r)
	}
	// A run that didn't end cleanly is not "all quiet".
	if s := BuildSummary(quietReport(), false); s.Kicker == "ALL QUIET" || s.Headline == "Everything is up to date" {
		t.Errorf("an unclean run says %q / %q", s.Kicker, s.Headline)
	}
}

func TestSummaryUnknownPartialAndAudit(t *testing.T) {
	r := report.Report{
		Freed: []report.Area{
			{ID: report.AreaJournal, Label: "Journal vacuum"},
			{ID: report.AreaTmpfiles, Label: "Temp files", Bytes: 54 << 20, Known: true},
		},
		FreedBytes: 54 << 20, FreedPartial: true,
		Audit: &report.AuditNote{Findings: 2, High: 1, Names: []string{"evil-bin", "meh"}},
		Cache: &report.CacheHold{},
		Notes: []string{"pacman.log was rotated"},
	}
	s := BuildSummary(r, true)
	if j := s.Freed.Rows[0]; j.Value != "unknown" || !j.Unknown || j.Fraction != 0 {
		t.Errorf("unknown row %+v", j)
	}
	if s.Freed.Total != "at least 54 MiB" || s.Sub != "at least 54 MiB freed" {
		t.Errorf("partial total %q / %q", s.Freed.Total, s.Sub)
	}
	if s.Audit != "2 findings (1 high): evil-bin, meh" {
		t.Errorf("audit %q", s.Audit)
	}
	if s.Cache != "The package cache is empty." || s.Packages != nil {
		t.Errorf("cache %q packages %+v", s.Cache, s.Packages)
	}
	if !slices.Equal(s.Notes, r.Notes) {
		t.Errorf("notes %q", s.Notes)
	}
	r.Freed[1].Known, r.Freed[1].Bytes, r.FreedBytes = false, 0, 0
	if s := BuildSummary(r, true); s.Sub != "freed space unknown" || s.Freed.Total != "at least 0 B" {
		t.Errorf("all unknown: %q / %q", s.Sub, s.Freed.Total)
	}
}

func TestSummaryManyPackagesAndOrphans(t *testing.T) {
	var up []report.Pkg
	for i := 0; i < MaxPackageRows+9; i++ {
		up = append(up, report.Pkg{Name: fmt.Sprintf("p%d", i), Old: "1", New: "2"})
	}
	p := BuildSummary(report.Report{Packages: &report.Packages{Upgraded: up}}, true).Packages
	if len(p.Rows) != MaxPackageRows || p.Hidden != 9 || p.Head != "69 upgraded" {
		t.Errorf("rows %d hidden %d head %q", len(p.Rows), p.Hidden, p.Head)
	}
	o := BuildSummary(report.Report{Orphans: &report.Orphans{Names: []string{"gtk2", "pyxdg"}, Total: 3}}, true).Packages
	if o == nil || o.Head != "2 orphans removed" || o.Lines[0].Text != "gtk2, pyxdg" {
		t.Errorf("orphans without the log: %+v", o)
	}
}

// TestSummaryCacheLabelIsTheMenu: the "still holds" line names the menu
// entry that frees the cache, in the entry's own words.
func TestSummaryCacheLabelIsTheMenu(t *testing.T) {
	a, ok := actions.ByID(actions.IDWipeCache)
	if !ok || a.Label != CacheMenuLabel || CacheMenuLabel != "Delete entire package cache" {
		t.Errorf("CacheMenuLabel %q, the menu entry %q", CacheMenuLabel, a.Label)
	}
}

// TestModelSummary: the model holds the report from RunReport until a new
// queue; the summary shows only for a finished, real run with data.
func TestModelSummary(t *testing.T) {
	step := engine.PlanStep{ID: "x", Title: "X"}
	start := engine.QueueStarted{Action: "fullcleanup", Steps: []engine.PlanStep{step}, Started: time.Now()}
	fin := engine.Finished{Action: "fullcleanup", Outcome: engine.OutcomeCompleted}
	rep := engine.RunReport{Report: busyReport()}

	m := NewModel()
	if m.Summary() != nil {
		t.Error("a summary before any run")
	}
	c := m.Apply([]engine.Event{start, rep})
	if !c.Report || m.Report == nil {
		t.Errorf("RunReport: Change.Report %v, model report %v", c.Report, m.Report)
	}
	if m.Summary() != nil {
		t.Error("a summary while the run goes")
	}
	m.Apply([]engine.Event{fin})
	s := m.Summary()
	if s == nil || s.Kicker != "NEEDS YOUR ATTENTION" {
		t.Fatalf("finished summary %+v", s)
	}
	m.DryRun = true
	if m.Summary() != nil {
		t.Error("a dry run has a summary")
	}
	m.DryRun = false
	m.Report.Attention, m.Report.Packages, m.Report.Freed = nil, nil, nil
	if m.Summary() != nil {
		t.Error("a report without data has a summary: the page keeps today's finished state")
	}
	m.Apply([]engine.Event{start})
	if m.Report != nil {
		t.Error("a new queue keeps the last run's report")
	}
	// Failed steps: attention still outranks the unclean-run kicker.
	m.Apply([]engine.Event{rep, engine.Finished{Action: "fullcleanup", Outcome: engine.OutcomeCompleted, Errs: 1}})
	if s := m.Summary(); s == nil || s.Kicker != "NEEDS YOUR ATTENTION" {
		t.Errorf("attention outranks the failed-run kicker: %+v", s)
	}
}
