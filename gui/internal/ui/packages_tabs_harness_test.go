package ui

// Two Packages page findings from the VM (#266): the Installed tab kept the
// Search tab's package in the detail pane, and the status line stayed at
// "Checking the packages…" while the review sheet was open.

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

func init() {
	for _, s := range []string{"cov-pkg-tabs", "cov-pkg-status"} {
		covOptions[s] = func(o *Options, _ *report) {
			pkgFix = newPkgEnv()
			o.Packages = pkgFix.wiring()
		}
	}
	covScenarios["cov-pkg-tabs"] = pkgTabs
	covScenarios["cov-pkg-status"] = pkgStatus
}

func pkgTabs(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	shows := func(name string) bool { return strings.Contains(pageText(p), name) }
	pkgSeq(c,
		func() { e.typeQuery(p, "firefox"); e.advance(120) },
		func() {
			e.advance(400)
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		},
		func() {
			c.ok(p.detailStack.VisibleChildName() == "detail" && shows("Install"), "the Search tab's detail: %q", p.detailStack.VisibleChildName())
			p.modeInstalled.SetActive(true)
		},
		func() {
			// The Installed tab has picked nothing: "Pick a package", not the
			// Search tab's firefox with its Install button.
			c.ok(p.detailStack.VisibleChildName() == "none", "the Installed tab shows %q, want the empty pane", p.detailStack.VisibleChildName())
			c.ok(!p.ctl.Search.Detail().Valid, "the Installed tab still holds the Search tab's package")
			shoot(w, "cov-pkg-tabs", "installed-empty")
			p.instList.Emit("row-activated", rowByName(p.instList, "htop"))
		},
		func() {
			c.ok(p.detailStack.VisibleChildName() == "detail" && shows("htop"), "the Installed tab's own pick: %q", p.detailStack.VisibleChildName())
			p.modeSearch.SetActive(true)
		},
		func() {
			d := p.ctl.Search.Detail()
			c.ok(d.Valid && d.Pkg.Name == "firefox", "back on Search the detail holds %q (valid %v), stack %q", d.Pkg.Name, d.Valid, p.detailStack.VisibleChildName())
			p.modeInstalled.SetActive(true)
		},
		func() {
			d := p.ctl.Search.Detail()
			c.ok(d.Valid && d.Pkg.Name == "htop", "back on Installed the detail holds %q (valid %v)", d.Pkg.Name, d.Valid)
			shoot(w, "cov-pkg-tabs", "installed-own-pick")
			p.modeSearch.SetActive(true)
		},
	)
}

func pkgStatus(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	p := showPackages(c)
	pkgSeq(c,
		func() {
			firefox, _ := e.cat.Lookup("firefox")
			// The engine says it is checking, then the sheet opens.
			w.current, w.busy = actions.IDPackages, true
			c.feed(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Checking the packages…"})
			c.ok(strings.Contains(w.status.Text(), "Checking the packages"), "while checking the status is %q", w.status.Text())
			_ = p.ctl.Now(firefox, pkgview.ModeInstall)
		},
		func() { p.ctl.Handle(pkgview.Preview{Prompt: 1, Plan: repoPlanFor("firefox")}) },
		func() {
			c.ok(p.sheet != nil, "no sheet")
			c.ok(!strings.Contains(w.status.Text(), "Checking the packages"), "with the sheet open the status still says %q", w.status.Text())
			c.ok(strings.Contains(w.status.Text(), "Waiting"), "with the sheet open the status is %q, want Waiting…", w.status.Text())
			c.press(p.sheet, "no")
			w.busy = false // the window can close
			w.current = ""
		},
	)
}

// TestGUIPackagesTabsAndStatus runs both.
func TestGUIPackagesTabsAndStatus(t *testing.T) {
	needDisplay(t)
	child(t, "cov-pkg-tabs")
	child(t, "cov-pkg-status")
}
