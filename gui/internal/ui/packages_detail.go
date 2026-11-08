package ui

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// refreshDetail draws the detail pane: the hero cell (corner marks), the
// actions, the stats, the dependencies.
func (p *pkgPage) refreshDetail() {
	d := p.ctl.Search.Detail()
	if !d.Valid {
		p.detailStack.SetVisibleChildName("none")
		return
	}
	clearBox(p.detail)
	pk := d.Pkg
	p.selected = pk.Name

	// The hero: kicker (repo), name, version, description.
	hero := gtk.NewBox(gtk.OrientationVertical, 8)
	hero.AddCSSClass("am-hero")
	hero.AddCSSClass("am-blueprint")
	hero.AddCSSClass("am-over-grid") // glass.css: on the stat grid's edges
	kind := "Repository · " + repoLabel(pk)
	if d.AUR {
		kind = "AUR"
	}
	hero.Append(plainLabel(strings.ToUpper(kind), "am-kicker"))
	title := plainLabel(pk.Name, "am-hero-title")
	hero.Append(title)
	hero.Append(plainLabel(shownVersion(pk), "am-pkg-ver"))
	if pk.Description != "" {
		hero.Append(plainLabel(pk.Description, "am-hero-desc"))
	}
	tags := gtk.NewFlowBox()
	tags.SetSelectionMode(gtk.SelectionNone)
	tags.SetColumnSpacing(6)
	tags.SetRowSpacing(6)
	tags.SetMaxChildrenPerLine(8)
	tags.SetHAlign(gtk.AlignStart)
	tags.SetFocusable(false)
	tags.AddCSSClass("am-card-buttons") // no child padding: the tags start at the hero's text
	for _, t := range pkgview.RowTags(pk) {
		tags.Append(pkgTag(t))
	}
	if d.AUR {
		tags.Append(tag(d.Notice, "am-tag-warn"))
	}
	hero.Append(tags)
	p.detail.Append(hero)

	p.detail.Append(p.detailActions(d))
	if d.Loading {
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		row.Append(adw.NewSpinner())
		row.Append(plainLabel("Loading the AUR details…", "am-note"))
		row.AddCSSClass("am-over-grid-row")
		p.detail.Append(row)
	}
	if d.Err != "" {
		p.detail.Append(overGrid(plainLabel("Couldn't load the AUR details: "+d.Err, "am-note")))
	}

	stats := append([]pkgview.Stat(nil), d.Stats...)
	stats = append(stats, d.AURRows...)
	p.detail.Append(statsGrid(stats))
	if d.Flagged != "" {
		f := tag(d.Flagged, "am-tag-warn")
		f.SetHAlign(gtk.AlignStart)
		p.detail.Append(overGrid(f))
	}
	if d.Link != "" {
		p.detail.Append(overGrid(plainLabel(d.Link, "am-note")))
	}
	if len(d.Deps) > 0 {
		p.detail.Append(p.depList("Dependencies", d.Deps))
	}
	if len(d.Opt) > 0 {
		p.detail.Append(p.depList("Optional dependencies", d.Opt))
	}
	p.detailStack.SetVisibleChildName("detail")
}

func repoLabel(pk pkgdb.Package) string {
	if pk.Repo == "" {
		return "none"
	}
	return pk.Repo
}

// detailActions are Install and Add to list, or Remove and Add to the
// removal list, with the reason when the package can't go in.
func (p *pkgPage) detailActions(d pkgview.Detail) gtk.Widgetter {
	pk := d.Pkg
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	row := gtk.NewFlowBox()
	row.SetSelectionMode(gtk.SelectionNone)
	row.SetColumnSpacing(8)
	row.SetRowSpacing(8)
	row.SetMaxChildrenPerLine(3)
	row.SetHAlign(gtk.AlignStart)
	row.SetFocusable(false)
	// No child padding: Install starts where the hero starts. The whole
	// block is on the stat grid's edges (am-over-grid-row, below).
	row.AddCSSClass("am-card-buttons")
	box.AddCSSClass("am-over-grid-row")
	box.Append(row)

	mode := pkgview.ModeInstall
	if pk.Installed {
		mode = pkgview.ModeRemove
	}
	var primary, secondary *gtk.Button
	note := ""
	if mode == pkgview.ModeInstall {
		primary = gtk.NewButtonWithLabel("Install")
		primary.AddCSSClass("suggested-action")
		primary.AddCSSClass("am-blueprint")
		secondary = gtk.NewButtonWithLabel("Add to list")
	} else {
		primary = gtk.NewButtonWithLabel("Remove")
		primary.AddCSSClass("destructive-action")
		secondary = gtk.NewButtonWithLabel("Add to removal list")
	}
	err := p.ctl.List.CanAdd(pk, mode)
	if p.ctl.List.Has(pk.Name) {
		err = nil
	}
	if pk.Installed && pk.Update {
		note = "An update is waiting. Updates come with the system."
	}
	if err != nil {
		primary.SetSensitive(false)
		secondary.SetSensitive(false)
		note = err.Error()
		if pkgview.ErrKind(err) == pkgview.AddAlreadyInstalled && pk.Update {
			note = "An update is waiting. Updates come with the system."
		}
	}
	g := p.ctl.Gating(mode)
	if g.Blocked {
		primary.SetSensitive(false)
		note = g.Text + ". Update the system before installing."
	}
	primary.ConnectClicked(func() { p.now(pk, mode) })
	secondary.ConnectClicked(func() { p.add(pk, mode) })
	if p.ctl.List.Has(pk.Name) {
		secondary.SetLabel("In the list")
		secondary.SetSensitive(false)
	}
	row.Append(primary)
	row.Append(secondary)
	for c := row.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		gtk.BaseWidget(c).SetFocusable(false)
	}
	if g.Blocked {
		upd := gtk.NewButtonWithLabel("Update system first")
		upd.ConnectClicked(func() { p.ctl.UpdateFirst() })
		row.Append(upd)
	}
	if note != "" {
		box.Append(plainLabel(note, "am-note"))
	}
	if err != nil && clearable(err) {
		clr := gtk.NewButtonWithLabel("Clear the list and add this")
		clr.SetHAlign(gtk.AlignStart)
		clr.ConnectClicked(func() { p.clearAndAdd(pk, mode) })
		box.Append(clr)
	}
	return box
}

// now is Install / Remove on the detail: straight to the sheet.
func (p *pkgPage) now(pk pkgdb.Package, mode pkgview.Mode) {
	if p.w.busy {
		p.w.toast("A run is going. Wait for it to finish.")
		return
	}
	if err := p.ctl.Now(pk, mode); err != nil && err != pkgview.ErrBlocked {
		p.w.toast(err.Error())
	}
}

// overGrid puts a block of the detail column on the stat grid's edges: in
// the glass look the grid's cells are 4 px inside the column, and every
// block under or over them is inset to match (glass.css, #262).
func overGrid[W gtk.Widgetter](w W) W {
	gtk.BaseWidget(w).AddCSSClass("am-over-grid-row")
	return w
}

// statsGrid is the hairline grid of label/value cells (two across when the
// page is narrow, up to four wide).
func statsGrid(stats []pkgview.Stat) gtk.Widgetter {
	fb := gtk.NewFlowBox()
	fb.SetSelectionMode(gtk.SelectionNone)
	fb.SetHomogeneous(true)
	fb.SetMinChildrenPerLine(2)
	fb.SetMaxChildrenPerLine(4)
	fb.SetColumnSpacing(1)
	fb.SetRowSpacing(1)
	fb.SetFocusable(false)
	fb.AddCSSClass("am-grid")
	for _, s := range stats {
		cell := gtk.NewBox(gtk.OrientationVertical, 2)
		cell.AddCSSClass("am-cell")
		cell.Append(plainLabel(strings.ToUpper(s.Label), "am-kicker"))
		v := plainLabel(s.Value, "am-pkg-stat")
		cell.Append(v)
		fb.Append(cell)
	}
	return fb
}

// depList is a titled list of dependencies; one that installing brings in
// is tagged "pulled in", an optional one that is installed "installed".
func (p *pkgPage) depList(title string, deps []pkgview.DepRow) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.AddCSSClass("am-pkg-deplist")
	box.AddCSSClass("am-over-grid-row")
	box.Append(plainLabel(fmt.Sprintf("%s · %d", title, len(deps)), "am-kicker"))
	for _, d := range deps {
		name := plainLabel(d.Raw, "am-pkg-dep")
		var tags []gtk.Widgetter
		if d.Build {
			tags = append(tags, tag("build", "am-tag-neutral"))
		}
		switch {
		case d.PulledIn && title == "Dependencies":
			tags = append(tags, tag("pulled in", "am-tag-warn"))
		case d.Installed:
			tags = append(tags, tag("installed", "am-tag-ok"))
		}
		line := newTagLine(name, 8, tags...)
		line.tags.SetVisible(len(tags) > 0)
		line.setUnder(p.w.narrow)
		p.lines = append(p.lines, line)
		item := gtk.NewBox(gtk.OrientationVertical, 1)
		item.AddCSSClass("am-pkg-depline")
		item.Append(line.box)
		if d.Desc != "" {
			item.Append(plainLabel(d.Desc, "am-note"))
		}
		box.Append(item)
	}
	return box
}

// ---- the list tray ----

func (p *pkgPage) buildTray() {
	p.trayKicker = plainLabel("", "am-kicker")
	p.trayNames = ellipsized("", "am-pkg-trayname")
	p.trayNote = plainLabel("", "am-note")
	p.trayConfig = gtk.NewCheckButtonWithLabel("Also delete config files")
	p.trayConfig.ConnectToggled(func() {
		if !p.traySyncing {
			p.ctl.SetWithConfig(p.trayConfig.Active())
		}
	})
	p.trayClear = gtk.NewButtonWithLabel("Clear")
	p.trayClear.ConnectClicked(func() { p.ctl.List.Clear(); p.changed() })
	p.trayReview = gtk.NewButtonWithLabel("Review and install")
	p.trayReview.AddCSSClass("suggested-action")
	p.trayReview.ConnectClicked(func() {
		if p.w.busy {
			p.w.toast("A run is going. Wait for it to finish.")
			return
		}
		if err := p.ctl.Review(); err != nil && err != pkgview.ErrBlocked {
			p.w.toast(err.Error())
		}
	})
	p.trayUpdate = gtk.NewButtonWithLabel("Update system first")
	p.trayUpdate.ConnectClicked(func() { p.ctl.UpdateFirst() })

	left := gtk.NewBox(gtk.OrientationVertical, 2)
	left.SetHExpand(true)
	left.Append(p.trayKicker)
	left.Append(p.trayNames)
	left.Append(p.trayNote)
	left.Append(p.trayConfig)
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	buttons.SetHAlign(gtk.AlignEnd)
	buttons.SetVAlign(gtk.AlignCenter)
	buttons.Append(p.trayClear)
	buttons.Append(p.trayUpdate)
	buttons.Append(p.trayReview)
	bf := gtk.NewFlowBox()
	bf.SetSelectionMode(gtk.SelectionNone)
	bf.SetMaxChildrenPerLine(2)
	bf.SetFocusable(false)
	bf.Append(left)
	bf.Append(buttons)
	gtk.BaseWidget(bf.FirstChild()).SetFocusable(false)

	p.tray = gtk.NewBox(gtk.OrientationVertical, 0)
	p.tray.AddCSSClass("am-tray")
	p.tray.Append(bf)
	p.tray.SetVisible(false)
}

func (p *pkgPage) refreshTray() {
	l := p.ctl.List
	p.tray.SetVisible(l.Len() > 0)
	if l.Len() == 0 {
		return
	}
	remove := l.Mode() == pkgview.ModeRemove
	p.tray.RemoveCSSClass("am-tray-remove")
	p.trayKicker.RemoveCSSClass("am-tone-danger")
	if remove {
		p.tray.AddCSSClass("am-tray-remove")
		p.trayKicker.AddCSSClass("am-tone-danger")
		p.trayKicker.SetText(fmt.Sprintf("REMOVAL LIST · %d", l.Len()))
		p.trayReview.SetLabel("Review and remove")
		p.trayReview.RemoveCSSClass("suggested-action")
		p.trayReview.AddCSSClass("destructive-action")
		p.trayNote.SetText("Removals only. Installs are a separate run.")
	} else {
		p.trayKicker.SetText(fmt.Sprintf("INSTALL LIST · %d", l.Len()))
		p.trayReview.SetLabel("Review and install")
		p.trayReview.RemoveCSSClass("destructive-action")
		p.trayReview.AddCSSClass("suggested-action")
		p.trayNote.SetText("Installs only. Removals are a separate run.")
	}
	p.trayNames.SetText(strings.Join(l.Names(), ", "))
	p.trayConfig.SetVisible(remove)
	p.traySyncing = true
	p.trayConfig.SetActive(remove && l.WithConfig)
	p.traySyncing = false
	g := p.ctl.Gating(l.Mode())
	p.trayReview.SetSensitive(!g.Blocked)
	p.trayUpdate.SetVisible(g.Blocked)
	if g.Blocked {
		p.trayNote.SetText(g.Text + ". Installing waits for the update.")
	}
}

// updatePkgBadge shows the list's size on the sidebar's Packages row.
func (w *Window) updatePkgBadge() {
	if w.shell == nil || w.shell.pkgBadge == nil || w.pkgs == nil || w.pkgs.ctl == nil {
		return
	}
	n := w.pkgs.ctl.List.Len()
	w.shell.pkgBadge.SetText(fmt.Sprint(n))
	w.shell.pkgBadge.SetVisible(n > 0)
}
