package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// A line of a title and its tags ("root", "asks", a step's state): on one
// line when both fit, the title at the start and the tags at the end, as a
// box with an expanding title draws it; in a narrower window the tags move
// under the title instead of pushing the line past the window's width
// (Dev 5's VM round b5: in a 360 px window the Overview's and the Run
// page's root tags were cut off on the right).

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

// tagLine is title and tags in an AdwWrapBox, spaced as a horizontal box
// with spacing would; tags is the box holding the tags, returned so the
// caller can hide it when it shows nothing (an empty box would still take
// the spacing, and a line of its own).
func tagLine(title gtk.Widgetter, spacing int, tags ...gtk.Widgetter) (line *adw.WrapBox, tagBox *gtk.Box) {
	tagBox = gtk.NewBox(gtk.OrientationHorizontal, spacing)
	// Under the title, the tags start where the title does (a line with
	// one child is stretched; the box keeps its natural width at the start).
	tagBox.SetHAlign(gtk.AlignStart)
	for _, t := range tags {
		tagBox.Append(t)
	}
	line = adw.NewWrapBox()
	line.SetChildSpacing(spacing)
	line.SetLineSpacing(4)
	// Spread: the title at the start and the tags at the end of the line.
	line.SetJustify(adw.JustifySpread)
	line.SetJustifyLastLine(true)
	line.SetWrapPolicy(adw.WrapNatural)
	line.SetHExpand(true)
	line.Append(title)
	line.Append(tagBox)
	return line, tagBox
}
