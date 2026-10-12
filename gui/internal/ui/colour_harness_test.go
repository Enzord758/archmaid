package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGUIRunColour: the Run page draws a step's colour, on the real Driver,
// with topgrade and yay shims that colour only when their switch asks
// (shimOpts.colour; actions/colour.go sets it on their children):
//
//   - real-colour: Run topgrade; at its end topgrade's section is open,
//     and its own lines (the step titles, the summary's OK) are drawn with
//     a Pango foreground and bold, the pacman lines under it plain;
//   - real-colour-fc: Full Cleanup, whose last step, yay -Sc --aur, is
//     open at its end: yay ran with --color always and its ":: " is drawn
//     in a colour.
//
// With ARCHMAID_GUI_SHOTS the two Run pages are real-colour-end-1.png and
// real-colour-fc-end-1.png.
func TestGUIRunColour(t *testing.T) {
	needDisplay(t)

	// drawn is the attributes of the visible output row whose text is
	// text, or "" if it is drawn without any (or isn't visible).
	drawn := func(rep *report, text string) string {
		for _, la := range rep.LineAttrs {
			if tx, attrs, _ := strings.Cut(la, "\t"); tx == text {
				return attrs
			}
		}
		return ""
	}

	t.Run("topgrade", func(t *testing.T) {
		_, env := installShims(t, shimOpts{colour: true})
		rep, _ := child(t, "real-colour", env...)
		if rep.Outcome != "completed" {
			t.Fatalf("outcome %q rows %q", rep.Outcome, rep.Rows)
		}
		for _, text := range []string{"System update: OK", "―― 09:00:05 - Summary ――"} {
			if a := drawn(rep, text); !strings.Contains(a, "weight bold") {
				t.Errorf("row %q drawn with %q, want bold; coloured rows %q", text, a, rep.LineAttrs)
			}
		}
		if a := drawn(rep, "System update: OK"); !strings.Contains(a, "foreground #") {
			t.Errorf("topgrade's OK drawn with %q, want a foreground colour", a)
		}
		if a := drawn(rep, ":: Synchronizing package databases..."); a != "" {
			t.Errorf("a plain line drawn with %q", a)
		}
		if rep.Coloured == 0 || rep.Coloured >= rep.Labels {
			t.Errorf("%d of %d visible rows coloured, want some, not all", rep.Coloured, rep.Labels)
		}
	})

	t.Run("yay", func(t *testing.T) {
		dir, env := installShims(t, shimOpts{colour: true})
		rep, _ := child(t, "real-colour-fc", env...)
		if rep.Outcome != "completed" {
			t.Fatalf("outcome %q rows %q", rep.Outcome, rep.Rows)
		}
		if got := readLines(t, filepath.Join(dir, "yay.log")); !slices.Equal(got, []string{"-Sc --aur --noconfirm --color always"}) {
			t.Errorf("yay ran %q, want -Sc --aur --noconfirm --color always", got)
		}
		if a := drawn(rep, ":: Cleaning AUR cache..."); !strings.Contains(a, "0 2 foreground #") {
			t.Errorf("yay's :: drawn with %q, want a foreground colour; coloured rows %q", a, rep.LineAttrs)
		}
	})
}
