package ui

// The Run page's finished state with a run report (task #191, the owner's
// variant C of #187). A busy report (a restart, a .pacnew, 47 upgrades,
// five freed areas, the cache line, a long name) and a quiet one, each fed
// as a finished run, at the owner's window (1460x930), a narrow one (600
// px: the sidebar collapsed, one column), the narrowest (360 px) and a low
// one (960x372). At each: the hero says what the report says, the columns
// sit side by side or stacked, nothing is wider than the window, the
// folded output is hidden and unfolds on a click, the one button copies
// `sudo pacdiff` and nothing else, and a low window scrolls to the end of
// the summary. A run with no report, and a dry run's report, keep today's
// page: the timeline alone. With ARCHMAID_GUI_SHOTS: run-report-<size>-
// <case>.png.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/engine"
	rr "github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

func init() { covScenarios["cov-run-report"] = covRunReport }

func TestGUIRunReport(t *testing.T) {
	needDisplay(t)
	var buf bytes.Buffer
	childCmd(t, "cov-run-report", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) })
	for _, l := range strings.Split(buf.String(), "\n") {
		if glibWarned(l) {
			t.Errorf("GTK logged: %s", l)
		}
	}
}

// uiBusyReport is the mockup's busy run, with a package whose name is one
// long word and a long restart text.
func uiBusyReport() rr.Report {
	up := []rr.Pkg{
		{Name: "linux-cachyos", Old: "7.2.10-1", New: "7.2.12-1"},
		{Name: "systemd", Old: "257.9-1", New: "258.1-2"},
		{Name: "nvidia-utils", Old: "580.82-1", New: "580.95-1"},
		{Name: "mesa", Old: "25.2.4-1", New: "25.2.5-1"},
		{Name: longPkg, Old: "2026.1-1", New: "2026.2-1"},
	}
	for i := len(up); i < 47; i++ {
		up = append(up, rr.Pkg{Name: fmt.Sprintf("package-%02d", i), Old: "1.0-1", New: "1.1-1", AUR: i < 11})
	}
	return rr.Report{
		Action:  "fullcleanup",
		Elapsed: 14*time.Minute + 52*time.Second,
		Steps:   rr.StepTally{Done: 3, Skipped: 1},
		Packages: &rr.Packages{Upgraded: up, AURUpgraded: 6, AURKnown: true,
			Installed: []rr.Pkg{{Name: "cachyos-gaming-meta", New: "1-1"}, {Name: "lib32-vulkan-icd-loader", New: "1-1"}, {Name: "zenity", New: "4-1"}},
			Removed:   []rr.Pkg{{Name: "python-pyxdg-old", Old: "1-1"}, {Name: "gtk2", Old: "2-1"}}},
		Freed: []rr.Area{
			{ID: rr.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 1966 << 20, Known: true},
			{ID: rr.AreaPkgUninstalled, Label: "Uninstalled packages' cache", Bytes: 212 << 20, Known: true},
			{ID: rr.AreaYay, Label: "AUR build cache", Bytes: 648 << 20, Known: true},
			{ID: rr.AreaJournal, Label: "Journal vacuum", Known: false},
			{ID: rr.AreaTmpfiles, Label: "Temp files", Bytes: 0, Known: true},
		},
		FreedBytes: 2826 << 20, FreedPartial: true,
		Cache: &rr.CacheHold{Bytes: 4509715661, Packages: 312, Source: "paccache"},
		Attention: []rr.Attention{
			{Kind: rr.KindRestart, Short: "Restart recommended", Text: "Restart recommended: linux-cachyos, systemd, nvidia-utils and mesa upgraded (running 7.2.10, installed 7.2.12)", Detail: []string{"linux-cachyos", "systemd", "nvidia-utils", "mesa"}},
			{Kind: rr.KindPacnew, Short: "1 new .pacnew", Text: "1 new .pacnew: /etc/pacman.d/mirrorlist.pacnew (merge it with pacdiff)", Detail: []string{"/etc/pacman.d/mirrorlist.pacnew"}},
		},
		Audit: &rr.AuditNote{Total: 40, Findings: 2, High: 1, Names: []string{"evil-bin", "meh"}},
		Notes: []string{"pacman.log was rotated while the queue ran; the changes before that are not counted."},
	}
}

// uiQuietReport is the mockup's quiet run.
func uiQuietReport() rr.Report {
	return rr.Report{
		Action:  "fullcleanup",
		Elapsed: 6*time.Minute + 8*time.Second,
		Steps:   rr.StepTally{Done: 4},
		Packages: &rr.Packages{AURKnown: true, AURUpgraded: 1, Upgraded: []rr.Pkg{
			{Name: "ripgrep", Old: "14.1.1-1", New: "15.0.0-1"}, {Name: "python-pip", Old: "25.2-1", New: "25.2-2"}, {Name: "yay-bin", Old: "12.5.1-1", New: "12.5.2-1", AUR: true}}},
		Freed: []rr.Area{
			{ID: rr.AreaPaccache, Label: "Package cache (keeps 3)", Bytes: 286 << 20, Known: true},
			{ID: rr.AreaPkgUninstalled, Label: "Uninstalled packages' cache", Known: true},
			{ID: rr.AreaYay, Label: "AUR build cache", Bytes: 96 << 20, Known: true},
		},
		FreedBytes: 382 << 20,
		Cache:      &rr.CacheHold{Bytes: 4187593113, Packages: 298, Source: "paccache"},
	}
}

func covRunReport(c *covEnv) {
	w := c.w
	c.quiet()
	sizes := [][2]int{{1460, 930}, {600, 800}, {360, 700}, {960, 372}}
	type kase struct {
		name   string
		rep    *rr.Report
		dry    bool
		report bool // the summary is expected
	}
	busy, quiet := uiBusyReport(), uiQuietReport()
	cases := []kase{{"busy", &busy, false, true}, {"quiet", &quiet, false, true}, {"none", nil, false, false}, {"dry", &busy, true, false}}
	size, next := 0, 0
	plan := []engine.PlanStep{{ID: "s1", Title: "System update"}, {ID: "s2", Title: "Orphaned packages"}, {ID: "s3", Title: "Package cache"}, {ID: "s4", Title: "Temp files"}}

	var step func()
	step = func() {
		if next == len(cases) {
			next = 0
			size++
			if size == len(sizes) {
				c.finish()
				return
			}
			w.win.SetDefaultSize(sizes[size][0], sizes[size][1])
			coreglib.TimeoutAdd(500, step)
			return
		}
		k := cases[next]
		wd, ht := sizes[size][0], sizes[size][1]
		moment := fmt.Sprintf("%s at %dx%d", k.name, wd, ht)
		w.current = "fullcleanup"
		w.pickPage("run")
		evs := []engine.Event{
			engine.PhaseChanged{Phase: engine.PhaseRunning},
			engine.QueueStarted{Action: "fullcleanup", DryRun: k.dry, Started: time.Now(), Steps: plan},
		}
		for _, p := range plan {
			evs = append(evs, engine.StepStarted{ID: p.ID}, engine.OutputChunk{ID: p.ID, Lines: []string{"output of " + p.ID}},
				engine.StepDone{ID: p.ID, Status: steps.OK})
		}
		c.feed(evs...)
		c.ok(w.sum != nil && !w.sum.shown, "%s: a summary while the run goes", moment)
		// The report comes before Finished, as the Driver sends it: here as
		// a batch of its own, so the page must wait for Finished.
		if k.rep != nil {
			c.feed(engine.RunReport{Report: *k.rep})
			c.ok(!w.sum.shown, "%s: the summary shows before the run finished", moment)
		}
		c.feed(engine.Finished{Action: "fullcleanup", Outcome: engine.OutcomeCompleted, Elapsed: time.Minute}, engine.PhaseChanged{Phase: engine.PhaseIdle})
		coreglib.TimeoutAdd(500, func() {
			c.ok(w.narrow == (wd < 760), "%s: narrow is %v", moment, w.narrow)
			if k.report {
				checkRunSummary(c, moment, k.name, k.rep)
			} else {
				c.ok(!w.sum.shown && !w.sum.box.IsDrawable() && !w.sum.fold.IsDrawable(), "%s: the summary shows without a report to show", moment)
				c.ok(w.tl.scroll.IsDrawable() && w.tl.scroll.Height() > 50, "%s: the timeline isn't up (%d px)", moment, w.tl.scroll.Height())
			}
			c.ok(len(tooWide(w.toasts, w.win.Width())) == 0, "%s: wider than the window: %q", moment, tooWide(w.toasts, w.win.Width()))
			shoot(w, fmt.Sprintf("run-report-%dx%d", wd, ht), k.name)
			next++
			if !k.report {
				coreglib.TimeoutAdd(100, step)
				return
			}
			// Unfold the output, look, fold it again.
			w.sum.toggle.Emit("clicked")
			coreglib.TimeoutAdd(400, func() {
				c.ok(w.sum.expanded && w.tl.scroll.IsDrawable() && w.tl.scroll.Height() >= 100, "%s: the output didn't unfold (expanded %v, %d px)", moment, w.sum.expanded, w.tl.scroll.Height())
				c.ok(len(w.tl.sections) == len(plan), "%s: the unfolded timeline has %d sections", moment, len(w.tl.sections))
				c.ok(len(tooWide(w.toasts, w.win.Width())) == 0, "%s: unfolded, wider than the window: %q", moment, tooWide(w.toasts, w.win.Width()))
				shoot(w, fmt.Sprintf("run-report-%dx%d", wd, ht), k.name+"-output")
				w.sum.toggle.Emit("clicked")
				c.ok(!w.sum.expanded && !w.tl.scroll.IsVisible(), "%s: the output didn't fold again", moment)
				coreglib.TimeoutAdd(100, step)
			})
		})
	}
	w.win.SetDefaultSize(sizes[0][0], sizes[0][1])
	coreglib.TimeoutAdd(500, step)
}

// checkRunSummary checks the drawn summary of a report at the window's
// size.
func checkRunSummary(c *covEnv, moment, name string, r *rr.Report) {
	w := c.w
	s := w.sum
	c.ok(s.shown && s.box.IsDrawable() && s.fold.IsDrawable() && s.hero != nil, "%s: no summary drawn (shown %v)", moment, s.shown)
	if s.hero == nil {
		return
	}
	c.ok(!w.tl.scroll.IsVisible(), "%s: the output is not folded", moment)
	texts := strings.Join(labelTexts(s.box), "\n")
	want := []string{"ALL QUIET", "Everything is up to date", "3 packages upgraded · 382 MiB freed · 6 m 08 s", "3 upgraded (1 AUR)", "ripgrep, python-pip, yay-bin", "0 B", "382 MiB",
		"The package cache still holds 3.9 GiB (298 packages)."}
	if name == "busy" {
		want = []string{"NEEDS YOUR ATTENTION", "Restart recommended", "1 new .pacnew", "47 packages upgraded · at least 2.76 GiB freed · 14 m 52 s",
			"47 upgraded (6 AUR)", "linux-cachyos, systemd, nvidia-utils +44 more", "Installed", "Removed", "unknown", "at least 2.76 GiB",
			`"Delete entire package cache" in the menu frees it.`, "2 findings (1 high): evil-bin, meh", "Note: pacman.log was rotated"}
	}
	for _, x := range want {
		c.ok(strings.Contains(texts, x), "%s: the summary lacks %q:\n%s", moment, x, texts)
	}
	if name == "quiet" {
		c.ok(!strings.Contains(texts, "Installed") && !strings.Contains(texts, "Removed") && !strings.Contains(texts, "Audit"),
			"%s: Installed, Removed or Audit shown for a run with none:\n%s", moment, texts)
		c.ok(!strings.Contains(texts, "NEEDS YOUR ATTENTION") && len(s.copies) == 0, "%s: attention in a quiet run", moment)
	}
	// Nothing in the summary promises a reminder or runs anything.
	c.ok(!strings.Contains(texts, "Remind me"), "%s: a reminder button", moment)
	if bl := boundsOf(c, s.box, w.win); bl.w > float64(w.win.Width())+0.5 {
		c.fail("%s: the summary is %.0f px wide in a %d px window", moment, bl.w, w.win.Width())
	}

	// The columns.
	if s.cols == nil {
		c.fail("%s: no columns", moment)
		return
	}
	l, rt := s.cols.FirstChild(), s.cols.LastChild()
	lb, rb := boundsOf(c, l, w.win), boundsOf(c, rt, w.win)
	if w.narrow {
		c.ok(rb.y >= lb.y+lb.h-1 && rb.x <= lb.x+1, "%s: narrow, the columns aren't stacked (%+v, %+v)", moment, lb, rb)
	} else {
		c.ok(rb.x >= lb.x+lb.w-1 && absf(rb.y-lb.y) < 2, "%s: wide, the columns aren't side by side (%+v, %+v)", moment, lb, rb)
	}
	for _, b := range s.bars {
		c.ok(b.IsVisible() == !w.narrow, "%s: a space bar's visibility is %v, window narrow %v", moment, b.IsVisible(), w.narrow)
	}

	// The copy button: only on a .pacnew row, and it copies the command.
	if name == "busy" {
		c.ok(len(s.copies) == 1 && s.copies[0].Label() == "Copy pacdiff command", "%s: copy buttons %d", moment, len(s.copies))
		if len(s.copies) == 1 {
			w.copied = ""
			s.copies[0].Emit("clicked")
			c.ok(w.copied == "sudo pacdiff", "%s: the button copied %q", moment, w.copied)
			readClipboard(c, moment, "sudo pacdiff")
		}
	}

	// A low window scrolls to the end of the summary; the fold stays
	// reachable.
	if w.win.Height() < 500 {
		adj := s.scroll.VAdjustment()
		c.ok(adj.Upper()-adj.PageSize() > 1, "%s: the summary doesn't scroll (%.0f of %.0f)", moment, adj.PageSize(), adj.Upper())
		adj.SetValue(adj.Upper() - adj.PageSize())
		coreglib.TimeoutAdd(100, func() {
			fb := boundsOf(c, s.fold, w.win)
			c.ok(fb.y+fb.h <= float64(w.win.Height())+0.5 && fb.y >= 0, "%s: scrolled to the end, the fold row is at y %.0f-%.0f of %d", moment, fb.y, fb.y+fb.h, w.win.Height())
			adj.SetValue(0)
		})
	}
}

// readClipboard checks the clipboard really holds want (the copy button
// went through GDK, not just the window's record of it).
func readClipboard(c *covEnv, moment, want string) {
	c.w.win.Clipboard().ReadTextAsync(context.Background(), func(res gio.AsyncResulter) {
		got, err := c.w.win.Clipboard().ReadTextFinish(res)
		if err != nil || got != want {
			c.fail("%s: the clipboard holds %q (%v), want %q", moment, got, err, want)
		}
	})
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// boundsOf is wd's bounds in root's coordinates, failing the scenario
// without them.
func boundsOf(c *covEnv, wd, root gtk.Widgetter) box {
	b, ok := boundsIn(wd, root)
	if !ok {
		c.fail("no bounds for a %T", wd)
	}
	return b
}

// labelTexts are the labels' texts under root, in tree order.
func labelTexts(root gtk.Widgetter) []string {
	var out []string
	eachWidget(root, func(wd gtk.Widgetter) {
		if l, ok := wd.(*gtk.Label); ok && l.Text() != "" {
			out = append(out, l.Text())
		}
	})
	return out
}
