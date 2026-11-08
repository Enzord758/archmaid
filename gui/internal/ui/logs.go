package ui

import (
	"errors"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// The Logs page (P8 row 14, A4 and A7): the run history as a sidebar, and
// the chosen run's log: a toggle per category, the log as entries and
// topgrade's collapsible sections in one ListView, a search that filters
// it, and Open folder. A narrow window collapses the split view: the runs
// first, then the log with a back button. It only reads: nothing here
// creates, deletes or rotates anything (that is a real run's business);
// Open folder only asks the Launcher.

// logsNarrow is the width under which the Logs page shows one pane.
const logsNarrow = 560

type logsPage struct {
	split  *adw.NavigationSplitView
	runs   *gtk.ListBox
	back   *gtk.Button
	title  *gtk.Label
	cats   *adw.ToggleGroup
	search *gtk.SearchEntry
	bar    *gtk.SearchBar
	folder *gtk.Button

	// list mirrors rows, line for line (text for the harness and the
	// labels; rows for each one's kind).
	list    *gtk.StringList
	listLen uint
	rows    []logview.Row
	// view is the entries rows were laid out from (the search applied):
	// rows' Entry indexes are into it.
	view []logview.Entry

	// runList is what the sidebar shows; run and cat the log in view;
	// entries that log parsed, collapsed its folded sections.
	runList   []logview.Run
	run       *logview.Run
	cat       logstore.Category
	entries   []logview.Entry
	collapsed map[logview.SectionKey]bool
	// filling: the toggles are being rebuilt, so their notify is not the
	// user's choice.
	filling bool

	// shown is "run/category" of the log in the view; open opens one by
	// that key (the harness's handles).
	shown string
	open  map[string]func()

	// newest is the newest run the last refresh saw; picked: the user
	// chose a run since then (refreshLogs).
	newest string
	picked bool
}

func (w *Window) newLogs() gtk.Widgetter {
	p := &logsPage{open: map[string]func(){}, collapsed: map[logview.SectionKey]bool{}}
	w.logs = p

	// The sidebar: one row per run directory, newest first, dry runs last.
	p.runs = gtk.NewListBox()
	p.runs.AddCSSClass("navigation-sidebar")
	p.runs.SetSelectionMode(gtk.SelectionSingle)
	p.runs.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if i := row.Index(); i >= 0 && i < len(p.runList) {
			p.picked = true
			w.showRun(p.runList[i], p.cat)
			p.split.SetShowContent(true)
		}
	})
	runsScroll := gtk.NewScrolledWindow()
	runsScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	runsScroll.SetChild(p.runs)
	sidebar := adw.NewNavigationPage(runsScroll, "Runs")

	// The log's bar: back (narrow only), the run, the categories, search
	// and Open folder.
	p.back = gtk.NewButtonFromIconName("go-previous-symbolic")
	p.back.SetTooltipText("Runs")
	p.back.AddCSSClass("flat")
	p.back.SetVisible(false)
	p.back.ConnectClicked(func() { p.split.SetShowContent(false) })
	p.title = gtk.NewLabel("Pick a run")
	p.title.AddCSSClass("heading")
	p.title.SetXAlign(0)
	p.title.SetHExpand(true)
	p.title.SetWrap(true)
	p.cats = adw.NewToggleGroup()
	p.cats.SetCanShrink(true)
	p.cats.NotifyProperty("active-name", func() {
		if p.filling || p.run == nil {
			return
		}
		if c := logstore.Category(p.cats.ActiveName()); c != "" && c != p.cat {
			w.openLog(*p.run, c)
		}
	})
	searchBtn := gtk.NewToggleButton()
	searchBtn.SetIconName("system-search-symbolic")
	searchBtn.SetTooltipText("Search this log")
	searchBtn.AddCSSClass("flat")
	p.folder = gtk.NewButtonFromIconName("folder-open-symbolic")
	p.folder.SetTooltipText("Open the run's folder")
	p.folder.AddCSSClass("flat")
	p.folder.SetSensitive(false)
	p.folder.ConnectClicked(func() {
		if p.run != nil {
			if err := w.launcher.OpenFolder(p.run.Dir()); err != nil {
				w.toast("Couldn't open the folder: " + err.Error())
			}
		}
	})
	bar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	bar.SetMarginStart(12)
	bar.SetMarginEnd(12)
	bar.SetMarginTop(6)
	bar.SetMarginBottom(6)
	bar.Append(p.back)
	bar.Append(p.title)
	bar.Append(searchBtn)
	bar.Append(p.folder)
	catsRow := gtk.NewBox(gtk.OrientationHorizontal, 0)
	catsRow.SetMarginStart(12)
	catsRow.SetMarginEnd(12)
	catsRow.SetMarginBottom(6)
	catsRow.Append(p.cats)

	p.search = gtk.NewSearchEntry()
	p.search.SetHExpand(true)
	p.search.ConnectSearchChanged(func() { w.layoutLog() })
	p.bar = gtk.NewSearchBar()
	p.bar.SetChild(p.search)
	p.bar.ConnectEntry(p.search)
	p.bar.SetShowCloseButton(true)
	searchBtn.ConnectToggled(func() {
		if on := searchBtn.Active(); on != p.bar.SearchMode() {
			p.bar.SetSearchMode(on)
		}
	})
	p.bar.NotifyProperty("search-mode-enabled", func() {
		if on := p.bar.SearchMode(); on != searchBtn.Active() {
			searchBtn.SetActive(on)
		}
		if !p.bar.SearchMode() && p.search.Text() != "" {
			p.search.SetText("") // closing the search shows the whole log again
		}
	})

	// The log: one ListView, a row per entry header, section header or
	// line (logview.Rows); clicking a section header folds it.
	p.list = gtk.NewStringList(nil)
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(func(obj *coreglib.Object) {
		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		box.SetMarginStart(12)
		box.SetMarginEnd(12)
		text := gtk.NewLabel("")
		text.SetXAlign(0)
		text.SetHExpand(true)
		text.SetSelectable(true)
		text.SetWrap(true)
		text.SetWrapMode(pango.WrapWordChar) // a long path wraps; nothing is cut
		aside := gtk.NewLabel("")
		aside.SetXAlign(1)
		aside.AddCSSClass("dim-label")
		box.Append(text)
		box.Append(aside)
		obj.Cast().(*gtk.ListItem).SetChild(box)
	})
	factory.ConnectBind(func(obj *coreglib.Object) {
		item := obj.Cast().(*gtk.ListItem)
		box := item.Child().(*gtk.Box)
		text := box.FirstChild().(*gtk.Label)
		aside := box.LastChild().(*gtk.Label)
		i := int(item.Position())
		if i >= len(p.rows) {
			return
		}
		r := p.rows[i]
		text.SetText(r.Text)
		aside.SetText(r.Aside)
		aside.SetVisible(r.Aside != "")
		for _, c := range []string{"heading", "caption-heading", "monospace", "am-state-ok", "am-state-failed", "am-state-auth-refused", "am-state-running"} {
			text.RemoveCSSClass(c)
		}
		text.SetSelectable(r.Kind == logview.RowLine)
		item.SetActivatable(r.Kind == logview.RowSection)
		switch r.Kind {
		case logview.RowEntry:
			text.AddCSSClass("heading")
			if c := entryClass(p.view, r.Entry); c != "" {
				text.AddCSSClass(c)
			}
		case logview.RowSection:
			text.AddCSSClass("caption-heading")
		default:
			text.AddCSSClass("monospace")
		}
	})
	view := gtk.NewListView(gtk.NewNoSelection(p.list), &factory.ListItemFactory)
	view.AddCSSClass("am-logs-list")
	view.SetSingleClickActivate(true)
	view.ConnectActivate(func(pos uint) { w.toggleSection(int(pos)) })
	logScroll := gtk.NewScrolledWindow()
	logScroll.AddCSSClass(textSurfClass) // glass.css: the log text is 93 % opaque
	logScroll.SetChild(view)
	logScroll.SetVExpand(true)
	logScroll.SetHExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.Append(bar)
	content.Append(catsRow)
	content.Append(p.bar)
	content.Append(logScroll)
	contentPage := adw.NewNavigationPage(content, "Log")

	p.split = adw.NewNavigationSplitView()
	p.split.SetSidebar(sidebar)
	p.split.SetContent(contentPage)
	p.split.SetMinSidebarWidth(240)
	p.split.SetMaxSidebarWidth(330)
	p.split.NotifyProperty("collapsed", func() { p.back.SetVisible(p.split.Collapsed()) })

	bin := adw.NewBreakpointBin()
	bin.SetSizeRequest(300, 200)
	bin.SetChild(p.split)
	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(adw.BreakpointConditionMaxWidth, logsNarrow, adw.LengthUnitSp))
	bp.AddSetter(p.split, "collapsed", true)
	bin.AddBreakpoint(bp)
	return bin
}

// entryClass is the state colour for an entry's kind: done, failed,
// declined or skipped, and the rest (dry run, audit, accept) as running.
func entryClass(entries []logview.Entry, i int) string {
	if i < 0 || i >= len(entries) {
		return ""
	}
	switch entries[i].Kind {
	case "OK":
		return "am-state-ok"
	case "FAIL", "ABORT":
		return "am-state-failed"
	case "SKIP", "WARN":
		return "am-state-auth-refused"
	case "":
		return ""
	}
	return "am-state-running"
}

func (w *Window) showLogs() {
	w.refreshLogs()
	w.stack.SetVisibleChildName("logs")
}

// refreshLogs rereads the run history and shows the run that was shown, if
// it is still there, else the newest. When a newer run has appeared since
// the last refresh, the newest is shown instead, unless the user picked a
// run in the meantime.
func (w *Window) refreshLogs() {
	p := w.logs
	p.runs.RemoveAll()
	clear(p.open)
	p.runList = nil
	runs, err := logview.Runs(w.opts.StateDir)
	if err != nil {
		p.runs.Append(placeholder("The logs can't be read", err.Error()))
		w.showRun(logview.Run{}, "")
		return
	}
	if len(runs) == 0 {
		p.runs.Append(placeholder("No logs yet", "Each run writes its logs here."))
		w.showRun(logview.Run{}, "")
		return
	}
	p.runList = runs
	newer := p.newest != "" && runs[0].Name != p.newest && !p.picked
	p.newest, p.picked = runs[0].Name, false
	pick := 0
	for i, r := range runs {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(r.Title())
		row.SetSubtitle(r.Subtitle())
		row.SetActivatable(true)
		p.runs.Append(row)
		if p.run != nil && !newer && r.Name == p.run.Name {
			pick = i
		}
		for _, c := range r.Categories {
			p.open[r.Name+"/"+string(c)] = func() { w.showRun(r, c) }
		}
	}
	p.runs.SelectRow(p.runs.RowAtIndex(pick))
	w.showRun(runs[pick], p.cat)
}

func placeholder(title, sub string) gtk.Widgetter {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(title)
	row.SetSubtitle(sub)
	return row
}

// showRun shows run r: its categories as toggles, and the log for cat if
// r has it, else its first. A zero r (no runs) shows nothing.
func (w *Window) showRun(r logview.Run, cat logstore.Category) {
	p := w.logs
	p.filling = true
	p.cats.RemoveAll()
	for _, c := range r.Categories {
		t := adw.NewToggle()
		t.SetName(string(c))
		t.SetLabel(c.Label())
		p.cats.Add(t)
	}
	p.filling = false
	p.folder.SetSensitive(r.Dir() != "")
	if r.Name == "" {
		p.run, p.cat, p.entries, p.shown = nil, "", nil, ""
		p.title.SetText("No logs yet")
		w.layoutLog()
		return
	}
	if i := p.runs.SelectedRow(); i == nil || i.Index() < 0 || i.Index() >= len(p.runList) || p.runList[i.Index()].Name != r.Name {
		for i, x := range p.runList {
			if x.Name == r.Name {
				p.runs.SelectRow(p.runs.RowAtIndex(i))
			}
		}
	}
	run := r
	p.run = &run
	if len(r.Categories) == 0 {
		p.cat, p.entries, p.shown = "", nil, r.Name+"/"
		p.title.SetText(r.Title() + " · no logs")
		w.layoutLog()
		return
	}
	pick := r.Categories[0]
	for _, c := range r.Categories {
		if c == cat {
			pick = c
		}
	}
	w.openLog(r, pick)
}

// openLog shows one run's category log.
func (w *Window) openLog(r logview.Run, c logstore.Category) {
	p := w.logs
	lines, err := r.Read(c)
	switch {
	case errors.Is(err, logstore.ErrEmpty):
		lines = []string{"(empty)"}
	case err != nil:
		lines = []string{"Can't read this log: " + err.Error()}
	}
	p.entries = logview.Parse(lines)
	clear(p.collapsed)
	p.cat = c
	p.shown = r.Name + "/" + string(c)
	p.filling = true
	p.cats.SetActiveName(string(c))
	p.filling = false
	p.title.SetText(r.Title())
	w.layoutLog()
}

// layoutLog redraws the list from the log in view, the search and the
// folded sections.
func (w *Window) layoutLog() {
	p := w.logs
	// While searching, every match shows: folding is for browsing, and a
	// filtered view's section indexes aren't the log's.
	folded := p.collapsed
	if p.searching() {
		folded = nil
	}
	p.view = logview.Filter(p.entries, p.search.Text())
	p.rows = logview.Rows(p.view, folded)
	texts := make([]string, len(p.rows))
	for i, r := range p.rows {
		texts[i] = r.Text
	}
	p.list.Splice(0, p.listLen, texts)
	p.listLen = uint(len(texts))
}

// toggleSection folds or unfolds the section whose header is row i.
func (w *Window) toggleSection(i int) {
	p := w.logs
	if i < 0 || i >= len(p.rows) || p.rows[i].Kind != logview.RowSection || p.searching() {
		return
	}
	k := logview.SectionKey{Entry: p.rows[i].Entry, Section: p.rows[i].Section}
	p.collapsed[k] = !p.collapsed[k]
	w.layoutLog()
}

// searching: the search has text, so the list is filtered.
func (p *logsPage) searching() bool { return strings.TrimSpace(p.search.Text()) != "" }
