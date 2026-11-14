package ui

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Ignoring in the System health sheet (#288, the backend is #287): a Look
// row has an "Ignore" button, per item when the row has several; ignored
// items are listed under IGNORED at the bottom with an "Undo". Both write
// the ignore list through the Source (healthview.Ignorer; the real one is
// health.IgnoreStore), off the main thread, then run the checks again.

// ignoreTip is the Ignore button's tooltip.
const ignoreTip = "Stop counting this until something new shows up here"

// ignoreItems are the keys an Ignore of check c is about: its active items,
// or the check itself when it came with none (the backend's rule).
func ignoreItems(c healthview.Check) []healthview.Item {
	if len(c.Items) > 0 {
		return c.Items
	}
	return []healthview.Item{{Key: c.ID, Label: c.Title}}
}

// ignoreButton is the row's Ignore: a button for one item, a button with a
// popover (each item with its own Ignore, and Ignore all) for several. Nil
// for a row that cannot be ignored (not Look, or a Source without a list).
func (w *Window) ignoreButton(c healthview.Check) gtk.Widgetter {
	if c.State != healthview.Look {
		return nil
	}
	if _, ok := w.health.src.(healthview.Ignorer); !ok {
		return nil
	}
	items := ignoreItems(c)
	if len(items) == 1 {
		b := gtk.NewButtonWithLabel("Ignore")
		b.AddCSSClass("flat")
		b.SetVAlign(gtk.AlignCenter)
		b.SetTooltipText(ignoreTip)
		key := items[0].Key
		b.ConnectClicked(func() { w.ignoreKeys([]string{key}, false) })
		return b
	}
	mb := gtk.NewMenuButton()
	mb.SetLabel("Ignore")
	mb.AddCSSClass("flat")
	mb.SetVAlign(gtk.AlignCenter)
	mb.SetTooltipText(ignoreTip)
	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("am-ignore-pop")
	box.SetName("ignore-pop-" + c.ID)
	head := gtk.NewLabel("Ignore which?")
	head.SetXAlign(0)
	head.AddCSSClass("am-note")
	box.Append(head)
	var all []string
	for _, it := range items {
		all = append(all, it.Key)
		line := gtk.NewBox(gtk.OrientationHorizontal, 12)
		lab := gtk.NewLabel(it.Label)
		lab.SetXAlign(0)
		lab.SetHExpand(true)
		lab.SetUseMarkup(false)
		lab.SetEllipsize(pango.EllipsizeEnd)
		lab.SetMaxWidthChars(36)
		lab.SetTooltipText(it.Label)
		one := gtk.NewButtonWithLabel("Ignore")
		one.AddCSSClass("flat")
		key := it.Key
		one.ConnectClicked(func() { pop.Popdown(); w.ignoreKeys([]string{key}, false) })
		line.Append(lab)
		line.Append(one)
		box.Append(line)
	}
	every := gtk.NewButtonWithLabel("Ignore all")
	every.AddCSSClass("flat")
	every.SetHAlign(gtk.AlignEnd)
	every.ConnectClicked(func() { pop.Popdown(); w.ignoreKeys(all, false) })
	box.Append(every)
	pop.SetChild(box)
	mb.SetPopover(pop)
	return mb
}

// ignoredSection appends the IGNORED section: every ignored item with its
// check, when it was ignored and an Undo. Nothing when none is.
func (w *Window) ignoredSection(rows *gtk.Box, r *healthview.Report) {
	list := r.IgnoredRows()
	if len(list) == 0 {
		return
	}
	_, canUndo := w.health.src.(healthview.Ignorer)
	head := gtk.NewLabel("IGNORED")
	head.SetXAlign(0)
	head.SetName("section-Ignored")
	head.AddCSSClass("am-kicker")
	head.AddCSSClass("am-health-section")
	head.SetTooltipText("Not counted until something new shows up in the same check")
	rows.Append(head)
	now := time.Now()
	for _, it := range list {
		row := gtk.NewBox(gtk.OrientationHorizontal, 14)
		row.AddCSSClass("am-health-row")
		row.AddCSSClass("am-health-ignored")
		row.SetName("ignored-" + it.Item.Key)
		title := gtk.NewLabel(it.Title)
		title.SetXAlign(0)
		title.SetUseMarkup(false)
		title.AddCSSClass("am-health-title")
		since := "ignored"
		if !it.Since.IsZero() {
			since += " " + healthview.Ago(it.Since, now)
		}
		line := gtk.NewLabel(it.Item.Label + " · " + since)
		line.SetXAlign(0)
		line.SetUseMarkup(false)
		line.SetEllipsize(pango.EllipsizeEnd)
		line.AddCSSClass("monospace")
		line.AddCSSClass("am-health-detail")
		line.SetTooltipText(it.Item.Label)
		col := gtk.NewBox(gtk.OrientationVertical, 2)
		col.SetHExpand(true)
		col.Append(title)
		col.Append(line)
		row.Append(col)
		if canUndo {
			undo := gtk.NewButtonWithLabel("Undo")
			undo.AddCSSClass("flat")
			undo.SetVAlign(gtk.AlignCenter)
			undo.SetTooltipText("Count this again")
			key := it.Item.Key
			undo.ConnectClicked(func() { w.ignoreKeys([]string{key}, true) })
			row.Append(undo)
		}
		rows.Append(row)
	}
}

// ignoreKeys puts keys on the ignore list (undo: takes them off), off the
// main thread, then runs the checks again. A failure says so in a toast and
// leaves the sheet as it is (a run only follows if something was saved).
func (w *Window) ignoreKeys(keys []string, undo bool) {
	ig, ok := w.health.src.(healthview.Ignorer)
	if !ok || len(keys) == 0 || w.closed {
		return
	}
	go func() {
		saved := 0
		var err error
		for _, k := range keys {
			if undo {
				err = ig.Unignore(k)
			} else {
				err = ig.Ignore(k)
			}
			if err != nil {
				break
			}
			saved++
		}
		post(func() {
			if w.closed {
				return
			}
			if err != nil {
				w.toast("Couldn't save the ignore list: " + sanitize.Clip(sanitize.OneLine(err.Error()), 100))
			}
			if saved > 0 {
				w.refreshHealth()
			}
		})
	}()
}
