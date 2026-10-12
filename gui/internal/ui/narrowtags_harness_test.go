package ui

// Where the tags sit (the owner's r532 screenshot at ~380 px: the plan's
// "root" tags at the line's end on some rows and under the title on
// others, by whether the title happened to fit). Under the narrow
// breakpoint every row's tags are on their own line under its title,
// starting where the title does; above it they are at the line's end, the
// same on every row. No tag is drawn taller than its own height (Dev 5's
// VM screenshot: a root tag beside a 3-line state line was 3 lines high).
// The hero's two buttons are stacked at full width when narrow. Checked in
// the min-width children (real-narrow, narrow-states) at each width.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/actions"
)

// narrowPx is narrowWidth in px (the tests run at text scale 1).
const narrowPx = 760

// box is a widget's bounds in root's coordinates.
type box struct{ x, y, w, h float64 }

func boundsIn(wd, root gtk.Widgetter) (box, bool) {
	r, ok := gtk.BaseWidget(wd).ComputeBounds(root)
	if !ok {
		return box{}, false
	}
	return box{float64(r.X()), float64(r.Y()), float64(r.Width()), float64(r.Height())}, true
}

// shownLabels are the labels under root that are drawn, in tree order.
func shownLabels(root gtk.Widgetter) []*gtk.Label {
	var out []*gtk.Label
	eachWidget(root, func(wd gtk.Widgetter) {
		if l, ok := wd.(*gtk.Label); ok && l.IsDrawable() && l.Text() != "" {
			out = append(out, l)
		}
	})
	return out
}

// tagRows are the rows whose tags are checked: the hero's plan and the
// Run page's step headers that are drawn.
func tagRows(w *Window) map[string][]gtk.Widgetter {
	rows := map[string][]gtk.Widgetter{}
	for ch := gtk.BaseWidget(w.heroSteps).FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
		if gtk.BaseWidget(ch).IsDrawable() {
			rows["hero"] = append(rows["hero"], ch)
		}
	}
	if w.tl != nil {
		for _, h := range w.tl.headers {
			if h.sec != nil && h.box.IsDrawable() {
				rows["run"] = append(rows["run"], h.box)
			}
		}
	}
	return rows
}

// checkTags checks the tags' places on the hero's plan and the Run page's
// drawn step headers, on the action cells and the hero's buttons, at the
// window's width; moment names the state in a failure. It returns how
// many tag lines it checked.
func checkTags(c *covEnv, moment string) int {
	w := c.w
	narrow := w.win.Width() <= narrowPx
	checked := 0
	for kind, rows := range tagRows(w) {
		checked += checkTagRows(c, moment, kind, rows, narrow)
	}
	return checked + checkCellTags(c, moment, narrow)
}

// dialogNarrowPx is the wide dialogs' breakpoint (wideNarrow) in px.
const dialogNarrowPx = 640

// checkRailTags checks the tags of the gate's rail in d as checkTags
// does the plan's rows: under the dialog's breakpoint (its sheet under
// 640 px: the bottom sheet in a narrow window) under each title, above it
// at the line's end. It returns how many rows it checked.
func checkRailTags(c *covEnv, d *amDialog, moment string) int {
	var rows []gtk.Widgetter
	var sheet gtk.Widgetter
	eachWidget(d, func(wd gtk.Widgetter) {
		b := gtk.BaseWidget(wd)
		if sheet == nil && b.CSSName() == "sheet" {
			sheet = wd
		}
		if b.HasCSSClass("am-rail-step") && b.IsDrawable() {
			rows = append(rows, wd)
		}
	})
	if sheet == nil {
		c.fail("%s: the gate has no sheet", moment)
		return 0
	}
	narrow := gtk.BaseWidget(sheet).Width() < dialogNarrowPx
	return checkTagRows(c, moment, "gate", rows, narrow)
}

// checkTagRows checks rows' tags (kind names them in a failure): under
// each title when narrow, else at the line's end, the same distance from
// it on every row; no tag drawn taller than its own height.
func checkTagRows(c *covEnv, moment, kind string, rows []gtk.Widgetter, narrow bool) int {
	checked := 0
	var ends []float64
	var endsOf []string
	for _, row := range rows {
		labels := shownLabels(row)
		// The title is the label after the step's number.
		ni := slices.IndexFunc(labels, func(l *gtk.Label) bool { return l.HasCSSClass("am-step-num") })
		if ni < 0 || ni+1 >= len(labels) {
			continue
		}
		title := labels[ni+1]
		var tags []*gtk.Label
		for _, l := range labels[ni+2:] {
			if l.HasCSSClass("am-tag") || l.HasCSSClass("am-step-sub") {
				tags = append(tags, l)
			}
		}
		if len(tags) == 0 {
			continue
		}
		checked++
		tb, ok := boundsIn(title, row)
		if !ok {
			c.fail("%s: %s row %q: no bounds", moment, kind, title.Text())
			continue
		}
		first, _ := boundsIn(tags[0], row)
		right := 0.0
		for _, t := range tags {
			b, _ := boundsIn(t, row)
			right = max(right, b.x+b.w)
			if narrow {
				c.ok(b.y >= tb.y+tb.h-1, "%s: %s row %q: tag %q at y %.0f, beside the title (y %.0f-%.0f), not under it",
					moment, kind, title.Text(), t.Text(), b.y, tb.y, tb.y+tb.h)
			} else {
				c.ok(b.y < tb.y+tb.h && b.x >= tb.x+tb.w, "%s: %s row %q: tag %q at %.0f,%.0f, not beside the title (%.0f,%.0f %.0fx%.0f)",
					moment, kind, title.Text(), t.Text(), b.x, b.y, tb.x, tb.y, tb.w, tb.h)
			}
			if t.HasCSSClass("am-tag") {
				// Height doesn't count the padding and border its
				// natural height does, so a tag at its own height is
				// a few px under it.
				_, nat, _, _ := t.Measure(gtk.OrientationVertical, -1)
				c.ok(t.Height() <= nat, "%s: %s row %q: tag %q drawn %d px high, its own height is %d px",
					moment, kind, title.Text(), t.Text(), t.Height(), nat)
			}
		}
		if narrow {
			c.ok(abs(first.x-tb.x) <= 1, "%s: %s row %q: the tags start at x %.0f, the title at %.0f",
				moment, kind, title.Text(), first.x, tb.x)
		}
		// How far the tags end from the row's end (a running or
		// failed step's header is a few px narrower: its border).
		gap := float64(gtk.BaseWidget(row).Width()) - right
		ends = append(ends, gap)
		endsOf = append(endsOf, fmt.Sprintf("%.16q %.0f px", title.Text(), gap))
	}
	if !narrow && len(ends) > 0 {
		lo, hi := slices.Min(ends), slices.Max(ends)
		c.ok(hi-lo <= 1, "%s: %s: the tags end %.0f to %.0f px before the row's end, not at the same line end: %q", moment, kind, lo, hi, endsOf)
	}
	return checked
}

// checkCellTags checks the action cells' root tags and the hero's
// buttons (checkTags).
func checkCellTags(c *covEnv, moment string, narrow bool) int {
	w := c.w
	checked := 0
	// The action cells' root tags: on the icon line above the title when
	// wide, under the title when narrow, as the plan's.
	for ch := gtk.BaseWidget(w.cells).FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
		var title *gtk.Label
		var roots []*gtk.Label
		for _, l := range shownLabels(ch) {
			switch {
			case l.HasCSSClass("am-cell-title"):
				title = l
			case l.HasCSSClass("am-tag-root"):
				roots = append(roots, l)
			}
		}
		if title == nil || len(roots) == 0 {
			continue
		}
		checked++
		c.ok(len(roots) == 1, "%s: cell %q shows %d root tags", moment, title.Text(), len(roots))
		tb, _ := boundsIn(title, ch)
		b, _ := boundsIn(roots[0], ch)
		if narrow {
			c.ok(b.y >= tb.y+tb.h-1 && abs(b.x-tb.x) <= 1, "%s: cell %q: root tag at %.0f,%.0f, not under the title (%.0f,%.0f %.0fx%.0f)",
				moment, title.Text(), b.x, b.y, tb.x, tb.y, tb.w, tb.h)
		} else {
			c.ok(b.y+b.h <= tb.y+1, "%s: cell %q: root tag at y %.0f, not on the line above the title (y %.0f)", moment, title.Text(), b.y, tb.y)
		}
		_, nat, _, _ := roots[0].Measure(gtk.OrientationVertical, -1)
		c.ok(roots[0].Height() <= nat, "%s: cell %q: root tag drawn %d px high, its own height is %d px", moment, title.Text(), roots[0].Height(), nat)
	}

	// The hero's Run and Dry run first: stacked at the hero's full width
	// when narrow, side by side when wide.
	if run := w.menu[actions.IDFullCleanup]; run != nil && run.cell.IsDrawable() && w.heroSteps.IsDrawable() {
		hero := gtk.BaseWidget(w.heroSteps).Parent()
		rb, _ := boundsIn(run.cell, hero)
		db, _ := boundsIn(w.dryFirst, hero)
		sb, _ := boundsIn(w.heroSteps, hero)
		if narrow {
			c.ok(rb.x == db.x && rb.w == db.w && db.y > rb.y, "%s: the hero's buttons aren't stacked at one width: Run %+v, Dry run first %+v", moment, rb, db)
			// Inside a flow box child's 3 px padding, as the plan's rows.
			c.ok(rb.x <= sb.x+4 && rb.x+rb.w >= sb.x+sb.w-4, "%s: the hero's Run is %+v, the hero's content %+v: not full width", moment, rb, sb)
			// Nothing under them but the hero's padding (20 px) and the
			// flow box child's (3 px): no line for a hidden spinner.
			hb, _ := boundsIn(hero, hero)
			c.ok(hb.y+hb.h-(db.y+db.h) <= 26, "%s: %.0f px under the hero's Dry run first (%+v) to its end (%+v)", moment, hb.y+hb.h-(db.y+db.h), db, hb)
		} else {
			c.ok(rb.y == db.y && db.x > rb.x, "%s: the hero's buttons aren't side by side: Run %+v, Dry run first %+v", moment, rb, db)
		}
	}
	return checked
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// noNamesTwice: a dialog with an item list names each item once (its body
// doesn't repeat the list).
func noNamesTwice(c *covEnv, moment string, labels, items []string) {
	for _, it := range items {
		// The body shortens long names, so look for their start.
		head := []rune(strings.Trim(it, `"`))
		if len(head) > 24 {
			head = head[:24]
		}
		n := 0
		for _, l := range labels {
			n += strings.Count(l, string(head))
		}
		c.ok(n == 1, "%s: %q is shown %d times: %q", moment, string(head), n, labels)
	}
}
