package ui

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/actions"
)

// Held updates (#280): the Updates card's "N held ›" link opens this sheet.
// It lists the AUR updates held back for the cooldown (updates-cache.json)
// and the packages an AUR audit holds (audit-state.json), each with the way
// to deal with it. "Review and update" is the Packages page's AUR install
// for that one package (plan, PKGBUILD review, the helper's install);
// nothing here updates anything by itself.

// heldIntro is the sheet's subtitle.
const heldIntro = "These AUR updates are held back on purpose. Updating one now skips that safety window, so you review its PKGBUILD first."

// readHeld rereads the held lists (with the cache) and redraws the open sheet.
func (w *Window) readHeld() {
	w.held = overview.ReadHeld(w.opts.StateDir)
	if w.heldFill != nil {
		w.heldFill()
	}
}

// heldCooldown is the cooldown rows that count: only a current cache says
// what is held (the card's rule).
func (w *Window) heldCooldown() []overview.HeldPkg {
	if !w.updU.Fresh(time.Now()) {
		return nil
	}
	return w.held.Cooldown
}

// openHeld is the link's click: the sheet over the window.
func (w *Window) openHeld() {
	if w.heldSheet != nil || w.closed {
		return
	}
	d := newWideDialog("Held updates", wideText)
	d.AddResponse("close", "Close")
	d.SetCloseResponse("close")
	d.SetDefaultResponse("close")
	d.add(dialogText(heldIntro, "am-note"))
	rows := gtk.NewBox(gtk.OrientationVertical, 0)
	rows.AddCSSClass("am-health-rows")
	sc := gtk.NewScrolledWindow()
	sc.SetChild(rows)
	// A scrollbar beside the rows, not over their buttons (#275).
	sc.SetOverlayScrolling(false)
	d.capList(sc, healthMaxHeight)
	d.add(sc)

	w.heldFill = func() {
		for c := gtk.BaseWidget(rows).FirstChild(); c != nil; c = gtk.BaseWidget(rows).FirstChild() {
			rows.Remove(c)
		}
		cool, aud := w.heldCooldown(), w.held.Audit
		section := func(title string) {
			head := gtk.NewLabel(title)
			head.SetXAlign(0)
			head.AddCSSClass("am-kicker")
			head.AddCSSClass("am-health-section")
			rows.Append(head)
		}
		if len(cool) > 0 {
			section("COOLDOWN · NEWER THAN 72 H")
			for _, h := range cool {
				rows.Append(w.heldRow("held-"+h.Name, h.Tag(), "am-tag-warn", h.Name, h.Detail(), "Review and update",
					func() { w.reviewHeld(h.Name) }))
			}
		}
		if len(aud) > 0 {
			section("HELD BY THE AUR AUDIT")
			for _, a := range aud {
				rows.Append(w.heldRow("audit-"+a.Pkg, "audit", "am-tag-danger", a.Pkg, a.Reason, "Open audit", w.openHeldAudit))
			}
		}
		if len(cool) == 0 && len(aud) == 0 {
			rows.Append(dialogText("Nothing is held back now.", "am-note"))
		}
	}
	d.ConnectClosed(func() { w.heldSheet, w.heldFill = nil, nil })
	w.heldSheet = d
	w.heldFill()
	d.show(w)
}

// heldRow is one row: the tag, the name over its mono line, the button.
func (w *Window) heldRow(id, tagText, class, name, detail, button string, fn func()) gtk.Widgetter {
	row := gtk.NewBox(gtk.OrientationHorizontal, 14)
	row.AddCSSClass("am-health-row")
	row.SetName(id)
	t := tag(tagText, class)
	t.SetVAlign(gtk.AlignStart)
	t.SetSizeRequest(84, -1)
	t.SetXAlign(0.5)
	row.Append(t)

	title := gtk.NewLabel(name)
	title.SetXAlign(0)
	title.SetUseMarkup(false)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.AddCSSClass("am-health-title")
	line := gtk.NewLabel(detail)
	line.SetXAlign(0)
	line.SetUseMarkup(false)
	line.SetEllipsize(pango.EllipsizeEnd)
	line.AddCSSClass("monospace")
	line.AddCSSClass("am-health-detail")
	line.SetTooltipText(detail)
	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	col.Append(title)
	col.Append(line)
	row.Append(col)

	b := gtk.NewButtonWithLabel(button)
	b.SetVAlign(gtk.AlignCenter)
	b.ConnectClicked(fn)
	row.Append(b)
	return row
}

// reviewHeld is "Review and update": the Packages page's AUR install review
// for exactly this package. The sheet closes first, so the plan and the
// PKGBUILD review are what the window shows. Nothing is skipped: the
// review's checkbox is what lets the install go on.
func (w *Window) reviewHeld(name string) {
	switch {
	case w.pkgs == nil || w.pkgs.ctl == nil:
		w.toast("Installing AUR packages isn't available in this build.")
		return
	case w.busy:
		w.toast("A run is going. Review it when the run is done.")
		return
	}
	w.pkgs.ctl.RefreshUpdates()
	w.heldRun = name
	if err := w.pkgs.ctl.ReviewAUR(name); err != nil {
		w.heldRun = ""
		switch err {
		case pkgview.ErrBlocked:
			w.toast("Repository updates are waiting. Run topgrade first, then review it here.")
		case pkgview.ErrSheetOpen:
			w.toast("Another review is open. Finish it first.")
		default:
			w.toast("Couldn't start the review.")
		}
		return
	}
	w.closeHeld()
}

// openHeldAudit is "Open audit": the existing Audit AUR packages action.
func (w *Window) openHeldAudit() {
	if w.busy {
		w.toast("A run is going. Open the audit when the run is done.")
		return
	}
	w.closeHeld()
	w.startAction(actions.IDAURAudit)
}

// closeHeld takes the sheet down.
func (w *Window) closeHeld() {
	if d := w.heldSheet; d != nil {
		w.heldSheet, w.heldFill = nil, nil
		d.ForceClose()
	}
}

// heldRunEnded: a held update's install ran to its end, so check for updates
// again and the held list follows (the card's own check; nothing to do when
// there is none, or when the review was only closed).
func (w *Window) heldRunEnded() {
	name := w.heldRun
	w.heldRun = ""
	if name == "" || !w.queued || w.current != actions.IDPackages {
		return
	}
	post(w.checkUpdates)
}
