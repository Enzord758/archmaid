package ui

// The Packages page against the stub engine (task #237): search, sheet,
// confirm, run and summary, through the real window, the adapter and the
// stub's Packages scripts (stub.PackagesCatalog), on the demo catalog. Each
// scenario is a headless child like the page's other ones; nothing reads
// pacman's databases, the network or a real engine.

import (
	"slices"
	"strings"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

var pkgFlowScenarios = []string{"cov-pkg-flow-repo", "cov-pkg-flow-remove", "cov-pkg-flow-aur", "cov-pkg-flow-aur-high",
	"cov-pkg-flow-refused", "cov-pkg-flow-cancel", "cov-pkg-flow-stale"}

var flowClock *pkgview.FakeClock

func init() {
	for _, s := range pkgFlowScenarios {
		covOptions[s] = func(o *Options, _ *report) {
			flowClock = pkgview.NewFakeClock(time.Now())
			o.Packages = demoWiring()
			o.Packages.Clock = flowClock
		}
	}
	covScenarios["cov-pkg-flow-repo"] = flowRepo
	covScenarios["cov-pkg-flow-remove"] = flowRemove
	covScenarios["cov-pkg-flow-aur"] = func(c *covEnv) { flowAUR(c, "paru", "paru-bin", false) }
	covScenarios["cov-pkg-flow-aur-high"] = func(c *covEnv) { flowAUR(c, "risky", "risky-bin", true) }
	covScenarios["cov-pkg-flow-refused"] = flowRefused
	covScenarios["cov-pkg-flow-cancel"] = flowCancel
	covScenarios["cov-pkg-flow-stale"] = flowStale
}

// flowSeq runs the steps in order. A step is called every 100 ms until it
// returns true; one that takes more than 20 s fails the scenario.
func flowSeq(c *covEnv, steps ...func() bool) {
	var run func(i, tries int)
	run = func(i, tries int) {
		if i == len(steps) {
			if c.w.pkgs.ctl != nil {
				c.w.pkgs.closeSheet()
			}
			c.finish()
			return
		}
		coreglib.TimeoutAdd(100, func() bool {
			if steps[i]() {
				run(i+1, 0)
				return false
			}
			if tries++; tries > 200 {
				c.fail("flow step %d never completed (sheet %v, busy %v, page %q)", i, c.w.pkgs.sheetState, c.w.busy, c.w.stack.VisibleChildName())
				c.finish()
				return false
			}
			return true
		})
	}
	run(0, 0)
}

// once makes a step that acts the first time it is called and is done when
// cond holds (cond nil: at once).
func once(act func(), cond func() bool) func() bool {
	did := false
	return func() bool {
		if !did {
			did = true
			act()
		}
		return cond == nil || cond()
	}
}

// until is a step that only waits.
func until(cond func() bool) func() bool { return cond }

func sheetIs(p *pkgPage, st pkgview.SheetState) func() bool {
	return func() bool {
		return p.sheet != nil && p.sheetState == st && time.Since(p.sheetAt) > 700*time.Millisecond
	}
}

func runEnded(c *covEnv) func() bool {
	return func() bool { return c.w.ended != nil && !c.w.busy }
}

// summaryText is what the Run page's summary says.
func summaryText(c *covEnv) string {
	return strings.Join(labelsUnder(gtk.BaseWidget(c.w.sum.box)), "\n")
}

// search types q and lets both debounces run.
func flowSearch(c *covEnv, p *pkgPage, q string) []func() bool {
	return []func() bool{
		once(func() { p.entry.SetText(q); flowClock.Advance(120 * time.Millisecond) }, nil),
		once(func() { flowClock.Advance(500 * time.Millisecond) }, func() bool { return !p.ctl.Search.Busy() && !p.dirty }),
	}
}

func noEngineDialog(c *covEnv, where string) {
	c.ok(len(c.w.dialogs) == 0, "%s: the run model opened a dialog of its own (%d)", where, len(c.w.dialogs))
}

// ---- a repo install: search, sheet, confirm, run, summary ----

func flowRepo(c *covEnv) {
	w := c.w
	c.quiet()
	p := showPackages(c)
	steps := flowSearch(c, p, "firefox")
	steps = append(steps,
		once(func() {
			c.ok(slices.Contains(rowNames(p.results), "firefox"), "rows %q", rowNames(p.results))
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		}, nil),
		once(func() { btn(c, gtk.BaseWidget(p.root), "Install").Emit("clicked") }, func() bool { return w.busy && p.sheet != nil }),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() {
			d := p.sheet
			txt := dialogLabels(d, nil)
			c.ok(d.Heading() == "Install 1 package", "heading %q", d.Heading())
			for _, want := range []string{"foo", "libfoo", "libbar", "Download 4.5 MiB", "1 password prompt"} {
				c.ok(strings.Contains(txt, want), "the sheet lacks %q:\n%s", want, txt)
			}
			c.ok(w.current == "packages" && w.busy, "current %q busy %v", w.current, w.busy)
			c.ok(w.stack.VisibleChildName() == "packages", "the Run page came up before the confirm: %q", w.stack.VisibleChildName())
			noEngineDialog(c, "the sheet")
			c.ok(p.sheetYes != nil && p.sheetYes.Sensitive(), "Install is off for a plain repo install")
			shoot(w, "cov-pkg-flow-repo", "1-sheet")
			c.press(d, "yes")
		}, func() bool { return w.stack.VisibleChildName() == "run" }),
		until(runEnded(c)),
		once(func() {
			txt := summaryText(c)
			for _, want := range []string{"Packages installed", "3 packages installed"} {
				c.ok(strings.Contains(txt, want), "the summary lacks %q:\n%s", want, txt)
			}
			c.ok(!strings.Contains(txt, "Everything is up to date") && !strings.Contains(txt, "upgraded"), "the summary reads as an update:\n%s", txt)
			c.ok(p.sheet == nil && p.ctl.Sheet.State() == pkgview.SheetClosed, "the sheet is still up after the run")
			c.ok(p.ctl.List.Len() == 0, "the list kept %d entries after a good run", p.ctl.List.Len())
			c.ok(w.shell.title.Label() == "Packages", "the page title is %q", w.shell.title.Label())
			noEngineDialog(c, "the run")
			shoot(w, "cov-pkg-flow-repo", "2-summary")
		}, nil),
	)
	flowSeq(c, steps...)
}

// ---- a removal ----

func flowRemove(c *covEnv) {
	w := c.w
	c.quiet()
	p := showPackages(c)
	flowSeq(c,
		once(func() {
			vim, _ := p.w.opts.Packages.Catalog.Lookup("vim")
			c.ok(p.ctl.Now(vim, pkgview.ModeRemove) == nil, "Now(vim)")
		}, nil),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() {
			txt := dialogLabels(p.sheet, nil)
			c.ok(p.sheet.Heading() == "Remove 1 package", "heading %q", p.sheet.Heading())
			for _, want := range []string{"Frees 54.0 MiB", "Dependencies nothing else needs go with it."} {
				c.ok(strings.Contains(txt, want), "the sheet lacks %q:\n%s", want, txt)
			}
			shoot(w, "cov-pkg-flow-remove", "1-sheet")
			c.press(p.sheet, "yes")
		}, func() bool { return w.stack.VisibleChildName() == "run" }),
		until(runEnded(c)),
		once(func() {
			txt := summaryText(c)
			for _, want := range []string{"Packages removed", "2 packages removed"} {
				c.ok(strings.Contains(txt, want), "the summary lacks %q:\n%s", want, txt)
			}
			shoot(w, "cov-pkg-flow-remove", "2-summary")
		}, nil),
	)
}

// ---- an AUR install, with or without a high audit finding ----

func flowAUR(c *covEnv, term, name string, high bool) {
	w := c.w
	scenario := "cov-pkg-flow-aur"
	if high {
		scenario += "-high"
	}
	c.quiet()
	p := showPackages(c)
	steps := flowSearch(c, p, term)
	steps = append(steps,
		once(func() {
			c.ok(slices.Contains(rowNames(p.results), name), "the AUR hit %q is missing from %q", name, rowNames(p.results))
			p.results.Emit("row-activated", rowByName(p.results, name))
		}, nil),
		once(func() { btn(c, gtk.BaseWidget(p.root), "Install").Emit("clicked") }, func() bool { return p.sheet != nil }),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() {
			d := p.sheet
			txt := dialogLabels(d, nil)
			findings := "Audit findings · 1"
			if high {
				findings = "Audit findings · 2"
			}
			for _, want := range []string{"PKGBUILD review", "Not reviewed by Arch", "2 password prompts", "libgit2", name,
				"has an .install script", "I read the PKGBUILD", findings} {
				c.ok(strings.Contains(txt, want), "the AUR sheet lacks %q:\n%s", want, txt)
			}
			var text string
			eachWidget(&d.Widget, func(wd gtk.Widgetter) {
				if tv, ok := wd.(*gtk.TextView); ok {
					b := tv.Buffer()
					start, end := b.Bounds()
					text += b.Text(start, end, true) + "\n"
				}
			})
			for _, want := range []string{"pkgname=" + name, "fix.patch (NO CHECKSUM)", "-pkgver=2.0.3", "(not shown: binary)"} {
				c.ok(strings.Contains(text, want), "the review lacks %q:\n%s", want, text)
			}
			noEngineDialog(c, "the AUR sheet")
			wantChecks := 1
			if high {
				wantChecks = 2
				c.ok(strings.Contains(txt, "I've read the findings and accept them."), "no findings box:\n%s", txt)
				c.ok(strings.Contains(txt, "the maintainer changed since the last audit"), "the high finding is not shown:\n%s", txt)
			}
			if len(p.sheetChecks) != wantChecks {
				c.fail("%d boxes, want %d", len(p.sheetChecks), wantChecks)
				c.finish()
				return
			}
			c.ok(!p.sheetYes.Sensitive(), "Install is on before any box is ticked")
			shoot(w, scenario, "1-sheet")
			p.sheetChecks[0].SetActive(true)
		}, nil),
		until(func() bool { return !p.dirty }),
		once(func() {
			if high {
				c.ok(!p.sheetYes.Sensitive(), "Install is on with the findings unaccepted")
				shoot(w, scenario, "2-sheet-pkgbuild-ticked")
				p.sheetChecks[1].SetActive(true)
			}
		}, func() bool { return !p.dirty }),
		once(func() {
			c.ok(p.sheetYes.Sensitive(), "Install is off with every box ticked")
			shoot(w, scenario, "3-sheet-ready")
			c.press(p.sheet, "yes")
		}, func() bool { return w.stack.VisibleChildName() == "run" }),
		until(runEnded(c)),
		once(func() {
			txt := summaryText(c)
			for _, want := range []string{"Packages installed", "2 packages installed"} {
				c.ok(strings.Contains(txt, want), "the summary lacks %q:\n%s", want, txt)
			}
			c.ok(p.ctl.List.Len() == 0, "the list kept entries")
			shoot(w, scenario, "4-summary")
		}, nil),
	)
	flowSeq(c, steps...)
}

// ---- the helper refuses: nothing runs ----

func flowRefused(c *covEnv) {
	w := c.w
	c.quiet()
	p := showPackages(c)
	ask := func(name string) func() {
		return func() {
			pk := pkgdb.Package{Name: name, Version: "1-1", Repo: "extra", Description: "a demo package"}
			c.ok(p.ctl.Now(pk, pkgview.ModeInstall) == nil, "Now(%s)", name)
		}
	}
	close := func() {
		c.press(p.sheet, "no")
	}
	flowSeq(c,
		once(ask("conflict"), nil),
		until(sheetIs(p, pkgview.SheetRefused)),
		once(func() {
			d := p.sheet
			c.ok(d.Heading() == "It replaces an installed package", "heading %q", d.Heading())
			c.ok(strings.Contains(dialogLabels(d, nil), "gvim replaces vim"), "the helper's detail is missing:\n%s", dialogLabels(d, nil))
			c.ok(!w.busy && w.stack.VisibleChildName() == "packages", "busy %v page %q after a refusal", w.busy, w.stack.VisibleChildName())
			noEngineDialog(c, "a refusal")
			shoot(w, "cov-pkg-flow-refused", "1-conflict")
			close()
		}, func() bool { return p.sheet == nil }),
		once(func() { p.ctl.List.Clear(); ask("protected")() }, nil),
		until(sheetIs(p, pkgview.SheetRefused)),
		once(func() {
			c.ok(p.sheet.Heading() == "It would remove a protected package", "heading %q", p.sheet.Heading())
			c.ok(strings.Contains(dialogLabels(p.sheet, nil), "glibc is protected"), "protected detail:\n%s", dialogLabels(p.sheet, nil))
			close()
		}, func() bool { return p.sheet == nil }),
		once(func() { p.ctl.List.Clear(); ask("updates-pending")() }, nil),
		until(sheetIs(p, pkgview.SheetPending)),
		once(func() {
			d := p.sheet
			c.ok(d.Heading() == "Update the system first" && strings.Contains(dialogLabels(d, nil), "7 updates are waiting"), "pending sheet %q:\n%s", d.Heading(), dialogLabels(d, nil))
			c.ok(d.ResponseLabel("update") == "Update system first", "no Update system first button")
			shoot(w, "cov-pkg-flow-refused", "2-updates-pending")
			// Update system first starts topgrade on the window's Driver.
			c.press(d, "update")
		}, func() bool { return w.busy && w.current == "topgrade" }),
		// The stub's topgrade runs by itself to its end.
		until(runEnded(c)),
		once(func() {
			c.ok(w.stack.VisibleChildName() == "run", "topgrade's Run page did not come up: %q", w.stack.VisibleChildName())
		}, nil),
	)
}

// ---- Cancel at the sheet: a real no ----

func flowCancel(c *covEnv) {
	w := c.w
	c.quiet()
	p := showPackages(c)
	flowSeq(c,
		once(func() {
			vim, _ := p.w.opts.Packages.Catalog.Lookup("htop")
			c.ok(p.ctl.Now(vim, pkgview.ModeRemove) == nil, "Now")
		}, nil),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() { c.press(p.sheet, "no") }, func() bool { return !w.busy }),
		once(func() {
			c.ok(p.sheet == nil && p.ctl.Sheet.State() == pkgview.SheetClosed, "the sheet is still up")
			c.ok(w.stack.VisibleChildName() == "packages", "page %q after Cancel", w.stack.VisibleChildName())
			c.ok(p.ctl.List.Len() == 1, "Cancel emptied the list")
			c.ok(w.model.Finished == nil || w.model.Action != "packages", "a cancelled sheet is on the Run page")
			noEngineDialog(c, "Cancel")
		}, nil),
		// A second request works on a fresh Driver.
		once(func() { c.ok(p.ctl.Review() == nil, "Review") }, nil),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() { c.press(p.sheet, "no") }, func() bool { return !w.busy }),
	)
}

// ---- the plan went stale after the confirm ----

func flowStale(c *covEnv) {
	w := c.w
	c.quiet()
	p := showPackages(c)
	flowSeq(c,
		once(func() {
			pk := pkgdb.Package{Name: "stale", Version: "1-1", Repo: "extra"}
			c.ok(p.ctl.Now(pk, pkgview.ModeInstall) == nil, "Now")
		}, nil),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() { c.press(p.sheet, "yes") }, func() bool { return w.stack.VisibleChildName() == "run" }),
		// The helper found the list changed: the sheet comes back over the
		// Run page with "Review again".
		until(sheetIs(p, pkgview.SheetStale)),
		once(func() {
			d := p.sheet
			c.ok(d.Heading() == "The list changed" && d.ResponseLabel("retry") == "Review again", "stale sheet %q %q", d.Heading(), d.ResponseLabel("retry"))
			c.ok(p.ctl.List.Len() == 1, "the list was cleared by a failed run")
			shoot(w, "cov-pkg-flow-stale", "1-stale")
		}, func() bool { return !w.busy }),
		once(func() { c.press(p.sheet, "retry") }, nil),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() { c.press(p.sheet, "no") }, func() bool { return !w.busy }),
	)
}

// TestGUIPackagesFlow runs the page against the stub engine.
func TestGUIPackagesFlow(t *testing.T) {
	needDisplay(t)
	for _, s := range pkgFlowScenarios {
		t.Run(s, func(t *testing.T) { child(t, s) })
	}
}
