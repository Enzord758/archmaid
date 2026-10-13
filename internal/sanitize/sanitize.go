// Package sanitize is the one filter for untrusted text (command output, AUR
// fields, sudo prompts, logs), plus the small helpers that put such text on one
// line. It must stay UI-free: nothing here may import charmbracelet.
package sanitize

import (
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── sanitizing untrusted text ────────────────────────────────────────────────
//
// Command output (topgrade, yay, makepkg — i.e. AUR build scripts), AUR RPC
// fields and sudo's prompt are untrusted: an escape sequence in them can retitle
// the terminal, rewrite the clipboard (OSC 52), hide text or move the cursor
// over what the user is reading, and a raw escape in a log file plays back in
// whatever terminal later views it. This package is the one place that decides what
// survives, for every such path: streamed output, logs (written and read back),
// and anything else that shows text archmaid didn't write itself.

// Mode selects what String keeps. The zero value is Plain, the strict one:
// keeping colours is something a caller asks for (KeepSGR), never what it
// gets by leaving a Mode unset.
type Mode int

const (
	// Plain removes every escape sequence: for logs, which must read
	// as plain text anywhere.
	Plain Mode = iota
	// KeepSGR keeps SGR sequences (ESC [ … m: colour and text style)
	// so streamed output keeps its colours in the output pane.
	KeepSGR
)

// ColourMode is the Mode for text that may show colour: KeepSGR, or Plain
// when NO_COLOR (set and not empty, no-color.org) is in the environment, so
// NO_COLOR means no SGR anywhere, a user step's own output included
// (yay's bold under topgrade). Callers read it once, at start: the engine
// Session in New, the helper client per step.
func ColourMode() Mode {
	if os.Getenv("NO_COLOR") != "" {
		return Plain
	}
	return KeepSGR
}

// String returns s with every control character removed except tab and
// newline, and every escape sequence removed except, in KeepSGR, SGR.
// That covers C0 and C1 controls, DEL, CSI, OSC, DCS/SOS/PM/APC strings and
// two-byte escapes. Unicode format characters (category Cf: bidi overrides and
// isolates, zero-width spaces and joiners, the BOM, tag characters) go too, so
// text can't be reordered or hidden from the reader. Invalid UTF-8 becomes
// U+FFFD. A carriage return (a progress bar redrawing its line) behaves like
// the terminal: text after it replaces the line so far, so "50%\r100%" reads "100%", while a trailing CR or CRLF just
// ends the line. Text that needs no change is returned without copying.
func String(s string, mode Mode) string {
	if clean(s) {
		return s
	}
	out := make([]byte, 0, len(s))
	lineStart := 0     // index in out where the current line begins
	pendingCR := false // a CR was seen: the next visible text replaces the line
	emit := func(text string) {
		if pendingCR {
			out = out[:lineStart]
			pendingCR = false
		}
		out = append(out, text...)
	}

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\n':
			pendingCR = false
			out = append(out, '\n')
			lineStart = len(out)
			i++
		case c == '\r':
			pendingCR = true
			i++
		case c == '\t':
			emit("\t")
			i++
		case c == 0x1b:
			end, sgr := escapeEnd(s, i)
			if sgr && mode == KeepSGR {
				if seq := withoutConceal(s[i:end]); seq != "" {
					emit(seq)
				}
			}
			i = end
		case c < 0x20 || c == 0x7f:
			i++ // other C0 control, or DEL
		case c < utf8.RuneSelf:
			j := i + 1 // copy the whole run of printable ASCII at once
			for j < len(s) && s[j] >= 0x20 && s[j] < 0x7f {
				j++
			}
			emit(s[i:j])
			i = j
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == utf8.RuneError && size == 1:
				emit("\uFFFD")
			case r >= 0x80 && r <= 0x9f:
				// C1 control (8-bit CSI, OSC, …): dropped
			case dropRune(r):
				// format character (bidi, zero-width), line/paragraph
				// separator or Hangul filler: dropped
			default:
				emit(s[i : i+size])
			}
			i += size
		}
	}
	return string(out)
}

// dropRune reports whether r is removed from the text: a Unicode format
// character (Cf), U+2028/U+2029 (line and paragraph separator: terminals don't
// break lines on them, so they are dropped rather than mapped to "\n", which
// keeps "a line" meaning exactly what \n says) and the Hangul fillers
// U+115F, U+1160, U+3164 and U+FFA0, which render as blank space and so can
// hide or pad text.
func dropRune(r rune) bool {
	switch r {
	case 0x2028, 0x2029, 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// withoutConceal returns the well-formed SGR sequence seq (ESC [ … m) without
// parameter 8 (conceal, which hides text from the reader). Other parameters
// stay; 38/48/58 colour selectors are copied whole, so the 8 in "38;5;8" (a
// palette index) survives. A sequence of only conceal becomes "".
func withoutConceal(seq string) string {
	params := seq[2 : len(seq)-1]
	if strings.IndexByte(params, '8') < 0 {
		return seq
	}
	toks := strings.Split(params, ";")
	kept := make([]string, 0, len(toks))
	for i := 0; i < len(toks); i++ {
		code, _, _ := strings.Cut(toks[i], ":")
		n, _ := strconv.Atoi(code) // "" (= 0) and overlong numbers aren't 8
		switch {
		case (n == 38 || n == 48 || n == 58) && toks[i] == code:
			// semicolon form: 5;n or 2;r;g;b follow
			take := 1
			if i+1 < len(toks) {
				switch toks[i+1] {
				case "5":
					take = 3
				case "2":
					take = 5
				}
			}
			end := min(i+take, len(toks))
			kept = append(kept, toks[i:end]...)
			i = end - 1
		case n == 8:
			// conceal: dropped
		default:
			kept = append(kept, toks[i])
		}
	}
	if len(kept) == len(toks) {
		return seq
	}
	if len(kept) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(kept, ";") + "m"
}

// clean reports whether s is already safe as-is: no control characters but
// tab and newline, no DEL, valid UTF-8, no C1 controls and no format
// characters.
func clean(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if (c < 0x20 && c != '\t' && c != '\n') || c == 0x7f {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (r >= 0x80 && r <= 0x9f) || dropRune(r) {
			return false
		}
		i += size
	}
	return true
}

// escapeEnd returns the index just past the escape sequence starting at
// s[i] == ESC, and whether it is a well-formed SGR (CSI with only digit, ';'
// or ':' parameters and final 'm'). An unterminated sequence runs to the end
// of s, so nothing of it leaks through.
func escapeEnd(s string, i int) (end int, sgr bool) {
	j := i + 1
	if j >= len(s) {
		return j, false
	}
	switch s[j] {
	case '[': // CSI: parameters 0x30–0x3F, intermediates 0x20–0x2F, final 0x40–0x7E
		j++
		sgr = true
		for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
			if (s[j] < '0' || s[j] > '9') && s[j] != ';' && s[j] != ':' {
				sgr = false // private/other parameters ("?", "<", "=", ">")
			}
			j++
		}
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			sgr = false
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			return j + 1, sgr && s[j] == 'm'
		}
		return j, false // malformed: drop what was consumed, resume at s[j]
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: string up to BEL or ST
		for j++; j < len(s); j++ {
			if s[j] == 0x07 && s[i+1] == ']' {
				return j + 1, false
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, false
			}
			if s[j] == 0xc2 && j+1 < len(s) && s[j+1] == 0x9c { // C1 ST, UTF-8 encoded
				return j + 2, false
			}
		}
		return len(s), false
	default: // nF (intermediates then a final, e.g. ESC ( B) or a two-byte escape
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
			j++
		}
		return j, false
	}
}
