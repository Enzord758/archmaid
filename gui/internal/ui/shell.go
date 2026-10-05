package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
)

// The Workbench shell (P8 row 3; spec 1.1, 1.8, 3): a sidebar with the
// four pages, the meters (row 9) and the status footer, beside the content
// (a header with the page's title and Stop, the stub's banner, the page).
// Below 760sp wide the sidebar collapses into an overlay opened from the
// header, and the status moves to a bar under the content.

// navPage is one sidebar entry: a page of the view stack.
type navPage struct {
	name, title, icon string
}

// navPages are the stack's pages, in the sidebar's order (the harness
// uses the names).
var navPages = []navPage{
	{"overview", "Overview", "go-home-symbolic"},
	{"run", "Run", "media-playback-start-symbolic"},
	{"logs", "Logs", "document-open-recent-symbolic"},
	{"settings", "Settings", "emblem-system-symbolic"},
}

// narrowWidth is where the sidebar collapses (spec 1.8).
const narrowWidth = "max-width: 760sp"

// shell is the window's frame around the pages.
type shell struct {
	meters    *meters
	split     *adw.OverlaySplitView
	nav       *gtk.ListBox
	navRows   map[string]*gtk.ListBoxRow
	runBadge  *gtk.Label
	title     *gtk.Label
	toggle    *gtk.ToggleButton
	statusBar *gtk.Label // the narrow form's status, under the content
	syncing   bool       // the nav's selection follows the stack, not the user
}

// buildShell builds the shell around w.stack (its pages added) and sets it
// as the window's content, under the toast overlay.
func (w *Window) buildShell() {
	sh := &shell{navRows: map[string]*gtk.ListBoxRow{}}
	w.shell = sh

	// The sidebar: brand, pages, (meters,) status.
	brand := gtk.NewLabel("ARCHMAID")
	brand.SetXAlign(0)
	brand.AddCSSClass("am-brand")
	sh.nav = gtk.NewListBox()
	sh.nav.AddCSSClass("navigation-sidebar")
	sh.nav.SetSelectionMode(gtk.SelectionBrowse)
	for _, p := range navPages {
		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		box.Append(gtk.NewImageFromIconName(p.icon))
		l := gtk.NewLabel(p.title)
		l.SetXAlign(0)
		l.SetHExpand(true)
		box.Append(l)
		if p.name == "run" {
			sh.runBadge = gtk.NewLabel("")
			sh.runBadge.AddCSSClass("am-badge")
			sh.runBadge.AddCSSClass("numeric")
			sh.runBadge.SetVisible(false)
			box.Append(sh.runBadge)
		}
		row := gtk.NewListBoxRow()
		row.SetChild(box)
		row.SetName(p.name)
		sh.navRows[p.name] = row
		sh.nav.Append(row)
	}
	sh.nav.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil || sh.syncing {
			return
		}
		w.stack.SetVisibleChildName(row.Name())
		if sh.split.Collapsed() {
			sh.split.SetShowSidebar(false)
		}
	})
	w.status.SetVExpand(false)
	w.status.SetWrap(true)
	w.status.AddCSSClass("am-footer")
	spacer := gtk.NewBox(gtk.OrientationVertical, 0)
	spacer.SetVExpand(true)
	side := gtk.NewBox(gtk.OrientationVertical, 6)
	side.AddCSSClass("am-sidebar")
	side.Append(brand)
	side.Append(sh.nav)
	side.Append(spacer)
	sh.meters = newMeters()
	side.Append(sh.meters.box)
	side.Append(w.status)

	// The content: header (sidebar toggle, title, Stop), the stub's
	// banner, the page, and the narrow form's status bar.
	sh.title = gtk.NewLabel("")
	sh.title.AddCSSClass("title")
	sh.title.SetEllipsize(0) // a title is short; never cut it
	sh.toggle = gtk.NewToggleButton()
	sh.toggle.SetIconName("sidebar-show-symbolic")
	sh.toggle.SetTooltipText("Show the pages")
	sh.toggle.SetVisible(false)
	header := adw.NewHeaderBar()
	header.SetTitleWidget(sh.title)
	header.PackStart(sh.toggle)
	header.PackEnd(w.stopBtn)
	view := adw.NewToolbarView()
	view.AddTopBar(header)
	if w.opts.Stub {
		// Always up: a demo must never pass for the real thing.
		w.banner = adw.NewBanner(StubBanner)
		w.banner.SetRevealed(true)
		view.AddTopBar(w.banner)
	}
	view.SetContent(w.stack)
	sh.statusBar = gtk.NewLabel(w.statusText())
	sh.statusBar.SetXAlign(0)
	sh.statusBar.SetWrap(true)
	sh.statusBar.AddCSSClass("am-footer")
	sh.statusBar.SetMarginStart(12)
	sh.statusBar.SetVisible(false)
	view.AddBottomBar(sh.statusBar)

	sh.split = adw.NewOverlaySplitView()
	sh.split.SetMinSidebarWidth(220)
	sh.split.SetMaxSidebarWidth(248)
	sh.split.SetSidebar(side)
	sh.split.SetContent(view)
	sh.toggle.ConnectToggled(func() { sh.split.SetShowSidebar(sh.toggle.Active()) })
	sh.split.NotifyProperty("show-sidebar", func() {
		if sh.toggle.Active() != sh.split.ShowSidebar() {
			sh.toggle.SetActive(sh.split.ShowSidebar())
		}
	})

	// Only one breakpoint applies at a time (the last added that
	// matches), so narrow-and-short has the setters of both.
	narrowSetters := func(bp *adw.Breakpoint) {
		bp.AddSetter(sh.split, "collapsed", true)
		bp.AddSetter(sh.toggle, "visible", true)
		bp.AddSetter(sh.statusBar, "visible", true)
	}
	shortSetters := func(bp *adw.Breakpoint) {
		bp.AddSetter(sh.meters.box, "visible", false)
	}
	narrow := adw.NewBreakpoint(adw.BreakpointConditionParse(narrowWidth))
	narrowSetters(narrow)
	short := adw.NewBreakpoint(adw.BreakpointConditionParse(shortHeight))
	shortSetters(short)
	both := adw.NewBreakpoint(adw.BreakpointConditionParse(narrowWidth + " and " + shortHeight))
	narrowSetters(both)
	shortSetters(both)
	w.win.AddBreakpoint(narrow)
	w.win.AddBreakpoint(short)
	w.win.AddBreakpoint(both)

	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(sh.split)
	w.win.SetContent(w.toasts)

	w.stack.NotifyProperty("visible-child-name", w.pageShown)
	w.pageShown()
}

// pageShown follows the stack's page: the sidebar's selection and the
// header's title.
func (w *Window) pageShown() {
	sh := w.shell
	name := w.stack.VisibleChildName()
	if row := sh.navRows[name]; row != nil && sh.nav.SelectedRow() != row {
		sh.syncing = true
		sh.nav.SelectRow(row)
		sh.syncing = false
	}
	w.updateTitle()
}

// updateTitle is the header's title: the page's, or on the Run page the
// action's name.
func (w *Window) updateTitle() {
	name := w.stack.VisibleChildName()
	title := ""
	for _, p := range navPages {
		if p.name == name {
			title = p.title
		}
	}
	if name == "run" && w.current != "" {
		title = overview.Label(w.current)
	}
	w.shell.title.SetLabel(title)
}

// setStatus shows the status line in the sidebar's footer and, in the
// narrow form, under the content.
func (w *Window) setStatus() {
	s := w.statusText()
	w.status.SetText(s)
	if w.shell != nil {
		w.shell.statusBar.SetText(s)
	}
}

// setBusy shows Stop only while a run is going (spec 1.1: hidden, not
// insensitive, when idle), and the Run page's badge.
func (w *Window) setBusy(busy bool) {
	w.busy = busy
	w.stopBtn.SetVisible(busy)
	w.stopBtn.SetSensitive(busy)
	w.updateRunBadge()
}

// updateRunBadge is the Run entry's badge: "NN/MM" while a run goes (the
// step running, of the plan's), "done" once it finished, nothing before.
func (w *Window) updateRunBadge() {
	if w.shell == nil {
		return
	}
	b := w.shell.runBadge
	text := runBadge(w.busy, w.model)
	b.SetText(text)
	b.SetVisible(text != "")
}

// runBadge is the text of the Run entry's badge.
func runBadge(busy bool, m *runview.Model) string {
	switch {
	case busy && len(m.Rows) > 0:
		n := 0
		for i, r := range m.Rows {
			if r.State != runview.RowPending {
				n = i + 1
			}
		}
		return fmt.Sprintf("%02d/%02d", max(n, 1), len(m.Rows))
	case !busy && m.Finished != nil:
		return "done"
	}
	return ""
}
