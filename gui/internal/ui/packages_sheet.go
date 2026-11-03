package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The confirm sheet (variant A4/A4-AUR): the Full Cleanup gate's dialog
// (newWideDialog: corner marks, the plan on the left, "Before you start" on
// the right, Cancel the default). It is rebuilt whenever the model's sheet
// changes state (planning, review, refused, updates pending, stale); inside
// the review state only the boxes change, and they only switch Install.

// refreshSheet opens, replaces or closes the dialog to match the model.
func (p *pkgPage) refreshSheet() {
	p.w.updateStop()
	v := p.ctl.Sheet.View()
	if v.State == pkgview.SheetClosed || v.State == pkgview.SheetRunning {
		p.closeSheet()
		return
	}
	if p.sheet != nil && p.sheetState == v.State {
		if p.sheetYes != nil {
			p.sheetYes.SetSensitive(v.YesEnabled)
		}
		return
	}
	p.closeSheet()
	p.openSheet(v)
}

// closeSheet takes the dialog down without answering for it.
func (p *pkgPage) closeSheet() {
	d := p.sheet
	if d == nil {
		return
	}
	p.sheet, p.sheetYes = nil, nil
	d.answered = true // a close the model asked for answers nothing
	d.ForceClose()
}

func (p *pkgPage) openSheet(v pkgview.SheetView) {
	width := wideText
	if v.State == pkgview.SheetReview {
		width = wideGate
	}
	d := newWideDialog(v.Heading, width)
	d.AddResponse("no", v.NoLabel)
	if v.UpdateFirst {
		d.AddResponse("update", "Update system first")
	}
	if v.Retry != "" {
		d.AddResponse("retry", v.Retry)
		d.SetResponseAppearance("retry", adw.ResponseSuggested)
	}
	if v.State == pkgview.SheetReview {
		d.AddResponse("yes", v.YesLabel)
		if v.Destructive {
			d.SetResponseAppearance("yes", adw.ResponseDestructive)
		} else {
			d.SetResponseAppearance("yes", adw.ResponseSuggested)
		}
		if r := d.response("yes"); r != nil {
			r.button.SetSensitive(v.YesEnabled)
			p.sheetYes = r.button
		}
	}
	d.SetCloseResponse("no")
	d.SetDefaultResponse("no") // Cancel is the default
	p.sheetChecks = nil
	p.sheetFill(d, v)
	d.ConnectResponse(func(resp string) {
		if p.sheet == d {
			p.sheet, p.sheetYes = nil, nil
		}
		switch resp {
		case "yes":
			p.ctl.Confirm()
		case "retry":
			p.ctl.Retry()
		case "update":
			p.ctl.UpdateFirst()
		default:
			p.ctl.Dismiss()
		}
	})
	p.sheet, p.sheetState, p.sheetAt = d, v.State, time.Now()
	d.show(p.w)
}

// planMin is the plan column's minimum width beside the notes.
const planMin = 400

func (p *pkgPage) sheetFill(d *amDialog, v pkgview.SheetView) {
	if v.Body != "" {
		d.add(dialogText(v.Body, "am-dialog-body"))
	}
	if v.State != pkgview.SheetReview {
		return
	}
	left := p.sheetRows(d, v)
	right := sheetNotes(v.Notes)
	// The columns' widths come from wrapping labels, whose natural width
	// depends on the height they are given: with a long note or long
	// versions the plan was squeezed to a sliver and the notes ran wide.
	// The plan gets a minimum of its own while the columns are side by side.
	gtk.BaseWidget(left).SetSizeRequest(planMin, -1)
	d.onNarrow(func(narrow bool) {
		if narrow {
			gtk.BaseWidget(left).SetSizeRequest(-1, -1)
		} else {
			gtk.BaseWidget(left).SetSizeRequest(planMin, -1)
		}
	})
	d.add(d.columns(left, right, notesColumn))
	if len(v.Reviews) > 0 {
		d.add(p.reviewView(d, v))
	}
	for _, c := range v.Checks {
		d.add(p.checkBox(c))
	}
}

// sheetRows is the plan: a rail of the packages, each with its kind and size.
func (p *pkgPage) sheetRows(d *amDialog, v pkgview.SheetView) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	title := "Will be installed"
	if v.Mode == pkgview.ModeRemove {
		title = "Will be removed"
	}
	box.Append(plainLabel(fmt.Sprintf("%s · %d", title, len(v.Rows)), "am-kicker"))
	rail := gtk.NewBox(gtk.OrientationVertical, 0)
	rail.AddCSSClass("am-rail")
	for _, r := range v.Rows {
		name := plainLabel(r.Name, "am-pkg-name")
		if v.Mode == pkgview.ModeRemove {
			name.AddCSSClass("error")
		}
		ver := r.Version
		if r.Old != "" {
			ver = r.Old + " → " + r.Version
		}
		head := gtk.NewBox(gtk.OrientationHorizontal, 8)
		head.Append(name)
		head.Append(plainLabel(ver, "am-pkg-ver"))
		var tags []gtk.Widgetter
		switch r.Kind {
		case "dependency":
			tags = append(tags, tag("dependency", "am-tag-neutral"))
		case "upgrade":
			tags = append(tags, tag("upgrade", "am-tag-warn"))
		case "removed":
			if !r.Target {
				tags = append(tags, tag("with it", "am-tag-neutral"))
			}
		}
		if r.Repo == "aur" {
			tags = append(tags, tag("AUR", "am-tag-neutral"))
		} else if r.Repo != "" {
			tags = append(tags, tag(r.Repo, "am-tag-neutral"))
		}
		line := newTagLine(head, 8, tags...)
		line.tags.SetVisible(len(tags) > 0)
		line.box.SetHExpand(true)
		// The tags go under the name: beside it the plan's natural width
		// squeezed the notes column (an AUR plan with long versions).
		line.setUnder(true)
		size := plainLabel(r.Size, "am-pkg-ver")
		size.SetVAlign(gtk.AlignBaselineCenter)
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		row.AddCSSClass("am-rail-step")
		row.Append(line.box)
		row.Append(size)
		rail.Append(row)
	}
	box.Append(rail)
	box.Append(plainLabel(v.Totals, "am-pkg-totals"))
	return box
}

// sheetNotes is "Before you start": each note with its tag.
func sheetNotes(notes []pkgview.Note) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.Append(plainLabel("Before you start", "am-kicker"))
	for _, n := range notes {
		row := gtk.NewBox(gtk.OrientationVertical, 4)
		if n.Tag != "" {
			t := tag(n.Tag, noteTagClass(n))
			t.SetHAlign(gtk.AlignStart)
			row.Append(t)
		}
		l := dialogText(n.Text, "am-note")
		// A wrapping label's natural width is taken for the height it is
		// given, which let a long note take the column from the plan: pin
		// it to one width.
		l.SetWidthChars(34)
		l.SetMaxWidthChars(34)
		l.SetHExpand(false)
		row.Append(l)
		row.SetHExpand(false)
		box.Append(row)
	}
	return box
}

func noteTagClass(n pkgview.Note) string {
	switch n.Tag {
	case "root":
		return "am-tag-root"
	case "asks":
		return "am-tag-asks"
	}
	return "am-tag-" + string(n.Tone)
}

// checkBox is a box the user ticks; Install enables when the model says so.
func (p *pkgPage) checkBox(c pkgview.Check) gtk.Widgetter {
	cb := gtk.NewCheckButton()
	l := dialogText(c.Label, "am-dialog-body")
	cb.SetChild(l)
	cb.SetActive(c.On)
	id := c.ID
	cb.ConnectToggled(func() {
		p.ctl.Sheet.Tick(id, cb.Active())
		p.changed()
	})
	p.sheetChecks = append(p.sheetChecks, cb)
	return cb
}

// reviewView is the AUR review: the PKGBUILD and the other files as plain
// scrollable text, the diff against the last reviewed commit, the flags and
// the audit's findings.
func (p *pkgPage) reviewView(d *amDialog, v pkgview.SheetView) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.Append(plainLabel("PKGBUILD review", "am-kicker"))
	var files, diff strings.Builder
	for _, r := range v.Reviews {
		fmt.Fprintf(&files, "### %s %s (commit %s)\n\n", r.Base, r.Version, shortCommit(r.Commit))
		for _, f := range r.Files {
			fmt.Fprintf(&files, "=== %s (%s)\n", f.Path, pkgview.Size(f.Size))
			if f.Text == "" {
				files.WriteString("(not shown: " + notShown(f) + ")\n\n")
				continue
			}
			files.WriteString(strings.TrimRight(f.Text, "\n") + "\n\n")
		}
		if len(r.Sources) > 0 {
			fmt.Fprintf(&files, "=== sources of %s\n", r.Base)
			for _, src := range r.Sources {
				sum := "checksum"
				if !src.Checksum {
					sum = "NO CHECKSUM"
				}
				fmt.Fprintf(&files, "%s (%s)\n", src.URL, sum)
			}
			files.WriteString("\n")
		}
		switch {
		case r.Diff != "":
			fmt.Fprintf(&diff, "### %s since %s\n\n%s\n\n", r.Base, shortCommit(r.Prev), strings.TrimRight(r.Diff, "\n"))
		case r.DiffNote != "" && r.Prev != "":
			fmt.Fprintf(&diff, "### %s since %s\n\n%s\n\n", r.Base, shortCommit(r.Prev), r.DiffNote)
		}
	}
	stack := gtk.NewStack()
	stack.AddNamed(reviewText(files.String()), "files")
	hasDiff := diff.Len() > 0
	if hasDiff {
		stack.AddNamed(reviewText(diff.String()), "diff")
	}
	if hasDiff {
		tabs := gtk.NewBox(gtk.OrientationHorizontal, 0)
		tabs.AddCSSClass("linked")
		tabs.SetHAlign(gtk.AlignStart)
		bf := gtk.NewToggleButtonWithLabel("Files")
		bd := gtk.NewToggleButtonWithLabel("Changes")
		bd.SetGroup(bf)
		bf.SetActive(true)
		bf.ConnectToggled(func() {
			if bf.Active() {
				stack.SetVisibleChildName("files")
			} else {
				stack.SetVisibleChildName("diff")
			}
		})
		tabs.Append(bf)
		tabs.Append(bd)
		box.Append(tabs)
	}
	box.Append(stack)
	for _, r := range v.Reviews {
		for _, fl := range r.Flags {
			row := gtk.NewBox(gtk.OrientationVertical, 2)
			ck := tag("check", "am-tag-warn")
			ck.SetHAlign(gtk.AlignStart)
			row.Append(ck)
			row.Append(dialogText(r.Base+": "+fl, "am-note"))
			box.Append(row)
		}
	}
	if audit := auditRows(v.Reviews); audit != nil {
		box.Append(audit)
	}
	return box
}

// notShown is why a file's text is not in the review.
func notShown(f steps.AURFile) string {
	if f.Note != "" {
		return f.Note
	}
	return "binary or too large"
}

func shortCommit(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	if c == "" {
		return "none"
	}
	return c
}

func reviewText(text string) gtk.Widgetter {
	tv := gtk.NewTextView()
	tv.SetEditable(false)
	tv.SetCursorVisible(false)
	tv.SetMonospace(true)
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.Buffer().SetText(text) // plain text: a TextBuffer has no markup
	tv.AddCSSClass("am-pkg-review")
	tv.SetFocusable(false)
	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sc.SetMinContentHeight(160)
	sc.SetMaxContentHeight(260)
	sc.SetPropagateNaturalHeight(true)
	sc.SetChild(tv)
	sc.AddCSSClass("am-pkg-review-frame")
	sc.AddCSSClass(textSurfClass) // glass.css: the PKGBUILD text is 93 % opaque
	return sc
}

// auditRows lists the maintainer audit's findings, nil when there are none.
func auditRows(rs []steps.AURPlan) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	n := 0
	for _, r := range rs {
		for _, f := range r.Audit {
			n++
			class, sev := "am-tag-warn", "flagged"
			if f.High {
				class, sev = "am-tag-danger", "high"
			}
			row := gtk.NewBox(gtk.OrientationVertical, 2)
			t := tag(sev, class)
			t.SetHAlign(gtk.AlignStart)
			row.Append(t)
			row.Append(dialogText(f.Name+": "+f.Reason, "am-note"))
			box.Append(row)
		}
	}
	if n == 0 {
		return nil
	}
	head := plainLabel(fmt.Sprintf("Audit findings · %d", n), "am-kicker")
	out := gtk.NewBox(gtk.OrientationVertical, 6)
	out.Append(head)
	out.Append(box)
	return out
}
