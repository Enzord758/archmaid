package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestSummaryZeroRowsAreDim: a Freed row that freed nothing, and an unknown
// one, are drawn in the dim grey; a row that freed something is not. (The
// goldens run without colour, so this is the only place the dimming shows.)
func TestSummaryZeroRowsAreDim(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	out := summaryBlock(quietReport(), summaryOpts{width: 80})
	dim := stepSkipStyle.Render("x")
	grey := dim[:strings.Index(dim, "x")] // the escape that starts the dim style
	if !strings.Contains(grey, "\x1b[") {
		t.Fatalf("no colour in a TrueColor render: %q", dim)
	}
	for _, c := range []struct {
		label string
		dim   bool
	}{
		{"Uninstalled packages' cache", true},
		{"Journal vacuum", true},
		{"Package cache (keeps 3)", false},
		{"AUR build cache", false},
		{"Temp files", false},
	} {
		var row string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, c.label) {
				row = l
			}
		}
		if row == "" {
			t.Fatalf("no row for %s", c.label)
		}
		if got := strings.Contains(row, grey); got != c.dim {
			t.Errorf("%s: dim = %v, want %v (%q)", c.label, got, c.dim, row)
		}
	}
}
