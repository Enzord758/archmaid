package ui

// The sidebar's selection always equals the visible page (the KDE check,
// #255): dragging the window under the narrow breakpoint and maximising it
// again left Packages highlighted while the header and the page said
// Overview.

import (
	"testing"
)

func init() { covScenarios["cov-navsync"] = covNavSync }

func covNavSync(c *covEnv) {
	w := c.w
	c.quiet()
	sh := w.shell
	synced := func(where string) {
		page := w.stack.VisibleChildName()
		row := sh.nav.SelectedRow()
		c.ok(row != nil && row.Name() == page, "%s: the sidebar selects %q, the page is %q", where, selName(row), page)
	}
	resize := func(wd int) func() { return func() { w.win.SetDefaultSize(wd, 640) } }
	c.seq(300,
		func() { synced("start") },
		resize(500),
		func() { synced("narrow") },
		resize(960),
		func() { synced("wide again") },
		// The selection drifting by itself (focus moving into the list as
		// the sidebar changes form selects the focused row) is put back.
		func() { sh.nav.SelectRow(sh.navRows["packages"]) },
		func() { synced("drifted to Packages") },
		resize(500),
		func() { sh.nav.SelectRow(sh.navRows["logs"]) },
		func() { synced("drifted while narrow") },
		resize(960),
		func() { synced("wide after drift") },
		// A real pick still changes the page, and the selection follows.
		func() { sh.nav.Emit("row-activated", sh.navRows["settings"]) },
		func() { synced("picked Settings") },
	)
}

func selName(r interface{ Name() string }) string {
	if r == nil {
		return "<none>"
	}
	return r.Name()
}

// TestGUINavSync: see covNavSync.
func TestGUINavSync(t *testing.T) {
	needDisplay(t)
	child(t, "cov-navsync")
}
