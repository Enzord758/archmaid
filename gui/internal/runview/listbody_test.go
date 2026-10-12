package runview

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// TestBodyWithoutItems: the downloads confirm's body, as a real scan's
// plan builds it (the first ten names, long ones cut, "…and N more"), is
// empty once its items are listed apart; a body's other lines stay, and
// a body without items is unchanged.
func TestBodyWithoutItems(t *testing.T) {
	names := []string{
		"Firefox Setup 131.0.3.exe",
		"Invoice_2026-08_Order-1234567890_Nordisk-Fjernvarme-og-Energiselskab-ApS.pdf",
		"NVIDIA-Linux-x86_64-615.71.run",
		"Screenshot from 2026-08-14 21-33-07.png",
		"archlinux-2026.09.01-x86_64.iso",
		"bad\x1b[31mname.txt", // quoted in both
		"jetbrains-toolbox-2.5.2.35332.tar.gz",
		"linux-firmware-20260910.r1234.gabcdef0123456789-1-any.pkg.tar.zst",
		"proton-ge-custom-GE-Proton10-17",
		"texlive-2026-full-installation-image-with-all-collections-and-documentation-x86_64.iso",
		"ubuntu-24.04.3-desktop-amd64.iso",
		"y2mate.com - Some_Really_Long_Video_Title_With_Underscores_That_Never_Ends_1080p.mp4",
	}
	p := downloads.Plan{Shown: "~/Downloads", Names: names}
	_, body, _ := strings.Cut(p.Confirm(), "\n")
	var items []string
	for _, n := range names {
		items = append(items, sanitize.QuoteName(n, 200))
	}
	if !strings.Contains(body, "Firefox") || !strings.Contains(body, "more") {
		t.Fatalf("the plan's text isn't the list it used to be:\n%s", body)
	}
	if got := BodyWithoutItems(body, items); got != "" {
		t.Errorf("BodyWithoutItems left:\n%s", got)
	}

	mixed := "These are older than 30 days.\n\n  " + items[0] + "\n  " + string([]rune(items[1])[:59]) + "…\n  …and 3 more\nNothing else is touched."
	if got, want := BodyWithoutItems(mixed, items), "These are older than 30 days.\n\nNothing else is touched."; got != want {
		t.Errorf("BodyWithoutItems(mixed) = %q, want %q", got, want)
	}
	// A line that only starts like an item, or a bare "…", stays.
	for _, keep := range []string{"Firefox", "…", "Firefox Setup 131.0.3.exe is open"} {
		if got := BodyWithoutItems(keep, items); got != keep {
			t.Errorf("BodyWithoutItems(%q) = %q", keep, got)
		}
	}
	if got := BodyWithoutItems(body, nil); got != body {
		t.Errorf("without items the body changed: %q", got)
	}
}

// TestBodyWithoutItemsKeepsOtherEllipsisLines (QA, batch 6): only a line cut
// the way the TUI cuts a name (to 60 runes, the last "…") can be a shortened
// item. A short line that ends in "…" (a note, a summary) stays even when
// some item's name starts the same way.
func TestBodyWithoutItemsKeepsOtherEllipsisLines(t *testing.T) {
	items := []string{"Moving files to trash.txt", "Total size 4 GB.iso"}
	head := "Delete 2 item(s) older than 30 days in ~/Downloads?"
	for _, keep := range []string{"Moving files…", "Total size…", "Moving…"} {
		body := head + "\n\n  " + items[0] + "\n  " + keep + "\n  " + items[1]
		want := head + "\n\n  " + keep
		if got := BodyWithoutItems(body, items); got != want {
			t.Errorf("a note %q beside the items: got\n%q\nwant\n%q", keep, got, want)
		}
	}
}
