package ui

import (
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/runview"
)

// TestAttrsParseInPango: every attribute runview.Line.Attrs writes is one
// Pango reads back (pango_attr_list_from_string returns NULL on any error).
// Pango needs no display, so this runs in the plain `go test`.
func TestAttrsParseInPango(t *testing.T) {
	p := runview.FallbackPalette
	for _, in := range []string{
		"\x1b[1;31ma\x1b[0m \x1b[7mb\x1b[0m \x1b[2;3;4;9;38;2;1;2;3;48;5;16mc",
		"✔ \x1b[32mok\x1b[0m",
		"\x1b[1;34m::\x1b[0m \x1b[1mline 1\x1b[22m (\x1b[32m1\x1b[39m/\x1b[93m2\x1b[0m) \x1b[38;5;208mupgrading\x1b[0m",
	} {
		l := runview.ParseSGR(in)
		attrs := l.Attrs(&p)
		list := pango.AttrListFromString(attrs)
		if list == nil {
			t.Fatalf("Pango rejected %q", attrs)
		}
		want := strings.Count(attrs, "\n")
		if got := len(list.Attributes()); got != want {
			t.Errorf("%q: Pango read %d attributes, want %d", attrs, got, want)
		}
	}
}
