package helper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1;38;5;196mbold\x1b[m!", "bold!"},
		{"title\x1b]0;pwned\x07after", "titleafter"},
		{"link \x1b]8;;http://x\x1b\\here\x1b]8;;\x1b\\", "link here"},
		{"dcs\x1bPq#0\x1b\\end", "dcsend"},
		{"\x1b7saved\x1b8", "saved"},
		{"\x1b(Bcharset", "charset"},
		{"bell\x07 bs\x08 nul\x00 del\x7f", "bell bs nul del"},
		{"c1 \u009b31mcsi", "c1 31mcsi"},
		{"tab\there", "tab here"},
		{"cr\rlf\n", "crlf"},
		{"bidi ‮evil‬ ⁦iso⁩", "bidi evil iso"},
		{"sep x", "sepx"},
		{"bad utf8 \xff\xfe", "bad utf8 ��"},
		{"unicode ok: æøå ✓", "unicode ok: æøå ✓"},
		{"unterminated \x1b[31", "unterminated "},
		{"trailing esc\x1b", "trailing esc"},
	}
	// Invisible characters that can reorder, hide or fake text.
	for _, r := range []rune{
		0x200b, 0x200c, 0x200d, 0x200e, 0x200f, // zero-width space/joiners, LRM/RLM
		0x061c,                                 // Arabic letter mark
		0xfeff,                                 // BOM / zero-width no-break space
		0x2060, 0x2061, 0x2062, 0x2063, 0x2064, // word joiner, invisible operators
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e, // bidi embeddings/overrides
		0x2066, 0x2067, 0x2068, 0x2069, // bidi isolates
		0xe0000, 0xe0001, 0xe0020, 0xe0041, 0xe007f, // tag block
		0x2028, 0x2029, // line/paragraph separators
		0x115f, 0x1160, 0x3164, 0xffa0, // Hangul fillers
	} {
		cases = append(cases, struct{ in, want string }{"a" + string(r) + "b", "ab"})
	}
	// Visible neighbours survive.
	for _, s := range []string{"æ", "‐", "ㅣ", "ﾡ", "\U0001f600", "中"} {
		cases = append(cases, struct{ in, want string }{s, s})
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEveryTextFieldIsSanitized drives hostile child output and package
// names through a real run and checks no control character survives into
// any emitted line.
func TestEveryTextFieldIsSanitized(t *testing.T) {
	f := newFake().on(paccacheArgv("3"), reply{out: "\x1b]0;title\x07ok \x1b[31mred\n", err: "e\x07rr\n", exit: 7})
	o, out := rootOpts(t, f)
	Run([]string{"paccache", "3"}, o)
	Run([]string{"bogus\x1b[2J\x07verb"}, o)
	for l := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case string:
				if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f }) {
					t.Errorf("control character in %q (line %s)", v, l)
				}
			case map[string]any:
				for _, x := range v {
					walk(x)
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			}
		}
		walk(ev)
	}
}

// TestJSONSchema pins the v1 wire format: field names and types per event.
func TestJSONSchema(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf)
	e.Start("check-files", false)
	e.Progress(1, 2, "label")
	e.Line("out", "text")
	e.Plan(Plan{Targets: []string{"a"}, Items: []string{"a 1-1"}, Hash: hash0})
	exit := 1
	e.Result(ExitOK, &exit, []Skip{{"x", "why"}}, "")
	e.Result(ExitUsage, nil, nil, "bad")

	want := []map[string]string{ // field → JSON type
		{"v": "number", "t": "string", "verb": "string", "version": "string"},
		{"v": "number", "t": "string", "step": "number", "of": "number", "label": "string"},
		{"v": "number", "t": "string", "stream": "string", "text": "string"},
		{"v": "number", "t": "string", "targets": "array", "items": "array", "total": "number", "hash": "string"},
		{"v": "number", "t": "string", "ok": "bool", "code": "number", "child_exit": "number", "skipped": "array"},
		{"v": "number", "t": "string", "ok": "bool", "code": "number", "child_exit": "null", "skipped": "array", "error": "string"},
	}
	types := []string{EventStart, EventProgress, EventLine, EventPlan, EventResult, EventResult}
	sc := bufio.NewScanner(&buf)
	for i := 0; sc.Scan(); i++ {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["v"] != float64(1) || ev["t"] != types[i] {
			t.Errorf("line %d: v/t = %v/%v", i, ev["v"], ev["t"])
		}
		keys := slices.Sorted(func(y func(string) bool) {
			for k := range ev {
				if !y(k) {
					return
				}
			}
		})
		wantKeys := slices.Sorted(func(y func(string) bool) {
			for k := range want[i] {
				if !y(k) {
					return
				}
			}
		})
		if !slices.Equal(keys, wantKeys) {
			t.Errorf("line %d keys = %v, want %v", i, keys, wantKeys)
		}
		for k, typ := range want[i] {
			if got := jsonType(ev[k]); got != typ {
				t.Errorf("line %d: %s is %s, want %s", i, k, got, typ)
			}
		}
	}
	// skipped is [] (never null) when nothing was skipped.
	var b2 bytes.Buffer
	NewEmitter(&b2).Result(ExitOK, nil, nil, "")
	if !strings.Contains(b2.String(), `"skipped":[]`) {
		t.Errorf("empty skipped must be [], got %s", b2.String())
	}
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "?"
}

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) { f.n++; return 0, errors.New("EPIPE") }

func TestEmitterStopsAfterWriteError(t *testing.T) {
	w := &failWriter{}
	e := NewEmitter(w)
	e.Line("out", "a")
	e.Line("out", "b")
	e.Result(0, nil, nil, "")
	if w.n != 1 {
		t.Errorf("kept writing after an error: %d writes", w.n)
	}
}

func TestSplitLines(t *testing.T) {
	in := "one\ntwo\r\nthree\rfour\n\nlast" + "\n" + strings.Repeat("x", maxLine+10)
	sc := bufio.NewScanner(strings.NewReader(in))
	sc.Buffer(nil, 2*maxLine)
	sc.Split(SplitLines)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	want := []string{"one", "two", "three", "four", "", "last", strings.Repeat("x", maxLine), strings.Repeat("x", 10)}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
