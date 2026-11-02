package ui

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func (p *pkgPage) dbLine() string {
	if p.ctl == nil || p.w.opts.Packages == nil || p.w.opts.Packages.DBAge == nil {
		return ""
	}
	if age := p.w.opts.Packages.DBAge(); age >= 0 {
		return "Package lists are " + pkgview.Age(age) + " old. This page never refreshes them; the system update does."
	}
	return ""
}

// fillRecent is the empty state's "Recently installed": the last few
// installs from the pacman database, each a button that opens its details.
func (p *pkgPage) fillRecent() {
	for c := p.recent.FirstChild(); c != nil; c = p.recent.FirstChild() {
		p.recent.Remove(c)
	}
	var rows []pkgview.RecentRow
	if w := p.w.opts.Packages; w != nil {
		rows = pkgview.RecentRows(w.Catalog, time.Now(), pkgview.RecentMax)
	}
	p.recent.SetVisible(len(rows) > 0)
	if len(rows) == 0 {
		return
	}
	p.recent.Append(plainLabel("Recently installed", "am-kicker"))
	for _, r := range rows {
		pk := r.Pkg
		line := gtk.NewBox(gtk.OrientationHorizontal, 8)
		name := ellipsized(pk.Name, "am-pkg-name")
		name.SetHExpand(true)
		line.Append(name)
		line.Append(plainLabel(pk.InstalledVersion, "am-pkg-ver"))
		line.Append(plainLabel(r.Ago, "am-pkg-ver"))
		b := gtk.NewButton()
		b.AddCSSClass("flat")
		b.SetChild(line)
		b.ConnectClicked(func() { p.selectPkg(pk) })
		p.recent.Append(b)
	}
}

func (p *pkgPage) markSelected(lb *gtk.ListBox) {
	for i := 0; ; i++ {
		r := lb.RowAtIndex(i)
		if r == nil {
			return
		}
		if r.Name() == p.selected && p.selected != "" {
			lb.SelectRow(r)
			return
		}
	}
}

func sectionRow(title string, n int) *gtk.ListBoxRow {
	text := title
	if n >= 0 {
		text = fmt.Sprintf("%s · %d", title, n)
	}
	l := plainLabel(text, "am-kicker")
	l.SetMarginTop(10)
	l.SetMarginBottom(4)
	l.SetMarginStart(4)
	r := gtk.NewListBoxRow()
	r.SetChild(l)
	r.SetActivatable(false)
	r.SetSelectable(false)
	return r
}

func noteRow(text string, spin bool) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	margin(box, 8)
	if spin {
		box.Append(adw.NewSpinner())
	}
	box.Append(plainLabel(text, "am-note"))
	r := gtk.NewListBoxRow()
	r.SetChild(box)
	r.SetActivatable(false)
	r.SetSelectable(false)
	return r
}

func (p *pkgPage) aurFailedRow(note string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	margin(box, 8)
	box.Append(plainLabel("AUR unreachable", "am-pkg-name"))
	if note != "" {
		box.Append(plainLabel(note, "am-note"))
	}
	retry := gtk.NewButtonWithLabel("Try again")
	retry.SetHAlign(gtk.AlignStart)
	retry.ConnectClicked(func() { p.ctl.Search.Retry() })
	box.Append(retry)
	r := gtk.NewListBoxRow()
	r.SetChild(box)
	r.SetActivatable(false)
	r.SetSelectable(false)
	return r
}

// resultRow is one package of the results or of the installed view: name,
// version and tags, the description (or why it can't be removed), and the
// round button that adds it to the list.
func (p *pkgPage) resultRow(pk pkgdb.Package, mode pkgview.Mode, why string) *gtk.ListBoxRow {
	name := ellipsized(pk.Name, "am-pkg-name")
	ver := ellipsized(shownVersion(pk), "am-pkg-ver")
	title := gtk.NewBox(gtk.OrientationHorizontal, 8)
	title.Append(name)
	title.Append(ver)
	var tags []gtk.Widgetter
	for _, t := range pkgview.RowTags(pk) {
		if mode == pkgview.ModeRemove && t.Text == "installed" {
			continue // every row of the installed view is
		}
		tags = append(tags, pkgTag(t))
	}
	line := newTagLine(title, 8, tags...)
	line.setUnder(p.w.narrow)
	line.box.SetHExpand(true)
	p.lines = append(p.lines, line)
	desc := pk.Description
	if why != "" {
		desc = why
	}
	d := ellipsized(desc, "am-pkg-desc")
	text := gtk.NewBox(gtk.OrientationVertical, 2)
	text.SetHExpand(true)
	text.Append(line.box)
	text.Append(d)

	add := p.addButton(pk, mode, why)
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.AddCSSClass("am-pkg-row")
	row.Append(text)
	row.Append(add)
	if why != "" {
		row.AddCSSClass("am-pkg-greyed")
	}
	r := gtk.NewListBoxRow()
	r.SetName(pk.Name)
	r.SetChild(row)
	return r
}

func shownVersion(pk pkgdb.Package) string {
	if pk.Installed && pk.InstalledVersion != "" && pk.InstalledVersion != pk.Version && pk.Repo != "" {
		return pk.InstalledVersion + " → " + pk.Version
	}
	return pk.Version
}

// addButton is the round + (or − on a removal) at a row's end.
func (p *pkgPage) addButton(pk pkgdb.Package, mode pkgview.Mode, why string) *gtk.Button {
	icon := "list-add-symbolic"
	tip := "Add to the install list"
	if mode == pkgview.ModeRemove {
		icon, tip = "list-remove-symbolic", "Add to the removal list"
	}
	b := gtk.NewButtonFromIconName(icon)
	b.AddCSSClass("circular")
	b.AddCSSClass("flat")
	b.SetVAlign(gtk.AlignCenter)
	switch {
	case p.ctl.List.Has(pk.Name):
		b.SetIconName("object-select-symbolic")
		b.SetTooltipText("In the list. Click to take it out.")
		b.ConnectClicked(func() { p.ctl.List.Remove(pk.Name); p.changed() })
		return b
	case why != "":
		b.SetSensitive(false)
		b.SetTooltipText(why)
		return b
	}
	if err := p.ctl.List.CanAdd(pk, mode); err != nil {
		b.SetSensitive(false)
		b.SetTooltipText(err.Error())
		return b
	}
	b.SetTooltipText(tip)
	b.ConnectClicked(func() { p.add(pk, mode) })
	return b
}

// add puts pk in the list, saying why when it can't go.
func (p *pkgPage) add(pk pkgdb.Package, mode pkgview.Mode) {
	if err := p.ctl.Add(pk, mode); err != nil {
		p.w.toast(err.Error())
	}
}

func (p *pkgPage) refreshInstalled() {
	v := p.ctl.Installed
	for i, vw := range pkgview.Views {
		n := v.Count(vw)
		p.viewBtns[i].SetLabel(fmt.Sprintf("%s %d", pkgview.ViewTitle(vw), n))
		if v.View() == vw && !p.viewBtns[i].Active() {
			p.viewBtns[i].SetActive(true)
		}
	}
	note := ""
	if v.View() == pkgdb.ViewOrphans {
		note = pkgview.OrphansNote(v.Count(pkgdb.ViewOrphans))
	}
	p.orphanNote.SetVisible(note != "")
	p.orphanText.SetText(note)
	rows, total := v.Rows()
	p.instList.RemoveAll()
	if len(rows) == 0 {
		msg := "Nothing is installed here."
		if v.Filter() != "" {
			msg = fmt.Sprintf("No installed package matches %q.", v.Filter())
		}
		p.noneLabel.SetText(msg)
		p.listStack.SetVisibleChildName("none")
		return
	}
	for _, r := range rows {
		p.instList.Append(p.installedRow(r))
	}
	if total > len(rows) {
		p.instList.Append(noteRow(fmt.Sprintf("Showing %d of %d. Filter to narrow it down.", len(rows), total), false))
	}
	p.markSelected(p.instList)
	p.listStack.SetVisibleChildName("installed")
}

// installedRow is a result row with the size in front of the description
// and the remove button; a package that can't be removed is greyed with
// the reason.
func (p *pkgPage) installedRow(r pkgview.InstalledRow) *gtk.ListBoxRow {
	why := ""
	if !r.Removable {
		why = r.Why
	}
	pk := r.Pkg
	row := p.resultRow(pk, pkgview.ModeRemove, why)
	if why == "" {
		if box, ok := row.Child().(*gtk.Box); ok {
			if text, ok := box.FirstChild().(*gtk.Box); ok {
				if last, ok := text.LastChild().(*gtk.Label); ok {
					last.SetText(r.Size + " · " + pk.Description)
				}
			}
		}
	}
	return row
}
