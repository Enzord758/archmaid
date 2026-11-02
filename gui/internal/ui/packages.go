package ui

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page (task #234, variant A): a search field and a list of
// results beside a detail pane (below the narrow breakpoint one at a time:
// AdwNavigationSplitView collapsed), an installed view, the list tray at the
// bottom and the confirm sheet (packages_sheet.go). The state is
// gui/internal/pkgview's, plain Go; this file only draws it. Every package
// text (names, versions, descriptions, the AUR's maintainer, a PKGBUILD) is
// a plain label, never markup.

// PackagesWiring is what the page needs from outside. A nil
// Options.Packages leaves the page saying it is not available in this build.
type PackagesWiring struct {
	// Catalog is the pacman DB reader (*pkgdb.DB); AUR the AUR client.
	Catalog pkgview.Catalog
	AUR     aursearch.Searcher
	// Backend takes StartPackages, Answer and Abort to the engine. Its
	// UpdateSystem is not used: the window starts the existing topgrade
	// action itself.
	Backend pkgview.Backend
	// Updates reads updates-cache.json (nil: overview.ReadUpdates on the
	// window's state folder). DBAge is the sync DBs' age (nil: unknown).
	Updates func() overview.Updates
	DBAge   func() time.Duration
	// Reload re-reads the pacman DBs after a successful run.
	Reload func()
	// Clock is the debounce's clock (nil: the wall clock; a test passes a
	// pkgview.FakeClock).
	Clock pkgview.Clock
	// Err, with a nil Catalog, says why the lists couldn't be opened (the
	// page then shows that instead of a search).
	Err string
}

// pkgBackend is the page's Backend: the wiring's, with the window's own
// topgrade for "Update system first".
type pkgBackend struct {
	w     *Window
	inner pkgview.Backend
}

func (b pkgBackend) StartPackages(r steps.PkgRequest) {
	if b.inner != nil {
		b.inner.StartPackages(r)
	}
}
func (b pkgBackend) Answer(p int, yes bool) {
	if b.inner != nil {
		b.inner.Answer(p, yes)
	}
}
func (b pkgBackend) Abort() {
	if b.inner != nil {
		b.inner.Abort()
	}
}
func (b pkgBackend) UpdateSystem() {
	if b.w.busy {
		b.w.toast("A run is going. Update the system when it is done.")
		return
	}
	b.w.startAction(actions.IDTopgrade)
}

// pkgPage is the page's widgets.
type pkgPage struct {
	w   *Window
	ctl *pkgview.Controller

	root   gtk.Widgetter
	banner *adw.Banner
	split  *adw.NavigationSplitView
	back   *gtk.Button

	modeSearch, modeInstalled *gtk.ToggleButton
	searchBox, installedBox   *gtk.Box
	entry                     *gtk.SearchEntry
	filter                    *gtk.SearchEntry
	viewBtns                  []*gtk.ToggleButton
	orphanNote                *gtk.Box
	orphanText                *gtk.Label
	listStack                 *gtk.Stack
	results                   *gtk.ListBox
	instList                  *gtk.ListBox
	emptyDB                   *gtk.Label
	noneLabel                 *gtk.Label
	detailStack               *gtk.Stack
	detail                    *gtk.Box

	tray        *gtk.Box
	trayKicker  *gtk.Label
	trayNames   *gtk.Label
	trayNote    *gtk.Label
	trayConfig  *gtk.CheckButton
	trayClear   *gtk.Button
	trayReview  *gtk.Button
	trayUpdate  *gtk.Button
	traySyncing bool

	installedMode bool
	selected      string
	lines         []*tagLine
	dirty         bool

	// The sheet (packages_sheet.go).
	sheet       *amDialog
	sheetState  pkgview.SheetState
	sheetYes    *gtk.Button
	sheetChecks []*gtk.CheckButton
	sheetAt     time.Time // when the dialog was presented (its opening animation)
	navAt       time.Time // the last drill-in or back (its slide animation)
}

// newPackages builds the page. Without wiring it is the "not available"
// notice.
func (w *Window) newPackages() gtk.Widgetter {
	p := &pkgPage{w: w}
	w.pkgs = p
	wire := w.opts.Packages
	if wire == nil || wire.Catalog == nil {
		title, why := "Packages are not available in this build", "This build has no package engine yet."
		if wire != nil {
			title, why = "The package lists couldn't be read", wire.Err
			if why == "" {
				why = "pacman's databases are missing or unreadable."
			}
		}
		box := gtk.NewBox(gtk.OrientationVertical, 8)
		box.SetVAlign(gtk.AlignCenter)
		margin(box, 24)
		t := plainLabel(title, "title-2")
		t.SetXAlign(0.5)
		t.SetJustify(gtk.JustifyCenter)
		d := plainLabel(why, "am-note")
		d.SetXAlign(0.5)
		d.SetJustify(gtk.JustifyCenter)
		box.Append(t)
		box.Append(d)
		p.root = box
		return box
	}
	updates := wire.Updates
	if updates == nil {
		dir := w.opts.StateDir
		updates = func() overview.Updates { return overview.ReadUpdates(dir) }
	}
	search := pkgview.NewSearch(pkgview.Deps{
		Catalog: wire.Catalog, AUR: wire.AUR, Clock: wire.Clock,
		Post:    post,
		Changed: p.changed,
	})
	p.ctl = pkgview.NewController(pkgview.Config{
		Search: search, Catalog: wire.Catalog, Backend: pkgBackend{w: w, inner: wire.Backend}, Clock: wire.Clock,
		Updates: updates, DBAge: wire.DBAge, Reload: wire.Reload, Changed: p.changed,
		ShowRun: func() { w.stack.SetVisibleChildName("run") },
	})
	p.build()
	// While the page is up the cache is re-read every 2 s: "Update system
	// first" ends in a new count, and the page re-enables by itself.
	coreglib.TimeoutAdd(2000, func() bool {
		if w.closed {
			return false
		}
		if w.stack.VisibleChildName() == "packages" {
			p.ctl.RefreshUpdates()
		}
		return true
	})
	p.refresh()
	return p.root
}

// changed schedules one redraw for the changes of this main-loop turn.
func (p *pkgPage) changed() {
	if p.dirty {
		return
	}
	p.dirty = true
	post(func() {
		p.dirty = false
		if !p.w.closed {
			p.refresh()
		}
	})
}

func plainLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	wrapNatural(l)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

func ellipsized(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetEllipsize(pango.EllipsizeEnd)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

func pkgTag(t pkgview.Tag) *gtk.Label {
	l := tag(t.Text, "am-tag-"+string(t.Tone))
	l.SetUseMarkup(false)
	return l
}

// build lays the page out.
func (p *pkgPage) build() {
	// The sidebar of the split: the mode switch, the search or installed
	// controls, the list.
	p.modeSearch = gtk.NewToggleButtonWithLabel("Search")
	p.modeInstalled = gtk.NewToggleButtonWithLabel("Installed")
	p.modeInstalled.SetGroup(p.modeSearch)
	p.modeSearch.SetActive(true)
	p.modeSearch.SetHExpand(true)
	p.modeInstalled.SetHExpand(true)
	modes := gtk.NewBox(gtk.OrientationHorizontal, 0)
	modes.AddCSSClass("linked")
	modes.Append(p.modeSearch)
	modes.Append(p.modeInstalled)
	p.modeSearch.ConnectToggled(func() { p.setMode(!p.modeSearch.Active()) })

	p.entry = gtk.NewSearchEntry()
	p.entry.SetPlaceholderText("Search packages")
	p.entry.SetSearchDelay(0)
	p.entry.Connect("changed", func() { p.ctl.Search.SetQuery(p.entry.Text()) })
	p.entry.ConnectActivate(func() {
		if hits := p.ctl.Search.Results().AllHits(); len(hits) > 0 {
			p.selectPkg(hits[0])
		}
	})
	p.searchBox = gtk.NewBox(gtk.OrientationVertical, 8)
	p.searchBox.Append(p.entry)

	p.filter = gtk.NewSearchEntry()
	p.filter.SetPlaceholderText("Filter the installed packages")
	p.filter.SetSearchDelay(0)
	p.filter.Connect("changed", func() {
		p.ctl.Installed.SetFilter(p.filter.Text())
		p.changed()
	})
	tabs := gtk.NewFlowBox()
	tabs.SetSelectionMode(gtk.SelectionNone)
	tabs.SetMinChildrenPerLine(2)
	tabs.SetMaxChildrenPerLine(4)
	tabs.SetColumnSpacing(6)
	tabs.SetRowSpacing(6)
	tabs.SetHomogeneous(true)
	for i, v := range pkgview.Views {
		b := gtk.NewToggleButtonWithLabel(pkgview.ViewTitle(v))
		if i > 0 {
			b.SetGroup(p.viewBtns[0])
		}
		b.SetActive(i == 0)
		view := v
		b.ConnectToggled(func() {
			if b.Active() {
				p.ctl.Installed.SetView(view)
				p.changed()
			}
		})
		p.viewBtns = append(p.viewBtns, b)
		tabs.Append(b)
	}
	p.orphanText = plainLabel("", "am-note")
	p.orphanText.SetHExpand(true)
	toClean := gtk.NewButtonWithLabel("Full Cleanup")
	toClean.SetVAlign(gtk.AlignCenter)
	toClean.ConnectClicked(func() { p.w.pickPage("overview") })
	p.orphanNote = gtk.NewBox(gtk.OrientationHorizontal, 8)
	p.orphanNote.AddCSSClass("am-pkg-orphans")
	p.orphanNote.Append(p.orphanText)
	p.orphanNote.Append(toClean)
	p.installedBox = gtk.NewBox(gtk.OrientationVertical, 8)
	p.installedBox.Append(p.filter)
	p.installedBox.Append(tabs)
	p.installedBox.Append(p.orphanNote)
	p.installedBox.SetVisible(false)

	p.results = gtk.NewListBox()
	p.results.SetSelectionMode(gtk.SelectionSingle)
	p.results.AddCSSClass("am-pkg-list")
	p.results.ConnectRowActivated(func(r *gtk.ListBoxRow) { p.rowActivated(r) })
	p.instList = gtk.NewListBox()
	p.instList.SetSelectionMode(gtk.SelectionSingle)
	p.instList.AddCSSClass("am-pkg-list")
	p.instList.ConnectRowActivated(func(r *gtk.ListBoxRow) { p.rowActivated(r) })

	p.listStack = gtk.NewStack()
	p.listStack.AddNamed(p.emptyState(), "empty")
	p.noneLabel = plainLabel("", "am-note")
	none := gtk.NewBox(gtk.OrientationVertical, 6)
	margin(none, 16)
	none.Append(p.noneLabel)
	p.listStack.AddNamed(none, "none")
	p.listStack.AddNamed(scrolled(p.results), "results")
	p.listStack.AddNamed(scrolled(p.instList), "installed")
	p.listStack.SetVExpand(true)

	side := gtk.NewBox(gtk.OrientationVertical, 8)
	margin(side, 12)
	side.Append(modes)
	side.Append(p.searchBox)
	side.Append(p.installedBox)
	side.Append(p.listStack)

	// The content: a back button when collapsed, the detail.
	p.back = gtk.NewButtonFromIconName("go-previous-symbolic")
	p.back.SetTooltipText("Back to the list")
	p.back.AddCSSClass("flat")
	p.back.SetHAlign(gtk.AlignStart)
	p.back.SetVisible(false)
	p.back.ConnectClicked(func() { p.split.SetShowContent(false); p.navAt = time.Now() })
	p.detail = gtk.NewBox(gtk.OrientationVertical, 14)
	margin(p.detail, 16)
	p.detailStack = gtk.NewStack()
	nothing := plainLabel("Pick a package to see its details.", "am-note")
	margin(nothing, 16)
	p.detailStack.AddNamed(nothing, "none")
	p.detailStack.AddNamed(scrolled(p.detail), "detail")
	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(p.back)
	content.Append(p.detailStack)
	p.detailStack.SetVExpand(true)

	p.split = adw.NewNavigationSplitView()
	p.split.SetMinSidebarWidth(280)
	p.split.SetMaxSidebarWidth(420)
	p.split.SetSidebarWidthFraction(0.42)
	p.split.SetSidebar(adw.NewNavigationPage(side, "Packages"))
	p.split.SetContent(adw.NewNavigationPage(content, "Details"))
	p.split.SetVExpand(true)

	p.banner = adw.NewBanner("")
	p.banner.SetUseMarkup(false)
	p.banner.SetButtonLabel("Update system first")
	p.banner.ConnectButtonClicked(func() { p.ctl.UpdateFirst() })

	p.buildTray()

	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.Append(p.banner)
	root.Append(p.split)
	root.Append(p.tray)
	p.root = root
}

// margin sets all four margins of w.
func margin(w gtk.Widgetter, n int) {
	b := gtk.BaseWidget(w)
	b.SetMarginTop(n)
	b.SetMarginBottom(n)
	b.SetMarginStart(n)
	b.SetMarginEnd(n)
}

func scrolled(child gtk.Widgetter) *gtk.ScrolledWindow {
	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sc.SetChild(child)
	sc.SetVExpand(true)
	return sc
}

// emptyState is A1: what the page is for and where to start.
func (p *pkgPage) emptyState() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	margin(box, 16)
	box.Append(plainLabel("Search the packages", "title-3"))
	box.Append(plainLabel("Type a name or a word from the description. The repositories answer at once; the AUR is asked after a short pause.", "am-note"))
	p.emptyDB = plainLabel("", "am-note")
	box.Append(p.emptyDB)
	return box
}

func (p *pkgPage) setMode(installed bool) {
	p.installedMode = installed
	p.searchBox.SetVisible(!installed)
	p.installedBox.SetVisible(installed)
	p.changed()
}

// setNarrow: the split collapses to a drill-in, the back button shows,
// the rows' tags go under their names.
func (p *pkgPage) setNarrow(narrow bool) {
	if p == nil || p.split == nil {
		return
	}
	p.split.SetCollapsed(narrow)
	p.back.SetVisible(narrow)
	for _, l := range p.lines {
		l.setUnder(narrow)
	}
	if narrow {
		p.root.(*gtk.Box).AddCSSClass("am-pkg-narrow")
	} else {
		p.root.(*gtk.Box).RemoveCSSClass("am-pkg-narrow")
	}
}

func (p *pkgPage) rowActivated(r *gtk.ListBoxRow) {
	name := r.Name()
	if name == "" {
		return
	}
	for _, h := range p.ctl.Search.Results().AllHits() {
		if h.Name == name {
			p.selectPkg(h)
			return
		}
	}
	rows, _ := p.ctl.Installed.Rows()
	for _, r := range rows {
		if r.Pkg.Name == name {
			p.selectPkg(r.Pkg)
			return
		}
	}
}

func (p *pkgPage) selectPkg(pk pkgdb.Package) {
	p.selected = pk.Name
	p.ctl.Search.Select(pk)
	p.split.SetShowContent(true)
	p.navAt = time.Now()
}

// refresh redraws everything from the model.
func (p *pkgPage) refresh() {
	if p.ctl == nil {
		return
	}
	p.lines = p.lines[:0]
	p.refreshBanner()
	p.refreshList()
	p.refreshDetail()
	p.refreshTray()
	p.refreshSheet()
	p.w.updatePkgBadge()
}

func (p *pkgPage) refreshBanner() {
	g := p.ctl.Gating(pkgview.ModeInstall)
	// The banner is for installing: it shows while updates wait and the
	// list (or the page) is about installs; a removal list is never gated.
	show := g.Blocked && p.ctl.List.Mode() != pkgview.ModeRemove
	if show {
		p.banner.SetTitle(g.Text + ". Update the system before installing.")
	}
	p.banner.SetRevealed(show)
}

func (p *pkgPage) refreshList() {
	if p.installedMode {
		p.refreshInstalled()
		return
	}
	res := p.ctl.Search.Results()
	switch {
	case !res.Active:
		p.emptyDB.SetText(p.dbLine())
		p.listStack.SetVisibleChildName("empty")
		return
	case len(res.Repo) == 0 && len(res.AUR) == 0 && !res.Searching &&
		(res.AURState == pkgview.AURReady || res.AURState == pkgview.AURDisabled || res.AURState == pkgview.AURFailed):
		p.noneLabel.SetText(fmt.Sprintf("No packages match %q.", res.Query))
		if res.AURState == pkgview.AURFailed {
			p.noneLabel.SetText(fmt.Sprintf("No repository package matches %q, and the AUR could not be reached.", res.Query))
		}
		p.listStack.SetVisibleChildName("none")
		return
	}
	p.results.RemoveAll()
	if len(res.Repo) > 0 {
		p.results.Append(sectionRow("Repositories", len(res.Repo)))
		for _, h := range res.Repo {
			p.results.Append(p.resultRow(h, pkgview.ModeInstall, ""))
		}
	} else if res.Searching {
		p.results.Append(noteRow("Searching…", false))
	}
	switch res.AURState {
	case pkgview.AURWaiting, pkgview.AURLoading:
		p.results.Append(sectionRow("AUR", -1))
		p.results.Append(noteRow("Searching the AUR…", true))
	case pkgview.AURFailed:
		p.results.Append(sectionRow("AUR", -1))
		p.results.Append(p.aurFailedRow(res.AURNote))
	case pkgview.AURReady:
		if len(res.AUR) > 0 {
			p.results.Append(sectionRow("AUR", len(res.AUR)))
			for _, h := range res.AUR {
				p.results.Append(p.resultRow(h, pkgview.ModeInstall, ""))
			}
		} else {
			p.results.Append(sectionRow("AUR", 0))
		}
	}
	p.markSelected(p.results)
	p.listStack.SetVisibleChildName("results")
}
