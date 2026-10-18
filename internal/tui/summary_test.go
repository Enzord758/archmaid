package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

func pk(name, old, new string, aur bool) report.Pkg {
	return report.Pkg{Name: name, Old: old, New: new, AUR: aur}
}

// busyReport is the owner's busy week (the #187 mockup): 47 upgraded, 6 of
// them AUR, a restart, a .pacnew, a high audit finding.
func busyReport() *report.Report {
	up := []report.Pkg{
		pk("firefox", "143.0", "144.0", false),
		pk("linux-cachyos", "7.2.10-1", "7.2.12-1", false),
		pk("systemd", "257.9-1", "258.1-2", false),
		pk("nvidia-utils", "580.82", "580.95", false),
		pk("mesa", "25.2.4", "25.2.5", false),
		pk("yay-bin", "12.5.1", "12.5.2", true),
	}
	for i := len(up); i < 47; i++ {
		up = append(up, pk("filler-"+string(rune('a'+i%26)), "1", "2", i < 11))
	}
	return &report.Report{
		Action: "full-cleanup", Elapsed: 14*time.Minute + 52*time.Second,
		Steps: report.StepTally{Done: 8, Skipped: 1},
		Packages: &report.Packages{Upgraded: up, AURUpgraded: 6, AURKnown: true,
			Installed: []report.Pkg{pk("cachyos-gaming-meta", "", "1", false), pk("lib32-vulkan-icd-loader", "", "1", false), pk("zenity", "", "4", false)},
			Removed:   []report.Pkg{pk("python-pyxdg-old", "1", "", false), pk("gtk2", "2", "", false)}},
		Freed: []report.Area{
			{ID: report.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 1966080000, Known: true},
			{ID: report.AreaPkgUninstalled, Label: "Uninstalled packages' cache", Bytes: 212 << 20, Known: true},
			{ID: report.AreaYay, Label: "AUR build cache", Bytes: 648 << 20, Known: true},
			{ID: report.AreaJournal, Label: "Journal vacuum", Bytes: 381 << 20, Known: true},
			{ID: report.AreaTmpfiles, Label: "Temp files", Bytes: 54 << 20, Known: true},
		},
		FreedBytes: 1966080000 + (212+648+381+54)<<20,
		Cache:      &report.CacheHold{Bytes: 4509715661, Packages: 312, Source: "paccache"},
		Attention: []report.Attention{
			{Kind: report.KindRestart, Short: "Restart recommended", Detail: []string{"linux-cachyos", "systemd", "nvidia-utils", "mesa"},
				Text: "Restart recommended: linux-cachyos, systemd, nvidia-utils, mesa upgraded (running 7.2.10-1-cachyos, installed 7.2.12-1-cachyos)"},
			{Kind: report.KindPacnew, Short: "1 new .pacnew", Text: "1 new .pacnew: /etc/pacman.d/mirrorlist.pacnew (merge it with pacdiff)"},
			{Kind: report.KindAudit, Short: "1 AUR warning", Text: "AUR audit: 1 high-severity finding (see the audit log)"},
		},
		Audit: &report.AuditNote{Total: 12, Findings: 2, High: 1, Names: []string{"evil-bin", "meh-git"}},
	}
}

// quietReport is a quiet week: six upgrades, nothing to do about them.
func quietReport() *report.Report {
	return &report.Report{
		Action: "full-cleanup", Elapsed: 6*time.Minute + 8*time.Second,
		Steps: report.StepTally{Done: 9},
		Packages: &report.Packages{AURKnown: true, AURUpgraded: 1, Upgraded: []report.Pkg{
			pk("ripgrep", "14", "15", false), pk("python-pip", "25.2-1", "25.2-2", false), pk("yay-bin", "12.5.1", "12.5.2", true),
			pk("a", "1", "2", false), pk("b", "1", "2", false), pk("c", "1", "2", false)}},
		Freed: []report.Area{
			{ID: report.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 286 << 20, Known: true},
			{ID: report.AreaPkgUninstalled, Label: "Uninstalled packages' cache", Known: true},
			{ID: report.AreaYay, Label: "AUR build cache", Bytes: 96 << 20, Known: true},
			{ID: report.AreaJournal, Label: "Journal vacuum", Known: true},
			{ID: report.AreaTmpfiles, Label: "Temp files", Bytes: 28 << 20, Known: true},
		},
		FreedBytes: 410 << 20,
		Cache:      &report.CacheHold{Bytes: 4187593114, Packages: 298, Source: "paccache"},
	}
}

// summaryModel is the summary screen of a finished queue with report r, at
// the given size.
func summaryModel(r *report.Report, w, h int, statuses ...steps.Status) model {
	m := update(fixedModel(), tea.WindowSizeMsg{Width: w, Height: h})
	m.screen = screenSummary
	m.queue = goldenQueue(statuses...)
	m.current = len(m.queue)
	m.runReport = r
	for _, s := range statuses {
		switch s {
		case steps.Skipped:
			m.skipped++
		case steps.Failed:
			m.errs++
		}
	}
	return m
}

func sizedGolden(t *testing.T, m model) {
	t.Helper()
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// TestSummaryReportGolden: the busy and the quiet run (the #187 mockup's TUI
// block, variant C) on a tall 80-column terminal, and the busy one on the
// 80x24 the other goldens use, where the checklist keeps only what didn't
// succeed and the block drops its zero rows.
func TestSummaryReportGolden(t *testing.T) {
	ok := steps.OK
	t.Run("busy", func(t *testing.T) { sizedGolden(t, summaryModel(busyReport(), 80, 60, ok, steps.Skipped, ok, ok)) })
	t.Run("quiet", func(t *testing.T) { sizedGolden(t, summaryModel(quietReport(), 80, 60, ok, ok, ok, ok)) })
	t.Run("busy_short", func(t *testing.T) { sizedGolden(t, summaryModel(busyReport(), 80, 24, ok, steps.Skipped, ok, ok)) })
	t.Run("narrow", func(t *testing.T) { sizedGolden(t, summaryModel(busyReport(), 60, 60, ok, steps.Skipped, ok, ok)) })
}

// TestSummaryReportFits: no line is wider than the terminal, at any width
// from 40 columns up, for the busy and the quiet run, with hostile names.
func TestSummaryReportFits(t *testing.T) {
	hostile := busyReport()
	hostile.Packages.Upgraded[0].Name = "evil\x1b]52;c;AAAA\x07-with-an-extremely-long-name-that-goes-on-and-on"
	hostile.Notes = []string{"AUR packages are not marked: pacman -Qqm gave no answer, and the note goes on for a good while longer than a line."}
	for _, r := range []*report.Report{busyReport(), quietReport(), hostile} {
		for _, w := range []int{40, 50, 60, 80, 100, 140} {
			out := summaryBlock(r, summaryOpts{width: w})
			if strings.ContainsAny(out, "\x1b\x07") && !strings.Contains(out, "\x1b[") {
				t.Errorf("width %d: a raw escape reached the screen: %q", w, out)
			}
			for _, l := range strings.Split(out, "\n") {
				if n := utf8.RuneCountInString(l); n > w {
					t.Errorf("width %d: line of %d columns: %q", w, n, l)
				}
			}
		}
	}
}

// TestSummaryReportWording: the parts the owner asked for, in their words.
func TestSummaryReportWording(t *testing.T) {
	busy := summaryBlock(busyReport(), summaryOpts{width: 80})
	for _, want := range []string{
		"Summary  ·  took 14 m 52 s  ·  9 steps: 8 done, 1 skipped, 0 failed",
		"47 upgraded (6 AUR)",
		"linux-cachyos, systemd, nvidia-utils, mesa +43 more", // the packages that call for a restart first
		"Installed   3 · cachyos-gaming-meta, lib32-vulkan-icd-loader, zenity",
		"Removed     2 · python-pyxdg-old, gtk2",
		"Freed       3.1 GiB",
		"Package cache (keeps 3)",
		"Package cache still holds 4.2 GiB (312 packages).",
		`"Delete entire package cache" in the menu frees it.`,
		"Needs your attention",
		"⚠ Restart recommended: linux-cachyos",
		"Audit       2 findings (1 high): evil-bin, meh-git",
	} {
		if !strings.Contains(busy, want) {
			t.Errorf("busy block lacks %q:\n%s", want, busy)
		}
	}
	quiet := summaryBlock(quietReport(), summaryOpts{width: 80})
	for _, want := range []string{"6 upgraded (1 AUR)", "Freed       410 MiB", "✔ Nothing needs your attention."} {
		if !strings.Contains(quiet, want) {
			t.Errorf("quiet block lacks %q:\n%s", want, quiet)
		}
	}
	for _, not := range []string{"Installed", "Removed", "Downgraded", "Audit", "Needs your attention", "Note:"} {
		if strings.Contains(quiet, not) {
			t.Errorf("quiet block shows %q:\n%s", not, quiet)
		}
	}
}

// TestSummaryCacheLabelIsTheMenu: the "still holds" line names the menu entry
// that frees the cache; the wording is the entry's own.
func TestSummaryCacheLabelIsTheMenu(t *testing.T) {
	if cacheMenuLabel != "Delete entire package cache" {
		t.Errorf("cacheMenuLabel = %q: the owner's wording for the menu entry", cacheMenuLabel)
	}
}

// TestSummaryReportParts: unknown rows, a partial total, an empty cache, no
// package list, orphans without the log, zero rows trimmed in short mode.
func TestSummaryReportParts(t *testing.T) {
	r := &report.Report{
		Steps:   report.StepTally{Done: 2},
		Orphans: &report.Orphans{Names: []string{"gtk2", "pyxdg"}, Total: 3},
		Freed: []report.Area{
			{ID: report.AreaJournal, Label: "Journal vacuum", Known: false},
			{ID: report.AreaTmpfiles, Label: "Temp files", Bytes: 54 << 20, Known: true},
			{ID: report.AreaPaccache, Label: "Package cache (keeps 3)", Known: true},
		},
		FreedBytes: 54 << 20, FreedPartial: true,
		Cache: &report.CacheHold{Source: "folder"},
		Notes: []string{"The package log couldn't be read, so package changes are not shown."},
	}
	out := summaryBlock(r, summaryOpts{width: 80})
	for _, want := range []string{"Freed       at least 54 MiB", "unknown", "The package cache is empty.", "Removed     2 orphans · gtk2, pyxdg", "Note: The package log couldn't be read"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Packages ") {
		t.Errorf("a report without packages has a Packages line:\n%s", out)
	}
	trimmed := summaryBlock(r, summaryOpts{width: 80, trimZeros: true})
	if strings.Contains(trimmed, "Package cache (keeps 3)") || !strings.Contains(trimmed, "unknown") || !strings.Contains(trimmed, "Temp files") {
		t.Errorf("trimZeros must drop known zero rows only:\n%s", trimmed)
	}
	if got := summaryBlock(&report.Report{}, summaryOpts{}); got != "" {
		t.Errorf("an empty report drew %q", got)
	}
	if got := summaryBlock(nil, summaryOpts{}); got != "" {
		t.Errorf("no report drew %q", got)
	}
}

// TestNameList: names that fit, then "+N more"; one always shows.
func TestNameList(t *testing.T) {
	names := []string{"linux-cachyos", "systemd", "nvidia-utils", "mesa", "firefox", "ripgrep"}
	for _, c := range []struct {
		max, width int
		want       string
	}{
		{4, 80, "linux-cachyos, systemd, nvidia-utils, mesa +2 more"},
		{4, 30, "linux-cachyos, systemd +4 more"},
		{4, 5, "linu…"}, // never wider than asked
		{10, 200, "linux-cachyos, systemd, nvidia-utils, mesa, firefox, ripgrep"},
	} {
		if got := nameList(names, c.max, c.width); got != c.want {
			t.Errorf("nameList(max %d, width %d) = %q, want %q", c.max, c.width, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0 s", 52 * time.Second: "52 s", 6*time.Minute + 8*time.Second: "6 m 08 s",
		14*time.Minute + 52*time.Second: "14 m 52 s", time.Hour + 2*time.Minute + 59*time.Second: "1 h 02 m",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestRunReportEventFlow: the model keeps the Driver's RunReport and shows
// it on the summary screen; a new queue forgets it; without one the summary
// screen is what it was.
func TestRunReportEventFlow(t *testing.T) {
	m := sizedModel()
	m = feed(m, engine.QueueStarted{Action: "full-cleanup", Steps: []engine.PlanStep{{ID: "a", Title: "A…", LogLabel: "A"}}})
	m = feed(m, engine.StepStarted{ID: "a", Index: 0}, engine.StepDone{ID: "a", Index: 0, Status: steps.OK})
	m = feed(m, engine.RunReport{Report: *quietReport()}, engine.Finished{Action: "full-cleanup", Outcome: engine.OutcomeCompleted})
	if m.screen != screenSummary || m.runReport == nil {
		t.Fatalf("screen %d, report %v", m.screen, m.runReport)
	}
	if v := m.View(); !strings.Contains(v, "6 upgraded (1 AUR)") || !strings.Contains(v, "Cleanup completed successfully") {
		t.Errorf("summary screen:\n%s", v)
	}
	// The report sits between the checklist and the outcome line.
	v := m.View()
	if !(strings.Index(v, "✔ A") < strings.Index(v, "Summary") && strings.Index(v, "Summary") < strings.Index(v, "Cleanup completed")) {
		t.Errorf("order wrong:\n%s", v)
	}
	m = feed(m, engine.QueueStarted{Action: "x", Steps: []engine.PlanStep{{ID: "a", Title: "A…"}}})
	if m.runReport != nil {
		t.Error("a new queue kept the last run's report")
	}
	m = feed(m, engine.StepStarted{ID: "a", Index: 0}, engine.StepDone{ID: "a", Index: 0, Status: steps.OK},
		engine.Finished{Action: "x", Outcome: engine.OutcomeCompleted})
	if strings.Contains(m.View(), "Summary") {
		t.Errorf("a run without a report shows a summary block:\n%s", m.View())
	}
}

// TestDriverDepsRunReport: the binary's Driver makes the run report with
// report.DefaultConfig, and every Driver the tests build gets none (the
// real config reads the machine's pacman log and runs paccache and pacman).
func TestDriverDepsRunReport(t *testing.T) {
	ptr := func(f func() *report.Config) uintptr { return reflect.ValueOf(f).Pointer() }
	if prodRunReportConfig == nil || ptr(prodRunReportConfig) != ptr(report.DefaultConfig) {
		t.Error("the binary's runReportConfig is not report.DefaultConfig")
	}
	if driverDeps(nil).RunReport != nil {
		t.Fatal("a test's driverDeps has a RunReport: TestMain's nil is gone")
	}
	// driverDeps hands the Driver whatever runReportConfig makes.
	fixture := &report.Config{PacmanLog: "fixture"}
	old := runReportConfig
	runReportConfig = func() *report.Config { return fixture }
	got := driverDeps(nil).RunReport
	runReportConfig = old
	if got != fixture {
		t.Error("driverDeps does not pass runReportConfig() on as the Driver's RunReport")
	}
	c := prodRunReportConfig()
	if c == nil || c.Query == nil || c.PacmanLog == "" || c.PacmanLog == report.DefaultPacmanLog {
		t.Errorf("the binary's config %+v (under testenv its log must not be the machine's)", c)
	}
}
