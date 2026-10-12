package runview

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// A confirm that acts on a list (old downloads) carries the list twice:
// as Items, and in its text, which is the TUI's (the first names and
// "…and N more"). The GUI shows Items as its own list, so the body keeps
// only what isn't the list.

// cutWidth is the width the TUI cuts a name to (downloads.Plan.Confirm:
// 59 runes and "…"). A shorter line ending in "…" is a note, not a name.
const cutWidth = 60

// moreLine is the TUI's "…and N more" line.
var moreLine = regexp.MustCompile(`^(…|\.\.\.)and \d+ more$`)

// BodyWithoutItems is body without the lines that list items: a line that
// is an item, or an item shortened with "…" (the TUI's text cuts long
// names), and the "…and N more" line. Every other line stays, and blank
// lines at its ends go.
func BodyWithoutItems(body string, items []string) string {
	if len(items) == 0 {
		return body
	}
	listed := func(line string) bool {
		if moreLine.MatchString(line) {
			return true
		}
		cut, short := strings.CutSuffix(line, "…")
		for _, it := range items {
			if line == it || (short && utf8.RuneCountInString(line) == cutWidth && strings.HasPrefix(it, cut)) {
				return true
			}
		}
		return false
	}
	var keep []string
	for _, l := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(l); t != "" && listed(t) {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Trim(strings.Join(keep, "\n"), "\n")
}
