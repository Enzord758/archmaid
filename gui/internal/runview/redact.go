package runview

import (
	"regexp"
	"strings"
)

// urlUserinfo is a URL's scheme and userinfo: "scheme://user:pass@" or
// "scheme://user@". The userinfo stops at the first "/", "@" or space, so a
// path or a plain user@host (no scheme) never matches.
var urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)

// RedactURLs replaces every URL's userinfo in s with "***": a failing
// git pull prints its remote, credentials and all, and what Copy error puts
// on the clipboard stays in the desktop's clipboard history.
func RedactURLs(s string) string {
	return urlUserinfo.ReplaceAllString(s, "${1}***@")
}

// errorLines is how many of a failed step's last output lines Copy error
// takes.
const errorLines = 20

// CopyError is what the failed step's Copy error puts on the clipboard:
// its title, its error and its last 20 output lines, with every URL's
// userinfo redacted (RedactURLs).
func (m *Model) CopyError(step string) string {
	var b strings.Builder
	for _, r := range m.Rows {
		if r.ID == step {
			b.WriteString(r.Title + "\n")
			if r.Err != "" {
				b.WriteString("Error: " + r.Err + "\n")
			}
		}
	}
	lines := m.StepText(step)
	lines = lines[max(0, len(lines)-errorLines):]
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return RedactURLs(b.String())
}
