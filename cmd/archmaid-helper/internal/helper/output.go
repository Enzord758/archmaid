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
	EventCheck    = "check"
	EventResult   = "result"
	// EventDone ends one part of a combined verb (full-cleanup).
	EventDone = "done"
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
	// Part is the verb a combined verb's step stands for (full-cleanup's
	// "paccache", "remove-orphans" …); omitted on a plain verb's progress.
	Part string `json:"part,omitempty"`
}

// doneEvent ends part Step of a combined verb: OK, or Code (ExitFailed,
// ExitStale, ExitBusy) with Error.
type doneEvent struct {
	V     int    `json:"v"`
	T     string `json:"t"`
	Step  int    `json:"step"`
	Of    int    `json:"of"`
	Part  string `json:"part"`
	OK    bool   `json:"ok"`
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
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
	// The package plans' additive fields (packages-contract.md section 3);
	// plan-orphans leaves them all out.
	Mode       string   `json:"mode,omitempty"`
	WithConfig bool     `json:"with_config,omitempty"`
	AsDeps     bool     `json:"asdeps,omitempty"`
	Rows       []PkgRow `json:"rows,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// PkgRow is one item of a package plan, same order as the plan's items.
// Dl and Inst are bytes (0: unknown); Old is always "" (an install that
// would upgrade is refused before a plan exists); Repo is "" for a remove.
type PkgRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Old     string `json:"old"`
	Repo    string `json:"repo"`
	Target  bool   `json:"target"`
	Dl      int64  `json:"dl"`
	Inst    int64  `json:"inst"`
}

type checkEvent struct {
	V       int      `json:"v"`
	T       string   `json:"t"`
	Checked []string `json:"checked"`
	Broken  []string `json:"broken"`
}

type resultEvent struct {
	V         int    `json:"v"`
	T         string `json:"t"`
	OK        bool   `json:"ok"`
	Code      int    `json:"code"`
	ChildExit *int   `json:"child_exit"`
	Skipped   []Skip `json:"skipped"`
	Error     string `json:"error,omitempty"`
	// Reason, Names and Count are the package verbs' machine-readable
	// refusal (docs/design/packages-contract.md section 3); additive.
	Reason string   `json:"reason,omitempty"`
	Names  []string `json:"names,omitempty"`
	Count  int      `json:"count,omitempty"`
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

// PartProgress is Progress for a part of a combined verb: part is the verb
// it stands for.
func (e *Emitter) PartProgress(step, of int, part, label string) {
	e.emit(progressEvent{V: SchemaVersion, T: EventProgress, Step: step, Of: of, Label: Sanitize(label), Part: part})
}

// Done ends part `step` of `of` of a combined verb with code (ExitOK or the
// part's failure) and msg.
func (e *Emitter) Done(step, of int, part string, code int, msg string) {
	e.emit(doneEvent{V: SchemaVersion, T: EventDone, Step: step, Of: of, Part: part,
		OK: code == ExitOK, Code: code, Error: Sanitize(msg)})
}

// Line relays one line of a child's output; stream is "out" or "err". It
// is the one text field that keeps colour: SanitizeLine keeps well-formed
// SGR sequences (pacman's --color always on the commands the helper only
// relays, helper.go) and drops every other escape, as Sanitize does.
// Nothing the helper parses reaches it coloured: a command whose output
// is parsed runs with --color never.
func (e *Emitter) Line(stream, text string) {
	e.emit(lineEvent{V: SchemaVersion, T: EventLine, Stream: stream, Text: SanitizeLine(text)})
}

// Plan reports an orphan-removal plan.
func (e *Emitter) Plan(p Plan) {
	e.emit(planEvent{
		V: SchemaVersion, T: EventPlan,
		Targets: sanitizeAll(p.Targets), Items: sanitizeAll(p.Items),
		Total: len(p.Items), Hash: p.Hash,
		Mode: p.Mode, WithConfig: p.WithConfig, AsDeps: p.AsDeps,
		Rows: sanitizeRows(p.Rows), Warnings: sanitizeAll(p.Warnings),
	})
}

// Check reports a check-files run (named packages or all): checked are the
// packages whose files were checked, broken those of them missing files.
// Both are always arrays, never null.
func (e *Emitter) Check(checked, broken []string) {
	e.emit(checkEvent{V: SchemaVersion, T: EventCheck, Checked: sanitizeAll(checked), Broken: sanitizeAll(broken)})
}

// Result is the final line; exactly one is written per invocation.
func (e *Emitter) Result(code int, childExit *int, skipped []Skip, msg string) {
	e.ResultWhy(code, childExit, skipped, msg, "", nil, 0)
}

// ResultWhy is Result with the package verbs' refusal reason, names and count.
func (e *Emitter) ResultWhy(code int, childExit *int, skipped []Skip, msg, reason string, names []string, count int) {
	clean := make([]Skip, 0, len(skipped))
	for _, s := range skipped {
		clean = append(clean, Skip{Name: Sanitize(s.Name), Reason: Sanitize(s.Reason)})
	}
	e.emit(resultEvent{
		V: SchemaVersion, T: EventResult,
		OK: code == ExitOK, Code: code, ChildExit: childExit,
		Skipped: clean, Error: Sanitize(msg),
		Reason: Sanitize(reason), Names: sanitizeNames(names), Count: count,
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
func Sanitize(s string) string { return sanitize(s, false) }

// SanitizeLine is Sanitize for a line of child output (the line event's
// text), except that it keeps an escape sequence that is a well-formed
// SGR (isSGR: colour and text style). A cursor move, an erase, a title or
// hyperlink (OSC), a private CSI (ESC [ ? 25 l) or a malformed sequence
// goes whole, as in Sanitize. The client filters the line again
// (internal/helperclient, sanitize.KeepSGR, which also drops SGR's
// conceal) and the logs keep no escape at all (sanitize.Plain).
func SanitizeLine(s string) string { return sanitize(s, true) }

// sanitize is Sanitize, keeping well-formed SGR sequences when keepSGR.
func sanitize(s string, keepSGR bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			end := skipEscape(s, i)
			if keepSGR && isSGR(s[i:end]) {
				b.WriteString(s[i:end])
			}
			i = end
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

// isSGR reports whether seq, one whole escape sequence as skipEscape cuts
// it, is a well-formed SGR: ESC [, parameters of only digits, ';' and ':'
// (no private marker, no intermediate byte), then the final byte 'm'.
func isSGR(seq string) bool {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return false
	}
	for i := 2; i < len(seq)-1; i++ {
		if c := seq[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
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

func sanitizeNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return sanitizeAll(in)
}

func sanitizeRows(in []PkgRow) []PkgRow {
	if len(in) == 0 {
		return nil
	}
	out := make([]PkgRow, len(in))
	for i, r := range in {
		r.Name, r.Version, r.Old, r.Repo = Sanitize(r.Name), Sanitize(r.Version), Sanitize(r.Old), Sanitize(r.Repo)
		out[i] = r
	}
	return out
}
