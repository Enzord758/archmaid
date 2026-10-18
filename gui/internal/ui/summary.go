package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
)

// The Run page's finished state (the owner's variant C of #187, task
// #191): once a real run has ended with a report (engine.RunReport, kept
// by the model), the page opens with a hero card (the kicker, one
// headline, the sub line, the things that need the user), then the
// Packages and the Space freed columns (one column when narrow), the
// audit line, and below them "Steps and output" folded: the timeline is
// the same widget as while the run went, one click away. Without a report
// (a dry run, an action that is no queue, a Driver with no report
// configuration, a report with no data) the page is what it always was:
// the timeline alone.
//
// runview.Summary says what is written; this file only draws it. Nothing
// here runs a command: the one button, on a .pacnew/.pacsave row, copies
// `sudo pacdiff` to the clipboard.

// outputMinHeight is how tall the unfolded output is at least (the page
// scrolls around it, so a low window still reaches all of the summary).
const outputMinHeight = 420

// summaryView is the Run page's body: the summary above the folded
// output.
type summaryView struct {
	scroll *gtk.ScrolledWindow // the page's scroll around the body
	body   *gtk.Box
	box    *gtk.Box // the summary, rebuilt for each report; hidden without one
	fold   *gtk.Box // the "Steps and output" row
	toggle *gtk.ToggleButton
	chev   *gtk.Image

	shown    bool // a summary is up
	expanded bool // the output is unfolded (only while shown)

	// What the harness reads.
	cols   *gtk.Grid // the two columns, nil without them
	bars   []*gtk.LevelBar
	copies []*gtk.Button
	hero   *gtk.Box
	last   *runview.Summary
}

// newSummaryView builds the page's body around the timeline's scroll out
// (which stays one widget, shown and hidden, never moved).
func (w *Window) newSummaryView(out *gtk.ScrolledWindow) *summaryView {
	s := &summaryView{}
	s.box = gtk.NewBox(gtk.OrientationVertical, 0)
	s.box.AddCSSClass("am-summary-body")
	s.box.SetVisible(false)

	s.toggle = gtk.NewToggleButton()
	s.toggle.AddCSSClass("flat")
	s.toggle.AddCSSClass("am-fold")
	s.chev = gtk.NewImageFromIconName("pan-end-symbolic")
	title := gtk.NewLabel("Steps and output")
	title.SetXAlign(0)
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.Append(s.chev)
	row.Append(title)
	s.toggle.SetChild(row)
	s.toggle.ConnectToggled(func() { w.foldOutput(s.toggle.Active()) })
	s.fold = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.fold.AddCSSClass("am-fold-row")
	s.fold.SetHAlign(gtk.AlignFill)
	s.fold.Append(s.toggle)
	s.fold.SetVisible(false)

	s.body = gtk.NewBox(gtk.OrientationVertical, 0)
	s.body.Append(s.box)
	s.body.Append(s.fold)
	s.body.Append(out)
	s.scroll = gtk.NewScrolledWindow()
	s.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyNever)
	s.scroll.SetVExpand(true)
	s.scroll.SetHExpand(true)
	s.scroll.SetChild(s.body)
	return s
}

// foldOutput unfolds or folds the output below the summary.
func (w *Window) foldOutput(open bool) {
	s := w.sum
	s.expanded = open && s.shown
	w.tl.scroll.SetVisible(s.expanded || !s.shown)
	if s.expanded {
		s.chev.SetFromIconName("pan-down-symbolic")
	} else {
		s.chev.SetFromIconName("pan-end-symbolic")
	}
}

// updateSummary draws the model's summary, or takes it away (the page is
// then the timeline alone).
func (w *Window) updateSummary() {
	s, tl := w.sum, w.tl
	if s == nil {
		return
	}
	sum := w.model.Summary()
	if sum == nil {
		if !s.shown {
			return
		}
		s.shown, s.expanded, s.last = false, false, nil
		s.cols, s.hero, s.bars, s.copies = nil, nil, nil, nil
		clearBox(s.box)
		s.box.SetVisible(false)
		s.fold.SetVisible(false)
		s.toggle.SetActive(false)
		s.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyNever)
		tl.scroll.SetMinContentHeight(0)
		tl.scroll.SetVExpand(true)
		tl.scroll.SetVisible(true)
		return
	}
	s.last = sum
	clearBox(s.box)
	s.cols, s.hero, s.bars, s.copies = nil, nil, nil, nil
	w.drawSummary(sum)
	if !s.shown {
		s.shown, s.expanded = true, false
		s.toggle.SetActive(false)
		s.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		tl.scroll.SetVExpand(false)
		tl.scroll.SetMinContentHeight(outputMinHeight)
		tl.scroll.SetVisible(false)
		s.box.SetVisible(true)
		s.fold.SetVisible(true)
		s.scroll.VAdjustment().SetValue(0)
	}
}

func clearBox(b *gtk.Box) {
	for ch := b.FirstChild(); ch != nil; ch = b.FirstChild() {
		b.Remove(ch)
	}
}

// drawSummary fills s.box from sum.
func (w *Window) drawSummary(sum *runview.Summary) {
	s := w.sum
	s.hero = w.newSummaryHero(sum)
	s.box.Append(s.hero)

	var left, right gtk.Widgetter
	if sum.Packages != nil {
		left = w.newPackagesColumn(sum.Packages)
	}
	if sum.Freed != nil || sum.Cache != "" {
		right = w.newFreedColumn(sum)
	}
	switch {
	case left != nil && right != nil:
		// A grid, not a box: a horizontal box of wrapping labels reports a
		// minimum height far above its natural one (GTK warns).
		s.cols = gtk.NewGrid()
		s.cols.SetColumnSpacing(24)
		gtk.BaseWidget(left).SetHExpand(true)
		gtk.BaseWidget(right).SetHExpand(true)
		s.cols.AddCSSClass("am-summary-cols")
		s.cols.Attach(left, 0, 0, 1, 1)
		s.cols.Attach(right, 1, 0, 1, 1)
		s.box.Append(s.cols)
	case left != nil:
		s.box.Append(left)
	case right != nil:
		s.box.Append(right)
	}
	w.setSummaryNarrow(w.narrow)
	if sum.Audit != "" {
		s.box.Append(summaryLine("Audit", sum.Audit, "am-summary-audit"))
	}
	for _, n := range sum.Notes {
		l := wrapLabel("Note: " + n)
		l.AddCSSClass("am-run-meta")
		l.AddCSSClass("am-summary-note")
		s.box.Append(l)
	}
}

// setSummaryNarrow lays the two columns side by side, or stacked when the
// window is narrow, and leaves out the space bars (the values stay).
func (w *Window) setSummaryNarrow(narrow bool) {
	s := w.sum
	if s == nil {
		return
	}
	if s.cols != nil {
		right := s.cols.ChildAt(1, 0)
		if right == nil {
			right = s.cols.ChildAt(0, 1)
		}
		s.cols.Remove(right)
		if narrow {
			s.cols.Attach(right, 0, 1, 1, 1)
		} else {
			s.cols.Attach(right, 1, 0, 1, 1)
		}
		s.cols.SetColumnHomogeneous(!narrow)
	}
	for _, b := range s.bars {
		b.SetVisible(!narrow)
	}
}

// wrapLabel is a left-aligned label that wraps (and breaks inside a long
// word: a package name, a path).
func wrapLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	wrapNatural(l)
	return l
}

// newSummaryHero is the hero card: the kicker, the headline, the sub line
// and the attention rows, with the corner marks (hero only, spec 1.10).
func (w *Window) newSummaryHero(sum *runview.Summary) *gtk.Box {
	hero := gtk.NewBox(gtk.OrientationVertical, 6)
	hero.AddCSSClass("am-hero")
	hero.AddCSSClass("am-blueprint")
	hero.AddCSSClass("am-summary-hero")
	kicker := wrapLabel(sum.Kicker)
	kicker.AddCSSClass("am-kicker")
	kicker.AddCSSClass("am-tone-" + sum.Tone)
	title := wrapLabel(sum.Headline)
	title.AddCSSClass("am-hero-title")
	hero.Append(kicker)
	hero.Append(title)
	if sum.Sub != "" {
		sub := wrapLabel(sum.Sub)
		sub.AddCSSClass("am-run-meta")
		hero.Append(sub)
	}
	if len(sum.Attention) > 0 {
		rows := gtk.NewBox(gtk.OrientationVertical, 8)
		rows.AddCSSClass("am-attn")
		for _, a := range sum.Attention {
			rows.Append(w.newAttentionRow(a))
		}
		hero.Append(rows)
	}
	return hero
}

// newAttentionRow is one thing that needs the user: an icon, the title,
// the detail and, for a .pacnew/.pacsave row, the copy button.
func (w *Window) newAttentionRow(a runview.AttentionRow) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.AddCSSClass("am-attn-row")
	icon := gtk.NewImageFromIconName("dialog-warning-symbolic")
	icon.AddCSSClass("am-state-auth-refused") // the warn tone
	icon.SetVAlign(gtk.AlignStart)
	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	title := wrapLabel(a.Title)
	title.AddCSSClass("am-step-title")
	col.Append(title)
	if a.Text != "" {
		d := wrapLabel(a.Text)
		d.AddCSSClass("am-step-sub")
		col.Append(d)
	}
	if a.Command != "" {
		b := gtk.NewButtonWithLabel(a.Button)
		b.AddCSSClass("am-step-action")
		b.SetHAlign(gtk.AlignStart)
		b.SetMarginTop(4)
		cmd := a.Command
		b.ConnectClicked(func() { w.copyCommand(cmd) })
		col.Append(b)
		w.sum.copies = append(w.sum.copies, b)
	}
	row.Append(icon)
	row.Append(col)
	return row
}

// copyCommand puts command on the clipboard. It never runs it.
func (w *Window) copyCommand(command string) {
	w.copied = command
	w.win.Clipboard().SetText(command)
	w.toast("Copied: " + command)
}

// columnHeading is a column's small heading.
func columnHeading(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("am-kicker")
	l.AddCSSClass("am-column-heading")
	return l
}

// summaryLine is a dim label and its text, the text wrapping.
func summaryLine(label, text, class string) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.AddCSSClass("am-summary-line")
	if class != "" {
		row.AddCSSClass(class)
	}
	k := gtk.NewLabel(label)
	k.SetXAlign(0)
	k.SetVAlign(gtk.AlignStart)
	k.AddCSSClass("am-run-meta")
	row.Append(k)
	t := wrapLabel(text)
	t.SetHExpand(true)
	row.Append(t)
	return row
}

// newPackagesColumn is the Packages column: the count, the first names
// that open to every upgrade (name, old to new), then the lines that are
// not zero.
func (w *Window) newPackagesColumn(p *runview.PackagesBlock) gtk.Widgetter {
	col := gtk.NewBox(gtk.OrientationVertical, 6)
	col.AddCSSClass("am-summary-col")
	col.Append(columnHeading("PACKAGES"))
	head := wrapLabel(p.Head)
	head.AddCSSClass("am-step-title")
	col.Append(head)
	if p.Top != "" {
		ex := gtk.NewExpander("")
		ex.AddCSSClass("am-pkgs")
		top := wrapLabel(p.Top)
		ex.SetLabelWidget(top)
		list := gtk.NewBox(gtk.OrientationVertical, 0)
		list.AddCSSClass("am-pkg-list")
		for _, r := range p.Rows {
			row := gtk.NewBox(gtk.OrientationHorizontal, 10)
			row.AddCSSClass("am-pkg")
			name := wrapLabel(r.Name)
			name.SetHExpand(true)
			name.AddCSSClass("monospace")
			ch := wrapLabel(r.Change)
			ch.AddCSSClass("monospace")
			ch.AddCSSClass("am-step-sub")
			ch.SetXAlign(1)
			row.Append(name)
			row.Append(ch)
			list.Append(row)
		}
		if p.Hidden > 0 {
			more := wrapLabel("… +" + strconv.Itoa(p.Hidden) + " more in the log")
			more.AddCSSClass("am-step-sub")
			list.Append(more)
		}
		ex.SetChild(list)
		col.Append(ex)
	}
	for _, l := range p.Lines {
		col.Append(summaryLine(l.Label, l.Text, ""))
	}
	return col
}

// newFreedColumn is the Space freed column: a row per area with its bar
// (zero rows muted, unknown ones "unknown"), the total, then the line on
// what the package cache still holds.
func (w *Window) newFreedColumn(sum *runview.Summary) gtk.Widgetter {
	col := gtk.NewBox(gtk.OrientationVertical, 6)
	col.AddCSSClass("am-summary-col")
	f := sum.Freed
	if f != nil {
		col.Append(columnHeading("SPACE FREED"))
		grid := gtk.NewGrid()
		grid.SetColumnSpacing(10)
		grid.SetRowSpacing(5)
		for i, r := range f.Rows {
			name := wrapLabel(r.Label)
			name.SetHExpand(true)
			bar := gtk.NewLevelBar()
			bar.AddCSSClass("am-freed-bar")
			bar.SetMinValue(0)
			bar.SetMaxValue(1)
			bar.SetValue(r.Fraction)
			bar.SetVAlign(gtk.AlignCenter)
			bar.SetSizeRequest(60, -1)
			bar.SetHExpand(true)
			w.sum.bars = append(w.sum.bars, bar)
			val := gtk.NewLabel(r.Value)
			val.SetXAlign(1)
			val.AddCSSClass("monospace")
			val.AddCSSClass("numeric")
			val.SetVAlign(gtk.AlignStart)
			if r.Zero || r.Unknown {
				for _, wd := range []gtk.Widgetter{name, val, bar} {
					gtk.BaseWidget(wd).AddCSSClass("am-zero")
				}
			}
			grid.Attach(name, 0, i, 1, 1)
			grid.Attach(bar, 1, i, 1, 1)
			grid.Attach(val, 2, i, 1, 1)
		}
		col.Append(grid)
		total := gtk.NewBox(gtk.OrientationHorizontal, 10)
		total.AddCSSClass("am-freed-total")
		tn := wrapLabel("Freed in total")
		tn.SetHExpand(true)
		tn.AddCSSClass("am-step-title")
		tv := gtk.NewLabel(f.Total)
		tv.SetXAlign(1)
		tv.AddCSSClass("monospace")
		tv.AddCSSClass("numeric")
		tv.AddCSSClass("am-step-title")
		total.Append(tn)
		total.Append(tv)
		col.Append(total)
	}
	if sum.Cache != "" {
		line := gtk.NewBox(gtk.OrientationHorizontal, 8)
		line.AddCSSClass("am-cacheline")
		icon := gtk.NewImageFromIconName("dialog-information-symbolic")
		icon.SetVAlign(gtk.AlignStart)
		icon.AddCSSClass("am-state-running") // the info tone
		line.Append(icon)
		t := wrapLabel(sum.Cache)
		t.SetHExpand(true)
		line.Append(t)
		col.Append(line)
	}
	return col
}
