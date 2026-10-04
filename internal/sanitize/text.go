package sanitize

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// OneLine is untrusted text made safe to show on one line: sanitized (Plain),
// with every run of whitespace (newlines included) collapsed to one space.
func OneLine(s string) string {
	return strings.Join(strings.Fields(String(s, Plain)), " ")
}

// Clip shortens s to at most n runes, marking a cut with "…".
func Clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// ClipTail shortens s to at most n runes, keeping its tail (a path's binary
// name) and marking the cut with "…".
func ClipTail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

// Field makes a package name or maintainer safe to put on screen, in a popup
// or in a log: String (Plain) drops escape sequences, controls and format
// characters, and the value is kept to one line of at most max runes plus "…".
// Line breaks and tabs are removed before String sees them, so a CR can't make
// the value read as just its tail ("mallory\ralice" shows both names, not
// "alice").
func Field(s string, max int) string {
	s = String(strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, s), Plain)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// QuoteName renders an entry name on one line: a name with control characters
// (a newline would forge extra list lines; escapes could restyle a popup) or
// invalid UTF-8 is shown quoted with escapes, and a long one is cut to n runes
// (the last being "…").
func QuoteName(s string, n int) string {
	if String(s, Plain) != s || strings.ContainsAny(s, "\n\t") {
		s = strconv.Quote(s)
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
