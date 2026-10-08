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

// Credentials that output carries outside a URL's userinfo, matched
// case-insensitively, the name kept and the value replaced:
//
//   - a query or assignment token (?token=…, &access_token=…, " api_key=…",
//     "password=…"): the name must start the text or follow ?, &, ;, a
//     blank or a quote, so a "key" inside an identifier ("monkey=1") or a
//     flag ("--key=…") is left alone; the value runs to &, ;, a blank or a
//     quote;
//   - an Authorization header: its scheme stays, its credentials go;
//   - "password: …" / "passwd = …" on one line (never across a line
//     break, so sudo's "Password:" prompt with nothing after it is kept).
var (
	secretParam = regexp.MustCompile(`(?im)(^|[?&;\s'"])(access_token|refresh_token|token|api[_-]?key|key|secret|password|passwd)=[^&;\s'"]+`)
	authHeader  = regexp.MustCompile(`(?i)(authorization:[ \t]*[A-Za-z]+[ \t]+)[^\s'"]+`)
	passPair    = regexp.MustCompile(`(?i)\b(pass(?:word|wd)[ \t]*[:=][ \t]*)[^\s'"]+`)
)

// Redact is RedactURLs plus the credentials above: what Copy error puts on
// the clipboard, which the desktop's clipboard history keeps.
func Redact(s string) string {
	s = RedactURLs(s)
	s = secretParam.ReplaceAllString(s, "${1}${2}=***")
	s = authHeader.ReplaceAllString(s, "${1}***")
	return passPair.ReplaceAllString(s, "${1}***")
}

// tailLines is how many of a failed step's last output lines Copy error
// takes; causeLines how many of its error-looking lines (causeLine) it
// takes before them, the first ones, so the real cause isn't lost behind
// a long tail (git's "could not read Username", pnpm's error).
const (
	tailLines  = 20
	causeLines = 15
)

// causeLine is an output line that reads like an error; ERR_ is pnpm's
// (ERR_PNPM_FETCH_404).
var causeLine = regexp.MustCompile(`(?i)error|failed|fatal|could not|\berr_`)

// CopyError is what the failed step's Copy error puts on the clipboard:
// its title and error, then the first causeLines error-looking lines of
// its output that aren't in its last tailLines lines, a "…" where lines
// were left out, then those last lines; all of it redacted (Redact).
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
	tail := max(0, len(lines)-tailLines)
	last := -1 // the index of the last line written
	n := 0
	for i := 0; i < tail && n < causeLines; i++ {
		if !causeLine.MatchString(lines[i]) {
			continue
		}
		if i != last+1 {
			b.WriteString("…\n")
		}
		b.WriteString(lines[i] + "\n")
		last, n = i, n+1
	}
	if tail > last+1 {
		b.WriteString("…\n")
	}
	for _, l := range lines[tail:] {
		b.WriteString(l + "\n")
	}
	return Redact(b.String())
}
