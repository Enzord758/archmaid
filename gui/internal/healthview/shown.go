package healthview

import "strings"

// Shown is cmd as one line for a tooltip or a toast: line breaks and runs
// of spaces become one space, and a command longer than max runes is cut
// with "…". The clipboard always gets the whole command, never this.
func Shown(cmd string, max int) string {
	cmd = strings.Join(strings.Fields(cmd), " ")
	if r := []rune(cmd); max > 0 && len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return cmd
}
