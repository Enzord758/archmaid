package helper

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// SchemaVersion is the "v" field on every JSON line. Bump it only for a
// breaking change; adding an optional field is not one.
const SchemaVersion = 1

// Event types ("t").
const (
	EventStart    = "start"
	EventProgress = "progress"
	EventLine     = "line"
	EventPlan     = "plan"
	EventResult   = "result"
)

// Skip reports one input the helper deliberately did not act on.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type startEvent struct {
	V       int    `json:"v"`
	T       string `json:"t"`
	Verb    string `json:"verb"`
	Version string `json:"version"`
	Dev     bool   `json:"dev,omitempty"`
}

type progressEvent struct {
	V     int    `json:"v"`
	T     string `json:"t"`
	Step  int    `json:"step"`
	Of    int    `json:"of"`
	Label string `json:"label"`
}

type lineEvent struct {
	V      int    `json:"v"`
	T      string `json:"t"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type planEvent struct {
	V       int      `json:"v"`
	T       string   `json:"t"`
	Targets []string `json:"targets"`
	Items   []string `json:"items"`
	Total   int      `json:"total"`
	Hash    string   `json:"hash"`
}

type resultEvent struct {
	V         int    `json:"v"`
	T         string `json:"t"`
	OK        bool   `json:"ok"`
	Code      int    `json:"code"`
	ChildExit *int   `json:"child_exit"`
	Skipped   []Skip `json:"skipped"`
	Error     string `json:"error,omitempty"`
}

// Emitter writes JSON lines. It is safe for concurrent use (a child's stdout
// and stderr are drained by two goroutines). A write error — the reader went
// away — is remembered and later events are dropped; the helper keeps going
// so a running pacman transaction is never cut short by a closed GUI.
type Emitter struct {
	mu  sync.Mutex
	enc *json.Encoder
	err error
}

// NewEmitter returns an Emitter writing to w.
func NewEmitter(w io.Writer) *Emitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Emitter{enc: enc}
}

func (e *Emitter) emit(v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return
	}
	e.err = e.enc.Encode(v)
}

// Start announces the verb being run.
func (e *Emitter) Start(verb string, dev bool) {
	e.emit(startEvent{V: SchemaVersion, T: EventStart, Verb: Sanitize(verb), Version: Version, Dev: dev})
}

// Progress announces step `step` of `of`.
func (e *Emitter) Progress(step, of int, label string) {
	e.emit(progressEvent{V: SchemaVersion, T: EventProgress, Step: step, Of: of, Label: Sanitize(label)})
}

// Line relays one line of a child's output; stream is "out" or "err".
func (e *Emitter) Line(stream, text string) {
	e.emit(lineEvent{V: SchemaVersion, T: EventLine, Stream: stream, Text: Sanitize(text)})
}

// Plan reports an orphan-removal plan.
func (e *Emitter) Plan(p Plan) {
	e.emit(planEvent{
		V: SchemaVersion, T: EventPlan,
		Targets: sanitizeAll(p.Targets), Items: sanitizeAll(p.Items),
		Total: len(p.Items), Hash: p.Hash,
	})
}

// Result is the final line; exactly one is written per invocation.
func (e *Emitter) Result(code int, childExit *int, skipped []Skip, msg string) {
	clean := make([]Skip, 0, len(skipped))
	for _, s := range skipped {
		clean = append(clean, Skip{Name: Sanitize(s.Name), Reason: Sanitize(s.Reason)})
	}
	e.emit(resultEvent{
		V: SchemaVersion, T: EventResult,
		OK: code == ExitOK, Code: code, ChildExit: childExit,
		Skipped: clean, Error: Sanitize(msg),
	})
}

func sanitizeAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, Sanitize(s))
	}
	return out
}

// Sanitize makes text safe to show in a terminal or GUI: ANSI/VT escape
// sequences (CSI, OSC, DCS, APC, PM and two-byte ESC forms) are removed
// whole, remaining C0/C1 control characters and DEL are dropped, a tab
// becomes a space, invalid UTF-8 becomes U+FFFD, and invisible characters
// that can reorder, hide or fake text are dropped (see invisible). It drops
// what the TUI's sanitizer drops.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size <= 1:
			b.WriteRune(utf8.RuneError)
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// C0, DEL, C1 (incl. the 8-bit CSI 0x9b): drop
		case invisible(r):
			// reorders, hides or fakes text: drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// invisible reports whether r is a display-spoofing character: a Unicode
// format character (category Cf: bidi embeddings, overrides, isolates and
// marks, zero-width spaces and joiners, U+2060–2064, the BOM, tag
// characters), any code point in the tag block U+E0000–E007F, the
// line/paragraph separators U+2028/2029, or a Hangul filler (U+115F,
// U+1160, U+3164, U+FFA0), which renders as blank space.
func invisible(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r),
		r >= 0xe0000 && r <= 0xe007f,
		r == 0x2028, r == 0x2029,
		r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
		return true
	}
	return false
}

// skipEscape returns the index just past the escape sequence starting at
// s[i] == ESC. An unterminated sequence swallows the rest of the string.
func skipEscape(s string, i int) int {
	i++ // ESC
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[': // CSI: parameter/intermediate bytes, then a final byte 0x40–0x7e
		for i++; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return i
	case ']', 'P', '_', '^', 'X': // OSC, DCS, APC, PM, SOS: until BEL or ST (ESC \)
		for i++; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return i
	default: // nF/Fp/Fe/Fs forms: intermediates 0x20–0x2f, then one final byte (ESC 7, ESC ( B)
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			i++
		}
		return i
	}
}
