package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// A line of a title and its tags ("root", "asks", a step's state): in a
// wide window the title, then the tags at the line's end; under the
// narrow breakpoint the tags on a line of their own under the title,
// starting where it does. Always one or the other, on every row: an
// AdwWrapBox moved them under the title only on the rows whose title
// didn't fit, so a narrow plan zig-zagged (the owner's r532 screenshot),
// and before that a plain row cut them off on the right (Dev 5's VM round
// b5, in a 360 px window).

// wrapNatural makes l wrap when it must, while its natural width stays
// that of its text on one line: by default a wrapping label's natural
// width is its minimum (its longest word), so a box or a wrap box that
// gives it its natural width wraps it at every word.
func wrapNatural(l *gtk.Label) {
	l.SetWrap(true)
	// A word longer than the line (a package name, a path) breaks inside
	// the word: by default its length is the label's minimum width.
	l.SetWrapMode(pango.WrapWordChar)
	l.SetNaturalWrapMode(gtk.NaturalWrapNone)
}

// tagLine is a title and its tags, beside it or under it (setUnder).
type tagLine struct {
	box     *gtk.Box // the title, then tags
	tags    *gtk.Box // the tags, hidden by the caller when it shows none
	spacing int      // between the title and the tags, when beside it
}

// tagLineSpacing is the space between the title and the tags under it.
const tagLineSpacing = 4

// newTagLine puts title and tags on a line, spaced as a horizontal box
// with spacing would; the tags beside the title until setUnder.
func newTagLine(title gtk.Widgetter, spacing int, tags ...gtk.Widgetter) *tagLine {
	l := &tagLine{tags: gtk.NewBox(gtk.OrientationHorizontal, spacing), spacing: spacing}
	for _, t := range tags {
		// Each tag keeps its own height on the title's or the state
		// line's first baseline: filling the line drew a root tag beside
		// a state line over three lines three lines high (Dev 5's VM
		// screenshot).
		gtk.BaseWidget(t).SetVAlign(gtk.AlignBaselineCenter)
		l.tags.Append(t)
	}
	// Beside the title the title takes the rest of the line, so the tags
	// end where the line does.
	l.tags.SetVAlign(gtk.AlignBaselineCenter)
	gtk.BaseWidget(title).SetHExpand(true)
	gtk.BaseWidget(title).SetVAlign(gtk.AlignBaselineCenter)
	l.box = gtk.NewBox(gtk.OrientationHorizontal, spacing)
	// Under, the line's baseline is the title's (the step's number beside
	// it sits on the title's first line).
	l.box.SetBaselineChild(0)
	l.box.Append(title)
	l.box.Append(l.tags)
	l.setUnder(false)
	return l
}

// setUnder puts the tags under the title (true) or beside it.
func (l *tagLine) setUnder(under bool) {
	// Beside, the tags hug the line's end even when they wrap (a long
	// state line); under, they start where the title does.
	if under {
		l.box.SetOrientation(gtk.OrientationVertical)
		l.box.SetSpacing(tagLineSpacing)
		l.tags.SetHAlign(gtk.AlignStart)
		return
	}
	l.box.SetOrientation(gtk.OrientationHorizontal)
	l.box.SetSpacing(l.spacing)
	l.tags.SetHAlign(gtk.AlignEnd)
}
