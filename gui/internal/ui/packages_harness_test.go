package ui

// The Packages page's display tests (task #234): each scenario is a small
// headless child (the cover scenarios' runChild) that drives the page
// through its real widgets with a fixture catalog, a fake AUR and a
// recording backend, on a fake debounce clock. Nothing reads pacman's
// databases, the network or the engine. With ARCHMAID_GUI_SHOTS every
// scenario writes the screens of the mockup it covers (A1..A7).

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	runreport "github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

var pkgFix *pkgEnv

// pkgScenarios are the page's scenarios: all use the fixture wiring.
var pkgScenarios = []string{"cov-pkg-search", "cov-pkg-installed", "cov-pkg-list", "cov-pkg-sheet",
	"cov-pkg-aur", "cov-pkg-gate", "cov-pkg-refused", "cov-pkg-theme", "cov-pkg-narrow", "cov-pkg-unavailable", "cov-pkg-noengine", "cov-pkg-live"}

const pkgNarrowEnv = "ARCHMAID_GUI_PKG_W"

// pkgWindowSize is the scenario's window: a wide one, or the narrow width.
func pkgWindowSize(scenario string) [2]int {
	switch {
	case scenario == "cov-pkg-narrow":
		w := 380
		if v, err := strconv.Atoi(os.Getenv(pkgNarrowEnv)); err == nil {
			w = v
		}
		return [2]int{w, 760}
	case strings.HasPrefix(scenario, "cov-pkg-"):
		return [2]int{1180, 760}
	}
	return [2]int{}
}

func init() {
	for _, s := range pkgScenarios {
		switch s {
		case "cov-pkg-unavailable":
			covOptions[s] = func(o *Options, _ *report) { o.Packages = nil }
		case "cov-pkg-noengine":
			covOptions[s] = func(o *Options, _ *report) { o.Packages = &PackagesWiring{Err: "pacman's database is missing"} }
		default:
			covOptions[s] = func(o *Options, _ *report) {
				pkgFix = newPkgEnv()
				if s == "cov-pkg-gate" {
					pkgFix.setUpdates(5)
				}
				o.Packages = pkgFix.wiring()
				if s == "cov-pkg-live" {
					pkgFix.liveWiring(o)
				}
			}
		}
	}
	covScenarios["cov-pkg-search"] = pkgSearch
	covScenarios["cov-pkg-installed"] = pkgInstalled
	covScenarios["cov-pkg-list"] = pkgList
	covScenarios["cov-pkg-sheet"] = pkgSheet
	covScenarios["cov-pkg-aur"] = pkgAUR
	covScenarios["cov-pkg-gate"] = pkgGate
	covScenarios["cov-pkg-refused"] = pkgRefused
	covScenarios["cov-pkg-theme"] = pkgTheme
	covScenarios["cov-pkg-narrow"] = pkgNarrow
	covScenarios["cov-pkg-unavailable"] = pkgUnavailable
	covScenarios["cov-pkg-noengine"] = pkgNoEngine
	covScenarios["cov-pkg-live"] = pkgLive
}

// ---- helpers ----

func pageLabels(p *pkgPage) []string { return labelsUnder(gtk.BaseWidget(p.root)) }

func pageText(p *pkgPage) string { return strings.Join(pageLabels(p), "\n") }

func has(texts []string, want string) bool { return slices.Contains(texts, want) }

func rowNames(lb *gtk.ListBox) []string {
	var out []string
	for i := 0; ; i++ {
		r := lb.RowAtIndex(i)
		if r == nil {
			return out
		}
		if r.Selectable() {
			out = append(out, r.Name())
		}
	}
}

func rowByName(lb *gtk.ListBox, name string) *gtk.ListBoxRow {
	for i := 0; ; i++ {
		r := lb.RowAtIndex(i)
		if r == nil {
			return nil
		}
		if r.Name() == name {
			return r
		}
	}
}

// noMarkup fails the scenario for any label under root that parses markup.
func noMarkup(c *covEnv, root *gtk.Widget, where string) {
	eachWidget(root, func(w gtk.Widgetter) {
		if l, ok := w.(*gtk.Label); ok && l.UseMarkup() {
			c.fail("%s: label %q parses markup", where, l.Text())
		}
	})
}

// btn is the button labelled label under root, failing when it is missing.
func btn(c *covEnv, root *gtk.Widget, label string) *gtk.Button {
	b := findButton(root, label)
	if b == nil {
		c.fail("no %q button", label)
		return gtk.NewButton()
	}
	return b
}

func (e *pkgEnv) typeQuery(p *pkgPage, q string) { p.entry.SetText(q) }

// search types q and lets both debounces run: 120 ms, then 500 ms.
func (e *pkgEnv) advance(ms int) { e.clock.Advance(time.Duration(ms) * time.Millisecond) }

func repoPlanFor(names ...string) steps.PkgPlan {
	pl := steps.PkgPlan{Targets: names, Hash: "sha256:" + strings.Repeat("c", 64)}
	for _, n := range names {
		pl.Rows = append(pl.Rows, steps.PkgRow{Name: n, Version: "133.0-1", Repo: "extra", Target: true, Download: 68 << 20, Installed: 260 << 20})
	}
	pl.Rows = append(pl.Rows,
		steps.PkgRow{Name: "libxt", Version: "1.3.1-1", Repo: "extra", Download: 120 << 10, Installed: 400 << 10},
		steps.PkgRow{Name: "dbus-glib", Version: "0.114-1", Repo: "extra", Download: 140 << 10, Installed: 500 << 10})
	return pl
}

func showPackages(c *covEnv) *pkgPage {
	c.w.stack.SetVisibleChildName("packages")
	return c.w.pkgs
}

// ---- A1, A2: search, results, detail ----

func pkgSearch(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	c.ok(w.navRowsHave("packages"), "the sidebar has no Packages row")
	pkgSeq(c,
		func() { // A1: the empty search
			txt := pageText(p)
			c.ok(p.listStack.VisibleChildName() == "empty", "stack shows %q, want the empty state", p.listStack.VisibleChildName())
			c.ok(strings.Contains(txt, "Search the packages"), "empty state: %q", txt)
			c.ok(strings.Contains(txt, "Package lists are 3 h old"), "no DB age in %q", txt)
			shoot(w, "cov-pkg-search", "a1-empty")
			// Typing, then typing again within 120 ms: one search.
			e.typeQuery(p, "fi")
			e.advance(100)
			e.typeQuery(p, "fir")
			e.advance(100)
			c.ok(e.aur.n == 0, "the AUR was searched before its 500 ms")
		},
		func() {
			c.ok(len(rowNames(p.results)) == 0 && e.cat != nil, "rows appeared before the debounce ended: %q", rowNames(p.results))
		},
		func() {
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() { // A2: repo first, then the AUR; the repo's own name is not repeated
			names := rowNames(p.results)
			want := []string{"firefox", "firefox-developer-edition", "firefox-i18n-da", "firefox-nightly-bin", "paru-bin"}
			c.ok(slices.Equal(names, want), "rows %q, want %q", names, want)
			txt := pageLabels(p)
			c.ok(has(txt, "REPOSITORIES · 3") || has(txt, "Repositories · 3"), "no Repositories header in %q", txt)
			c.ok(has(txt, "AUR · 2"), "no AUR header in %q", txt)
			c.ok(has(txt, markupDesc), "the AUR description was altered: markup must show as typed")
			c.ok(e.aur.n == 1, "AUR searched %d times for one settled term, want 1", e.aur.n)
			noMarkup(c, gtk.BaseWidget(p.root), "results")
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		},
		func() { // the detail: stats, dependencies with "pulled in", optional
			txt := pageText(p)
			for _, want := range []string{"Dependencies · 4", "Optional dependencies · 3", "68.0 MiB", "260 MiB", "hunspell-en_us", "Spell checking, American English", "Repository · extra"} {
				c.ok(strings.Contains(strings.ToUpper(txt), strings.ToUpper(want)), "detail lacks %q:\n%s", want, txt)
			}
			c.ok(has(pageLabels(p), "pulled in"), "libxt is not installed: it should be tagged pulled in")
			c.ok(has(pageLabels(p), "installed"), "gtk3 is installed: it should be tagged installed")
			dt := btn(c, gtk.BaseWidget(p.root), "Install")
			c.ok(dt.Sensitive(), "Install is off with no updates waiting")
			c.ok(btn(c, gtk.BaseWidget(p.root), "Add to list").Sensitive(), "Add to list is off")
			shoot(w, "cov-pkg-search", "a2-results-detail")
			p.results.Emit("row-activated", rowByName(p.results, "firefox-nightly-bin"))
		},
		func() { // an AUR package: the notice, maintainer, votes, flag
			txt := pageText(p)
			for _, want := range []string{pkgview.AURNotice, "somebody", "123", "Flagged out of date", "Last modified", "aur.archlinux.org/packages/firefox-nightly-bin", "unzip"} {
				c.ok(strings.Contains(strings.ToUpper(txt), strings.ToUpper(want)), "AUR detail lacks %q:\n%s", want, txt)
			}
			c.ok(has(pageLabels(p), "build"), "makedepends should be tagged build")
			noMarkup(c, gtk.BaseWidget(p.root), "AUR detail")
			shoot(w, "cov-pkg-search", "a2-aur-detail")
			p.results.Emit("row-activated", rowByName(p.results, "paru-bin"))
		},
		func() { // an orphaned AUR package and a description that looks like markup
			txt := pageText(p)
			c.ok(strings.Contains(txt, "orphaned"), "no orphaned maintainer in %q", txt)
			c.ok(has(pageLabels(p), markupDesc), "markup-looking description changed")
			// The AUR goes down: repo results stay, the section says so.
			e.aur.err = errAURDown
			e.typeQuery(p, "firef")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() {
			txt := pageLabels(p)
			c.ok(has(txt, "AUR unreachable"), "no AUR unreachable in %q", txt)
			c.ok(slices.Contains(rowNames(p.results), "firefox"), "repo results hidden by the AUR failure: %q", rowNames(p.results))
			shoot(w, "cov-pkg-search", "aur-unreachable")
			e.aur.err = nil
			btn(c, gtk.BaseWidget(p.root), "Try again").Emit("clicked")
		},
		func() {
			c.ok(slices.Contains(rowNames(p.results), "paru-bin"), "Try again did not bring the AUR back: %q", rowNames(p.results))
			e.aur.hits = nil
			e.typeQuery(p, "zzzzq")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() {
			c.ok(p.listStack.VisibleChildName() == "none", "no results shows %q", p.listStack.VisibleChildName())
			c.ok(has(pageLabels(p), `No packages match "zzzzq".`), "no-results text: %q", pageLabels(p))
			shoot(w, "cov-pkg-search", "no-results")
			// Backspacing to one character goes back to the empty state and cancels nothing it shouldn't.
			e.typeQuery(p, "z")
			e.advance(1000)
		},
		func() {
			c.ok(p.listStack.VisibleChildName() == "empty", "one character shows %q", p.listStack.VisibleChildName())
			// A1: "Recently installed", newest first, from the install dates.
			c.ok(has(pageLabels(p), "Recently installed"), "no Recently installed in %q", pageLabels(p))
			got := recentLines(p)
			want := []string{"yay 12.0-1 2 h ago", "htop 3.2.2-1 11 h ago", "vim 9.1.0-1 38 h ago", "gtk3 3.24-1 3 days ago", "libbar 1.4-2 6 days ago"}
			c.ok(slices.Equal(got, want), "recent %q, want %q", got, want)
			noMarkup(c, gtk.BaseWidget(p.root), "empty state")
			shoot(w, "cov-pkg-search", "a1-recent")
			recentButton(p, "gtk3").Emit("clicked")
		},
		func() {
			c.ok(p.selected == "gtk3", "a recent row selected %q", p.selected)
			c.ok(has(pageLabels(p), "GObject-based multi-platform GUI toolkit"), "the detail of the recent package is not shown")
		},
	)
}

// recentLines are the empty state's "Recently installed" rows as "name
// version ago".
func recentLines(p *pkgPage) []string {
	var out []string
	for ch := p.recent.FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
		if _, ok := ch.(*gtk.Button); ok {
			out = append(out, strings.Join(labelsUnder(gtk.BaseWidget(ch)), " "))
		}
	}
	return out
}

func recentButton(p *pkgPage, name string) *gtk.Button {
	for ch := p.recent.FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
		if b, ok := ch.(*gtk.Button); ok && strings.HasPrefix(strings.Join(labelsUnder(gtk.BaseWidget(ch)), " "), name+" ") {
			return b
		}
	}
	return gtk.NewButton()
}

// pkgSeq runs the steps one after another, 40 ms apart, each after the
// page has settled: no search or request on its way to the main loop and
// no redraw pending. A run that can't settle in 5 s fails.
func pkgSeq(c *covEnv, steps ...func()) {
	p := c.w.pkgs
	idle := func() bool {
		// A dialog is settled once its opening animation is over.
		return p.ctl == nil || (!p.ctl.Search.Busy() && !p.dirty && (p.sheet == nil || time.Since(p.sheetAt) > 700*time.Millisecond) && time.Since(p.navAt) > 500*time.Millisecond)
	}
	var run func(i int)
	run = func(i int) {
		if i == len(steps) {
			if p.ctl != nil {
				p.closeSheet() // win.Close would close a dialog instead of the window
			}
			c.finish()
			return
		}
		tries := 0
		coreglib.TimeoutAdd(120, func() bool {
			if !idle() {
				if tries++; tries > 125 {
					c.fail("step %d: the page never settled", i)
					c.finish()
					return false
				}
				return true
			}
			steps[i]()
			run(i + 1)
			return false
		})
	}
	run(0)
}

func (w *Window) navRowsHave(name string) bool { return w.shell.navRows[name] != nil }

// ---- A2b, A6: the installed view ----

func pkgInstalled(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			p.modeInstalled.SetActive(true)
		},
		func() { // A6: Explicit, with counts on the tabs
			var tabs []string
			for _, b := range p.viewBtns {
				tabs = append(tabs, b.Label())
			}
			c.ok(slices.Equal(tabs, []string{"Explicit 4", "Dependencies 2", "Orphans 1", "Foreign 1"}), "tabs %q", tabs)
			c.ok(slices.Equal(rowNames(p.instList), []string{"vim", "htop", "glibc", "yay"}), "rows %q", rowNames(p.instList))
			// glibc is protected: greyed, its button off, the reason as the tooltip.
			r := rowByName(p.instList, "glibc")
			c.ok(r != nil && gtk.BaseWidget(r.Child()).HasCSSClass("am-pkg-greyed"), "glibc is not greyed")
			c.ok(findTooltip(gtk.BaseWidget(r), pkgview.ProtectedReason) != nil, "glibc has no reason")
			c.ok(has(pageLabels(p), pkgview.ProtectedReason), "the reason is not shown: %q", pageLabels(p))
			c.ok(has(pageLabels(p), "update"), "htop has no update tag")
			shoot(w, "cov-pkg-installed", "a6-installed")
			p.instList.Emit("row-activated", rowByName(p.instList, "htop"))
		},
		func() { // A2b: an installed package with an update
			txt := pageText(p)
			c.ok(strings.Contains(txt, "An update is waiting. Updates come with the system."), "htop detail: %q", txt)
			c.ok(strings.Contains(txt, "3.2.2-1 → 3.3.0-1"), "no version change in %q", txt)
			rm := btn(c, gtk.BaseWidget(p.root), "Remove")
			c.ok(rm.Sensitive() && rm.HasCSSClass("destructive-action"), "Remove should be a working destructive button")
			shoot(w, "cov-pkg-installed", "a2b-installed-detail")
			p.instList.Emit("row-activated", rowByName(p.instList, "glibc"))
		},
		func() { // protected: Remove is off, with the reason
			c.ok(!btn(c, gtk.BaseWidget(p.root), "Remove").Sensitive(), "Remove works on a protected package")
			c.ok(has(pageLabels(p), pkgview.ProtectedReason), "no reason on the detail")
			shoot(w, "cov-pkg-installed", "protected")
			p.filter.SetText("vim")
		},
		func() {
			c.ok(slices.Equal(rowNames(p.instList), []string{"vim"}), "filter vim gave %q", rowNames(p.instList))
			p.filter.SetText("")
			p.viewBtns[2].SetActive(true)
		},
		func() { // orphans: not removable here, the banner points to Full Cleanup
			c.ok(slices.Equal(rowNames(p.instList), []string{"libfoo"}), "orphans %q", rowNames(p.instList))
			c.ok(p.orphanNote.IsVisible(), "no orphans banner")
			c.ok(has(pageLabels(p), "1 orphan. Full Cleanup removes them; they are listed here to look at."), "banner %q", pageLabels(p))
			c.ok(findTooltip(gtk.BaseWidget(rowByName(p.instList, "libfoo")), "Full Cleanup removes orphans.") != nil, "the orphan row's button has no reason")
			shoot(w, "cov-pkg-installed", "orphans")
			btn(c, gtk.BaseWidget(p.root), "Full Cleanup").Emit("clicked")
			c.ok(w.stack.VisibleChildName() == "overview", "the banner went to %q, want the Overview", w.stack.VisibleChildName())
			_ = e
		},
	)
}

// ---- A3: the list ----

func pkgList(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() {
			c.ok(!p.tray.IsVisible(), "the tray shows with an empty list")
			for _, n := range []string{"firefox", "firefox-developer-edition"} {
				b := findButton(gtk.BaseWidget(rowByName(p.results, n)), "")
				_ = b
				add := findTooltip(gtk.BaseWidget(rowByName(p.results, n)), "Add to the install list")
				if add == nil {
					c.fail("%s has no add button", n)
					continue
				}
				add.(*gtk.Button).Emit("clicked")
			}
		},
		func() { // the tray and the badge
			c.ok(p.tray.IsVisible(), "no tray")
			c.ok(p.trayKicker.Text() == "INSTALL LIST · 2", "kicker %q", p.trayKicker.Text())
			c.ok(p.trayNames.Text() == "firefox, firefox-developer-edition", "names %q", p.trayNames.Text())
			c.ok(p.trayNote.Text() == "Installs only. Removals are a separate run.", "note %q", p.trayNote.Text())
			c.ok(w.shell.pkgBadge.IsVisible() && w.shell.pkgBadge.Text() == "2", "badge %q visible %v", w.shell.pkgBadge.Text(), w.shell.pkgBadge.IsVisible())
			c.ok(!p.trayConfig.IsVisible(), "the config box shows on an install list")
			c.ok(p.trayReview.Label() == "Review and install", "review button %q", p.trayReview.Label())
			shoot(w, "cov-pkg-list", "a3-tray-install")
			// The added rows show the check, and a click takes them out.
			c.ok(findTooltip(gtk.BaseWidget(rowByName(p.results, "firefox")), "In the list. Click to take it out.") != nil, "firefox's button does not show it is in")
			p.modeInstalled.SetActive(true)
		},
		func() { // a removal can't join an install list: the buttons are off with the reason
			vim := rowByName(p.instList, "vim")
			why := "The list holds installs. Install and remove are separate runs: review or clear it first. Press to clear the list and add this."
			b := findTooltip(gtk.BaseWidget(vim), why)
			c.ok(b != nil && b.(*gtk.Button).Sensitive(), "vim's remove button should give the reason and offer to clear the list")
			// Clear empties it and the badge goes.
			p.trayClear.Emit("clicked")
		},
		func() {
			c.ok(!p.tray.IsVisible() && !w.shell.pkgBadge.IsVisible(), "tray %v badge %v after Clear", p.tray.IsVisible(), w.shell.pkgBadge.IsVisible())
			add := findTooltip(gtk.BaseWidget(rowByName(p.instList, "vim")), "Add to the removal list")
			c.ok(add != nil, "no remove button on vim")
			add.(*gtk.Button).Emit("clicked")
		},
		func() { // the removal list is red and has the config box
			c.ok(p.trayKicker.Text() == "REMOVAL LIST · 1", "kicker %q", p.trayKicker.Text())
			c.ok(p.tray.HasCSSClass("am-tray-remove") && p.trayKicker.HasCSSClass("am-tone-danger"), "the removal tray is not marked")
			c.ok(p.trayConfig.IsVisible() && p.trayReview.Label() == "Review and remove", "config box %v review %q", p.trayConfig.IsVisible(), p.trayReview.Label())
			c.ok(p.trayReview.HasCSSClass("destructive-action") && !p.trayReview.HasCSSClass("suggested-action"), "the removal review button is not destructive alone")
			c.ok(p.trayNote.Text() == "Removals only. Installs are a separate run.", "note %q", p.trayNote.Text())
			p.trayConfig.SetActive(true)
			c.ok(p.ctl.List.WithConfig, "ticking the box did not set -Rns")
			shoot(w, "cov-pkg-list", "a3-tray-remove")
			p.modeSearch.SetActive(true)
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() { // installs can't join a removal list
			why := "The list holds removals. Install and remove are separate runs: review or clear it first. Press to clear the list and add this."
			b := findTooltip(gtk.BaseWidget(rowByName(p.results, "firefox")), why)
			c.ok(b != nil && b.(*gtk.Button).Sensitive(), "the install button should give the reason and offer to clear the list over a removal list")
			// The review asks for a removal with the box: -Rns.
			p.trayReview.Emit("clicked")
		},
		func() {
			be := e.be
			be.mu.Lock()
			defer be.mu.Unlock()
			c.ok(len(be.started) == 1 && be.started[0].Mode == steps.PkgRemove && be.started[0].WithConfig && !be.started[0].AsDeps,
				"request %+v, want a removal with config files", be.started)
			c.ok(p.sheet != nil && p.sheet.Heading() == "Remove 1 package", "the sheet is up: %v", p.sheet != nil)
		},
	)
}

// ---- A4, A5, A5b: the sheet, the run, the summary ----

func pkgSheet(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			firefox, _ := e.cat.Lookup("firefox")
			if err := p.ctl.Now(firefox, pkgview.ModeInstall); err != nil {
				c.fail("Now: %v", err)
			}
		},
		func() { // planning
			c.ok(p.sheet != nil && p.sheet.Heading() == "Install 1 package", "sheet is up: %v", p.sheet != nil)
			if p.sheet == nil {
				return
			}
			c.ok(strings.Contains(dialogLabels(p.sheet, nil), "Checking the packages…"), "planning text: %q", dialogLabels(p.sheet, nil))
			c.ok(p.sheet.DefaultResponse() == "no", "default %q", p.sheet.DefaultResponse())
			c.ok(p.sheet.response("yes") == nil, "Install is offered while planning")
			p.ctl.Handle(pkgview.Preview{Prompt: 4, Plan: repoPlanFor("firefox")})
		},
		func() { // A4: the sheet
			d := p.sheet
			if d == nil {
				c.fail("no sheet in the review state")
				return
			}
			txt := dialogLabels(d, nil)
			for _, want := range []string{"Will be installed · 3", "firefox", "libxt", "dependency", "Before you start", "root", "asks",
				"1 password prompt.", "Package lists are 3 h old", "Download 68.3 MiB", "can't be stopped"} {
				c.ok(strings.Contains(txt, want), "sheet lacks %q:\n%s", want, txt)
			}
			c.ok(d.DefaultResponse() == "no" && d.ResponseLabel("no") == "Cancel", "Cancel must be the default")
			c.ok(d.ResponseAppearance("yes") == adw.ResponseSuggested && d.ResponseLabel("yes") == "Install", "yes is %q", d.ResponseLabel("yes"))
			noMarkup(c, &d.Widget, "sheet")
			shoot(w, "cov-pkg-sheet", "a4-sheet-repo")
			c.press(d, "yes")
		},
		func() {
			e.be.mu.Lock()
			c.ok(slices.Equal(e.be.answers, []string{"prompt 4 yes"}), "answers %q", e.be.answers)
			e.be.mu.Unlock()
			c.ok(p.sheet == nil, "the sheet stays up after Install")
			// The run starts: the Run page takes over.
			p.ctl.Handle(pkgview.RunStarted{})
			now := time.Now()
			c.feed(engine.QueueStarted{Action: "pkg-install", Started: now, Steps: []engine.PlanStep{{ID: steps.IDPkgInstall, Title: "Installing packages…", NeedsPriv: true, Verb: steps.IDPkgInstall}}},
				engine.StepStarted{ID: steps.IDPkgInstall, Index: 0, At: now},
				engine.OutputChunk{ID: steps.IDPkgInstall, Index: 0, Lines: []string{"resolving dependencies…", "(3/3) installing firefox", ":: Running post-transaction hooks…"}})
			c.ok(w.stack.VisibleChildName() == "run", "the Run page did not come up: %q", w.stack.VisibleChildName())
		},
		func() {
			shoot(w, "cov-pkg-sheet", "a5-run")
			rep := runreport.Report{Action: "pkg-install", Elapsed: 5 * time.Second, Steps: runreport.StepTally{Done: 1}, Packages: &runreport.Packages{
				Installed: []runreport.Pkg{{Name: "firefox", New: "133.0-1"}, {Name: "libxt", New: "1.3.1-1"}, {Name: "dbus-glib", New: "0.114-1"}}}}
			c.feed(engine.StepDone{ID: steps.IDPkgInstall, Index: 0, Status: steps.OK, Elapsed: 4 * time.Second},
				engine.RunReport{Report: rep},
				engine.Finished{Action: "pkg-install", Outcome: engine.OutcomeCompleted, Elapsed: 5 * time.Second})
			p.ctl.Handle(pkgview.Done{OK: true})
		},
		func() { // a successful run empties the list
			c.ok(p.ctl.List.Len() == 0, "the list kept %d entries after a good run", p.ctl.List.Len())
			shoot(w, "cov-pkg-sheet", "a5b-summary")
			// Cancel is a real answer: no.
			firefox, _ := e.cat.Lookup("firefox")
			w.stack.SetVisibleChildName("packages")
			_ = p.ctl.Now(firefox, pkgview.ModeInstall)
		},
		func() {
			p.ctl.Handle(pkgview.Preview{Prompt: 5, Plan: repoPlanFor("firefox")})
		},
		func() {
			if p.sheet == nil {
				c.fail("no second sheet")
				return
			}
			c.press(p.sheet, "no")
		},
		func() {
			e.be.mu.Lock()
			defer e.be.mu.Unlock()
			c.ok(slices.Contains(e.be.answers, "prompt 5 no"), "Cancel did not answer no: %q", e.be.answers)
			c.ok(p.ctl.List.Len() == 1, "Cancel emptied the list")
		},
	)
}

// ---- A4 for the AUR ----

func pkgAUR(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			paru, _ := e.aur.info["paru-bin"]
			c.ok(paru.Name != "", "fixture")
			if err := p.ctl.Now(paru, pkgview.ModeInstall); err != nil {
				c.fail("Now: %v", err)
			}
		},
		func() {
			plan := steps.PkgPlan{Targets: []string{"paru-bin"}, Hash: "sha256:" + strings.Repeat("d", 64), Rows: []steps.PkgRow{
				{Name: "libgit2", Version: "1.8.4-1", Repo: "extra", Target: true, Download: 600 << 10, Installed: 3 << 20}},
				AUR: []steps.AURPlan{{Base: "paru-bin", Names: []string{"paru-bin"}, Version: "2.0.4-1", Commit: "0123456789abcdef", Prev: "fedcba9876543210",
					Files: []steps.AURFile{
						{Path: "PKGBUILD", Kind: steps.AURFilePKGBUILD, Size: 300, Text: "pkgname=paru-bin\npkgver=2.0.4\nsource=(\"https://example.org/<b>paru</b>.tar.gz\")\n"},
						{Path: "paru.install", Kind: steps.AURFileInstall, Size: 80, Text: "post_install() { echo done; }\n"},
						{Path: "logo.png", Kind: steps.AURFileOther, Size: 9000}},
					Sources: []steps.AURSource{{URL: "https://example.org/<b>paru</b>.tar.gz", Checksum: true}, {URL: "http://example.org/extra.patch"}},
					Diff:    "@@ -1,2 +1,2 @@\n-pkgver=2.0.3\n+pkgver=2.0.4\n",
					Flags:   []string{"has an .install script: it runs as root at install"},
					Audit:   []steps.AURFinding{{Name: "paru-bin", High: true, Reason: "maintainer changed since the last build"}, {Name: "paru-bin", Reason: "no checksums"}}}}}
			p.ctl.Handle(pkgview.Preview{Prompt: 2, Plan: plan})
		},
		func() {
			d := p.sheet
			if d == nil {
				c.fail("no AUR sheet")
				return
			}
			txt := dialogLabels(d, nil)
			for _, want := range []string{"PKGBUILD review", "up to twice", "Not reviewed by Arch", "Audit findings · 2", "high", "maintainer changed since the last build",
				"has an .install script", "I read the PKGBUILD", "I've read the findings and accept them."} {
				c.ok(strings.Contains(txt, want), "AUR sheet lacks %q:\n%s", want, txt)
			}
			var text string
			eachWidget(&d.Widget, func(wd gtk.Widgetter) {
				if tv, ok := wd.(*gtk.TextView); ok {
					b := tv.Buffer()
					start, end := b.Bounds()
					text += b.Text(start, end, true) + "\n"
				}
			})
			c.ok(strings.Contains(text, "pkgname=paru-bin") && strings.Contains(text, "<b>paru</b>"), "the PKGBUILD text is wrong or was parsed as markup: %q", text)
			c.ok(strings.Contains(text, "-pkgver=2.0.3"), "no diff text: %q", text)
			c.ok(strings.Contains(text, "(not shown: binary or too large)"), "binary file not marked")
			c.ok(strings.Contains(text, "http://example.org/extra.patch (NO CHECKSUM)") && strings.Contains(text, "<b>paru</b>.tar.gz (checksum)"), "sources missing: %q", text)
			noMarkup(c, &d.Widget, "AUR sheet")
			c.ok(p.sheetYes != nil && !p.sheetYes.Sensitive(), "Install is on before the boxes are ticked")
			c.ok(len(p.sheetChecks) == 2, "checks %d", len(p.sheetChecks))
			// The two columns keep their widths with long notes and versions:
			// the notes 330 px, the plan the rest and at least planMin.
			eachWidget(&d.Widget, func(wd gtk.Widgetter) {
				b := gtk.BaseWidget(wd)
				if !b.HasCSSClass("am-columns") {
					return
				}
				plan, notes := gtk.BaseWidget(b.FirstChild()).Width(), gtk.BaseWidget(b.LastChild()).Width()
				c.ok(notes == notesColumn && plan >= planMin, "columns: plan %d px, notes %d px; want notes %d and plan at least %d", plan, notes, notesColumn, planMin)
			})
			shoot(w, "cov-pkg-aur", "a4-sheet-aur")
			p.sheetChecks[0].SetActive(true)
		},
		func() {
			c.ok(!p.sheetYes.Sensitive(), "Install is on with the audit finding unaccepted")
			p.sheetChecks[1].SetActive(true)
		},
		func() {
			c.ok(p.sheetYes.Sensitive(), "Install is off with both boxes ticked")
			shoot(w, "cov-pkg-aur", "a4-sheet-aur-ticked")
			c.press(p.sheet, "yes")
		},
		func() {
			e.be.mu.Lock()
			defer e.be.mu.Unlock()
			c.ok(slices.Equal(e.be.answers, []string{"prompt 2 yes"}), "answers %q", e.be.answers)
		},
	)
}

// ---- updates pending ----

func pkgGate(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			c.ok(p.banner.Revealed(), "no updates banner")
			c.ok(strings.HasPrefix(p.banner.Title(), "5 updates are waiting"), "banner %q", p.banner.Title())
			e.typeQuery(p, "gvim")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() { // A2 with the block: Install off, Update system first beside it
			p.results.Emit("row-activated", rowByName(p.results, "gvim"))
		},
		func() {
			c.ok(!btn(c, gtk.BaseWidget(p.root), "Install").Sensitive(), "Install works with updates waiting")
			c.ok(btn(c, gtk.BaseWidget(p.root), "Add to list").Sensitive(), "Add to list should still work: the list is kept")
			c.ok(has(pageLabels(p), "5 updates are waiting. Update the system before installing."), "no inline note: %q", pageLabels(p))
			shoot(w, "cov-pkg-gate", "updates-pending")
			btn(c, gtk.BaseWidget(p.root), "Add to list").Emit("clicked")
		},
		func() {
			c.ok(p.tray.IsVisible() && !p.trayReview.Sensitive(), "the tray's review must be off")
			c.ok(p.trayUpdate.IsVisible(), "no Update system first on the tray")
			e.be.mu.Lock()
			c.ok(len(e.be.started) == 0, "a blocked install reached the engine")
			e.be.mu.Unlock()
			// Update system first starts the existing topgrade action.
			p.banner.Emit("button-clicked")
		},
		func() {
			// The stub has no topgrade script and says so: that the status
			// names topgrade shows the existing action was started.
			c.ok(w.current == actions.IDTopgrade, "current %q: Update system first must start topgrade", w.current)
			c.ok(strings.Contains(w.statusText(), "topgrade"), "status %q", w.statusText())
			c.ok(p.ctl.List.Len() == 1, "the list was dropped")
		},
		func() {
			// The updates run is over: the cache has the new state.
			e.clearUpdates()
			w.stack.SetVisibleChildName("packages")
			p.ctl.RefreshUpdates()
		},
		func() {
			c.ok(!p.banner.Revealed(), "the banner stays after the cache is clean")
			c.ok(p.trayReview.Sensitive(), "Review stays off after the update")
			c.ok(p.ctl.List.Len() == 1, "the list was lost")
			shoot(w, "cov-pkg-gate", "updates-done")
		},
	)
}

// ---- refused, stale, protected, updates from the helper ----

func pkgRefused(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	gvim, _ := e.cat.Lookup("gvim")
	pkgSeq(c,
		func() { _ = p.ctl.Now(gvim, pkgview.ModeInstall) },
		func() {
			p.ctl.Handle(pkgview.Refused{Refusal: &steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}}})
			p.ctl.Handle(pkgview.Done{})
		},
		func() { // a replacement is refused, with the reason, and nothing offers to go on
			d := p.sheet
			if d == nil {
				c.fail("no refusal sheet")
				return
			}
			c.ok(d.Heading() == "It replaces an installed package", "heading %q", d.Heading())
			txt := dialogLabels(d, nil)
			c.ok(strings.Contains(txt, "Remove the old package first") && strings.Contains(txt, "vim"), "text %q", txt)
			c.ok(d.response("yes") == nil, "a refusal offers Install")
			c.ok(d.ResponseLabel("no") == "Close", "close label %q", d.ResponseLabel("no"))
			shoot(w, "cov-pkg-refused", "a4-refused")
			c.press(d, "no")
		},
		func() {
			c.ok(p.sheet == nil && p.ctl.List.Len() == 1, "sheet %v list %d", p.sheet, p.ctl.List.Len())
			// The list holds an install: Remove on vim is off with the reason.
			vim, _ := e.cat.Lookup("vim")
			p.selectPkg(vim)
		},
		func() {
			c.ok(!btn(c, gtk.BaseWidget(p.root), "Remove").Sensitive(), "Remove works next to an install list")
			c.ok(has(pageLabels(p), "The list holds installs. Install and remove are separate runs: review or clear it first."), "no reason on the detail: %q", pageLabels(p))
			p.ctl.List.Clear()
			// The helper protects htop: the page learns it.
			htop, _ := e.cat.Lookup("htop")
			_ = p.ctl.Now(htop, pkgview.ModeRemove)
		},
		func() {
			p.ctl.Handle(pkgview.Refused{Refusal: &steps.PkgRefusal{Reason: steps.RefuseProtected, Names: []string{"htop"}, Detail: "Needed to boot."}})
			p.ctl.Handle(pkgview.Done{})
		},
		func() {
			c.ok(p.sheet != nil && p.sheet.Heading() == "It would remove a protected package", "sheet is up: %v", p.sheet != nil)
			c.press(p.sheet, "no")
			p.ctl.List.Clear()
			p.modeInstalled.SetActive(true)
		},
		func() {
			r := rowByName(p.instList, "htop")
			c.ok(r != nil && gtk.BaseWidget(r.Child()).HasCSSClass("am-pkg-greyed"), "htop is not greyed after the helper protected it")
			c.ok(has(pageLabels(p), "Needed to boot."), "the helper's reason is not shown")
			p.modeSearch.SetActive(true)
			firefox, _ := e.cat.Lookup("firefox")
			_ = p.ctl.Now(firefox, pkgview.ModeInstall)
		},
		func() { p.ctl.Handle(pkgview.Preview{Prompt: 7, Plan: repoPlanFor("firefox")}) },
		func() { c.press(p.sheet, "yes") },
		func() { // the list changed under the confirm: stale
			e.be.mu.Lock()
			req := e.be.started[len(e.be.started)-1]
			e.be.mu.Unlock()
			p.ctl.Handle(pkgview.Stale{Req: req, Plan: repoPlanFor("firefox")})
			p.ctl.Handle(pkgview.Done{})
		},
		func() {
			d := p.sheet
			if d == nil {
				c.fail("no stale sheet")
				return
			}
			c.ok(d.Heading() == "The list changed" && d.ResponseLabel("retry") == "Review again", "heading %q retry %q", d.Heading(), d.ResponseLabel("retry"))
			c.ok(strings.Contains(dialogLabels(d, nil), "Nothing was installed or removed"), "text %q", dialogLabels(d, nil))
			shoot(w, "cov-pkg-refused", "stale")
			e.be.mu.Lock()
			before := len(e.be.started)
			e.be.mu.Unlock()
			c.press(d, "retry")
			c.ok(before >= 1, "")
		},
		func() {
			e.be.mu.Lock()
			n := len(e.be.started)
			e.be.mu.Unlock()
			c.ok(n == 4, "Review again made %d requests in all, want 4", n)
			c.ok(p.sheet != nil && strings.Contains(dialogLabels(p.sheet, nil), "Checking the packages…"), "no new planning sheet")
			// The helper says updates are waiting.
			p.ctl.Handle(pkgview.UpdatesPending{Count: 3})
			p.ctl.Handle(pkgview.Done{})
		},
		func() {
			d := p.sheet
			c.ok(d != nil && d.Heading() == "Update the system first" && d.ResponseLabel("update") == "Update system first", "sheet is up: %v", d != nil)
			shoot(w, "cov-pkg-refused", "updates-pending-sheet")
			c.press(p.sheet, "no")
		},
	)
}

// ---- light and dark ----

func pkgTheme(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	sm := adw.StyleManagerGetDefault()
	pkgSeq(c,
		func() {
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() {
			e.advance(400)
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		},
		func() {
			sm.SetColorScheme(adw.ColorSchemeForceLight)
		},
		func() {
			c.ok(!sm.Dark(), "not light")
			shoot(w, "cov-pkg-theme", "a2-light")
			firefox, _ := e.cat.Lookup("firefox")
			_ = p.ctl.Now(firefox, pkgview.ModeInstall)
		},
		func() { p.ctl.Handle(pkgview.Preview{Prompt: 1, Plan: repoPlanFor("firefox")}) },
		func() {
			shoot(w, "cov-pkg-theme", "a4-light")
			c.press(p.sheet, "no")
			sm.SetColorScheme(adw.ColorSchemeForceDark)
		},
		func() {
			c.ok(sm.Dark(), "not dark")
			shoot(w, "cov-pkg-theme", "a2-dark")
			firefox, _ := e.cat.Lookup("firefox")
			_ = p.ctl.Now(firefox, pkgview.ModeInstall)
		},
		func() { p.ctl.Handle(pkgview.Preview{Prompt: 2, Plan: repoPlanFor("firefox")}) },
		func() {
			shoot(w, "cov-pkg-theme", "a4-dark")
			c.press(p.sheet, "no")
			sm.SetColorScheme(adw.ColorSchemeDefault)
		},
	)
}

// ---- A7: narrow ----

func pkgNarrow(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	width := pkgWindowSize("cov-pkg-narrow")[0]
	pkgSeq(c,
		func() {
			c.ok(w.narrow && p.split.Collapsed() && p.back.IsVisible(), "narrow %v collapsed %v back %v", w.narrow, p.split.Collapsed(), p.back.IsVisible())
			// The page asks for no more than the window has.
			minW, _, _, _ := gtk.BaseWidget(p.root).Measure(gtk.OrientationHorizontal, -1)
			c.ok(minW <= width, "the page's minimum width is %d, window %d", minW, width)
			c.ok(w.win.Width() <= width+2, "window %d wide, asked for %d", w.win.Width(), width)
			e.typeQuery(p, "firefox")
			e.advance(120)
		},
		func() { e.advance(400) },
		func() { // A7: results in one column, tags under the names
			minW, _, _, _ := gtk.BaseWidget(p.root).Measure(gtk.OrientationHorizontal, -1)
			c.ok(minW <= width, "results: minimum width %d, window %d", minW, width)
			c.ok(!p.split.ShowContent(), "the detail shows before a pick")
			shoot(w, "cov-pkg-narrow", fmt.Sprintf("a7-results-%d", width))
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		},
		func() { // the detail pushed on top
			c.ok(p.split.ShowContent(), "the detail was not pushed")
			minW, _, _, _ := gtk.BaseWidget(p.root).Measure(gtk.OrientationHorizontal, -1)
			c.ok(minW <= width, "detail: minimum width %d, window %d", minW, width)
			shoot(w, "cov-pkg-narrow", fmt.Sprintf("a7-detail-%d", width))
			btn(c, gtk.BaseWidget(p.root), "Add to list").Emit("clicked")
		},
		func() {
			c.ok(p.tray.IsVisible(), "no tray")
			minW, _, _, _ := gtk.BaseWidget(p.root).Measure(gtk.OrientationHorizontal, -1)
			c.ok(minW <= width, "tray: minimum width %d, window %d", minW, width)
			shoot(w, "cov-pkg-narrow", fmt.Sprintf("a7-tray-%d", width))
			p.back.Emit("clicked")
			c.ok(!p.split.ShowContent(), "Back did not return to the list")
			firefox, _ := e.cat.Lookup("firefox")
			_ = p.ctl.Review()
			_ = firefox
		},
		func() { p.ctl.Handle(pkgview.Preview{Prompt: 1, Plan: repoPlanFor("firefox")}) },
		func() { // the sheet as a bottom sheet, its columns stacked
			d := p.sheet
			if d == nil {
				c.fail("no sheet")
				return
			}
			c.ok(strings.Contains(dialogLabels(d, nil), "Before you start"), "narrow sheet: %q", dialogLabels(d, nil))
			shoot(w, "cov-pkg-narrow", fmt.Sprintf("a7-sheet-%d", width))
			c.press(d, "no")
		},
	)
}

// ---- no engine, no database ----

func pkgUnavailable(c *covEnv) {
	c.quiet()
	p := showPackages(c)
	pkgSeq(c, func() {
		c.ok(p.ctl == nil, "a controller without wiring")
		c.ok(has(pageLabels(p), "Packages are not available in this build"), "text %q", pageLabels(p))
		c.ok(c.w.shell.pkgBadge != nil && !c.w.shell.pkgBadge.IsVisible(), "badge")
		shoot(c.w, "cov-pkg-unavailable", "unavailable")
	})
}

func pkgNoEngine(c *covEnv) {
	c.quiet()
	p := showPackages(c)
	pkgSeq(c, func() {
		c.ok(has(pageLabels(p), "The package lists couldn't be read"), "text %q", pageLabels(p))
		c.ok(has(pageLabels(p), "pacman's database is missing"), "reason missing: %q", pageLabels(p))
		shoot(c.w, "cov-pkg-noengine", "search-error")
	})
}

// TestGUIPackages runs the page's scenarios. ARCHMAID_GUI_SHOTS adds the
// screenshots of mockup screens A1..A7 (build with -tags shots).
func TestGUIPackages(t *testing.T) {
	needDisplay(t)
	for _, s := range pkgScenarios {
		t.Run(s, func(t *testing.T) {
			if s == "cov-pkg-narrow" {
				for _, wd := range []string{"380", "360"} {
					t.Run(wd, func(t *testing.T) { child(t, s, pkgNarrowEnv+"="+wd) })
				}
				return
			}
			child(t, s)
		})
	}
}

var _ = pkgdb.RepoAUR
