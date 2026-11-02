package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
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
	{"packages", "Packages", "system-software-install-symbolic"},
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
	side      *gtk.Box // the sidebar (a click outside it closes the overlay)
	nav       *gtk.ListBox
	navRows   map[string]*gtk.ListBoxRow
	runBadge  *gtk.Label
	pkgBadge  *gtk.Label // the Packages list's size
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
	// Single, not Browse: a browse list selects whatever row gets the
	// focus, so the overlay coming back (a breakpoint, the toggle) could
	// switch the page by itself (Dev 5 saw Run come up after a resize).
	// A page changes only when a row is activated: clicked or Enter.
	sh.nav.SetSelectionMode(gtk.SelectionSingle)
	for _, p := range navPages {
		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		box.Append(gtk.NewImageFromIconName(p.icon))
		l := gtk.NewLabel(p.title)
		l.SetXAlign(0)
		l.SetHExpand(true)
		box.Append(l)
		if p.name == "packages" {
			sh.pkgBadge = gtk.NewLabel("")
			sh.pkgBadge.AddCSSClass("am-badge")
			sh.pkgBadge.AddCSSClass("numeric")
			sh.pkgBadge.SetVisible(false)
			box.Append(sh.pkgBadge)
		}
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
	// Activated, not selected: picking the page already shown must still
	// close the overlay (row-selected doesn't fire for it).
	sh.nav.ConnectRowActivated(func(row *gtk.ListBoxRow) { w.pickPage(row.Name()) })
	w.status.SetVExpand(false)
	w.status.SetWrap(true)
	w.status.AddCSSClass("am-footer")
	spacer := gtk.NewBox(gtk.OrientationVertical, 0)
	spacer.SetVExpand(true)
	side := gtk.NewBox(gtk.OrientationVertical, 6)
	side.AddCSSClass("am-sidebar")
	sh.side = side
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
	// Ellipsized, with the whole title as its tooltip: the header centres
	// its title between Stop and the window's buttons, and an uncut "Full
	// Cleanup (Recommended)" made the window 479 px wide at least mid-run
	// (Dev 5's VM round b5, in a 360 px window).
	sh.title.SetEllipsize(3) // pango.EllipsizeEnd
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
	// The open overlay closes on Escape and on a click outside it, in the
	// capture phase, so the click doesn't also reach the dimmed page.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool { return w.navKey(keyval) })
	sh.split.AddController(keys)
	press := gtk.NewGestureClick()
	press.SetPropagationPhase(gtk.PhaseCapture)
	press.ConnectPressed(func(_ int, x, y float64) {
		if w.navPress(x, y) {
			press.SetState(gtk.EventSequenceClaimed)
		}
	})
	sh.split.AddController(press)

	// Only one breakpoint applies at a time (the last added that
	// matches), so narrow-and-short has the setters of both.
	narrowSetters := func(bp *adw.Breakpoint) {
		bp.AddSetter(sh.split, "collapsed", true)
		bp.AddSetter(sh.toggle, "visible", true)
		bp.AddSetter(sh.statusBar, "visible", true)
		// The hero's plan is one column here, and a title that wraps or
		// tags moved under it make that line taller: homogeneous, every
		// line would be as tall as the tallest.
		bp.AddSetter(w.heroSteps, "homogeneous", false)
	}
	shortSetters := func(bp *adw.Breakpoint) {
		bp.AddSetter(sh.meters.box, "visible", false)
		if w.heroSteps.Visible() { // the hero's plan, when there is one
			bp.AddSetter(w.heroSteps, "visible", false)
		}
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
	// What a setter can't do: the Run page's step headers come and go with
	// the list, and the rows' tags move between beside and under.
	w.win.NotifyProperty("current-breakpoint", func() {
		cur := w.win.CurrentBreakpoint()
		on := cur != nil && (sameObject(cur, narrow) || sameObject(cur, both))
		if on != w.narrow {
			w.setNarrow(on)
		}
	})

	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(sh.split)
	w.win.SetContent(w.toasts)

	w.stack.NotifyProperty("visible-child-name", w.pageShown)
	w.pageShown()
}

// sameObject: a and b wrap the same GObject.
func sameObject(a, b coreglib.Objector) bool {
	return coreglib.InternObject(a).Native() == coreglib.InternObject(b).Native()
}

// overlayOpen: the split view is collapsed and its sidebar shown over the
// page.
func (sh *shell) overlayOpen() bool { return sh.split.Collapsed() && sh.split.ShowSidebar() }

// pickPage is a page chosen in the sidebar: it shows it and, in the narrow
// form, closes the overlay.
func (w *Window) pickPage(name string) {
	w.stack.SetVisibleChildName(name)
	if w.shell.split.Collapsed() {
		w.shell.split.SetShowSidebar(false)
	}
}

// navKey closes the open overlay on Escape; true when it did.
func (w *Window) navKey(keyval uint) bool {
	if keyval != gdk.KEY_Escape || !w.shell.overlayOpen() {
		return false
	}
	w.shell.split.SetShowSidebar(false)
	return true
}

// navPress closes the open overlay on a press outside the sidebar (x, y
// in the split view's coordinates); true when it did.
func (w *Window) navPress(x, y float64) bool {
	sh := w.shell
	if !sh.overlayOpen() {
		return false
	}
	if r, ok := sh.side.ComputeBounds(sh.split); ok && x >= float64(r.X()) && x < float64(r.X()+r.Width()) &&
		y >= float64(r.Y()) && y < float64(r.Y()+r.Height()) {
		return false // inside the sidebar: its own rows handle it
	}
	sh.split.SetShowSidebar(false)
	return true
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
	if name == "packages" {
		w.reloadPackages() // pacman may have run outside the window
	}
}

// reloadPackages has the Packages page re-read pacman's databases when their
// files changed since it last did.
func (w *Window) reloadPackages() {
	if w.pkgs != nil && w.pkgs.ctl != nil {
		w.pkgs.ctl.Reload()
	}
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
	w.shell.title.SetTooltipText(title)
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
	w.redrawHeaders()
}

// updateRunBadge is the Run entry's badge: "NN/MM" while a run goes (the
// step running, of the plan's), "done" once it finished, nothing before.
func (w *Window) updateRunBadge() {
	if w.shell == nil {
		return
	}
	b := w.shell.runBadge
	text := w.model.Header().Badge
	b.SetText(text)
	b.SetVisible(text != "")
}
