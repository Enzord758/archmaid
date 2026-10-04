package runview

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

func idx(n uint8) Color          { return Color{Kind: ColorIndex, Index: n} }
func rgb(r, g, b uint8) Color    { return Color{Kind: ColorRGB, R: r, G: g, B: b} }
func sp(a, b int, st Style) Span { return Span{Start: a, End: b, Style: st} }

func TestParseSGR(t *testing.T) {
	bold := Style{Bold: true}
	red := Style{FG: idx(1)}
	cases := []struct {
		name, in string
		want     Line
	}{
		{"plain", "plain text", Line{Text: "plain text"}},
		{"empty", "", Line{}},
		{"pacman arrow", "\x1b[1;32m==>\x1b[0m Nothing to do.", Line{"==> Nothing to do.", []Span{sp(0, 3, Style{Bold: true, FG: idx(2)})}}},
		{"empty params reset", "\x1b[31ma\x1b[mb", Line{"ab", []Span{sp(0, 1, red)}}},
		{"unclosed style runs to the end", "x\x1b[1my", Line{"xy", []Span{sp(1, 2, bold)}}},
		{"escapes without text leave no span", "\x1b[31m\x1b[0ma", Line{Text: "a"}},
		{"style change without text", "\x1b[31m\x1b[1ma", Line{"a", []Span{sp(0, 1, Style{Bold: true, FG: idx(1)})}}},
		{"same style again merges", "\x1b[31ma\x1b[0m\x1b[31mb", Line{"ab", []Span{sp(0, 2, red)}}},
		{"bright and backgrounds", "\x1b[91;42ma\x1b[103;30mb", Line{"ab", []Span{sp(0, 1, Style{FG: idx(9), BG: idx(2)}), sp(1, 2, Style{FG: idx(0), BG: idx(11)})}}},
		{"default colours", "\x1b[31;41ma\x1b[39mb\x1b[49mc", Line{"abc", []Span{sp(0, 1, Style{FG: idx(1), BG: idx(1)}), sp(1, 2, Style{BG: idx(1)})}}},
		{"256 colour", "\x1b[38;5;208ma\x1b[48;5;17mb", Line{"ab", []Span{sp(0, 1, Style{FG: idx(208)}), sp(1, 2, Style{FG: idx(208), BG: idx(17)})}}},
		{"24-bit", "\x1b[38;2;1;2;3ma", Line{"a", []Span{sp(0, 1, Style{FG: rgb(1, 2, 3)})}}},
		{"colon 256", "\x1b[38:5:196ma", Line{"a", []Span{sp(0, 1, Style{FG: idx(196)})}}},
		{"colon 24-bit with colour space", "\x1b[38:2::10:20:30ma", Line{"a", []Span{sp(0, 1, Style{FG: rgb(10, 20, 30)})}}},
		{"colon 24-bit without colour space", "\x1b[48:2:10:20:30ma", Line{"a", []Span{sp(0, 1, Style{BG: rgb(10, 20, 30)})}}},
		{"params after an extended colour", "\x1b[38;5;1;1ma", Line{"a", []Span{sp(0, 1, Style{FG: idx(1), Bold: true})}}},
		{"truncated extended colour ignored", "\x1b[38;5ma", Line{Text: "a"}},
		{"out-of-range extended colour ignored", "\x1b[38;5;300ma", Line{Text: "a"}},
		{"attributes on and off", "\x1b[1;2;3;4;7;9ma\x1b[22;23;24;27;29mb", Line{"ab", []Span{sp(0, 1, Style{Bold: true, Dim: true, Italic: true, Underline: true, Reverse: true, Strike: true})}}},
		{"underline styles", "\x1b[4:3ma\x1b[4:0mb\x1b[21mc", Line{"abc", []Span{sp(0, 1, Style{Underline: true}), sp(2, 3, Style{Underline: true})}}},
		{"unknown codes ignored", "\x1b[5;53;73ma", Line{Text: "a"}},
		{"utf-8 byte offsets", "✔ \x1b[32mok\x1b[0m", Line{"✔ ok", []Span{sp(4, 6, Style{FG: idx(2)})}}},
		// Never interpreted, only dropped: sanitize removes these before a
		// line reaches the view; the parser holds anyway.
		{"cursor movement dropped", "a\x1b[2Ab\x1b[Kc", Line{Text: "abc"}},
		{"private CSI dropped", "\x1b[?25la", Line{Text: "a"}},
		{"two-byte escape dropped", "\x1b7a\x1bc", Line{Text: "a"}},
		{"unterminated CSI eats the rest", "cut \x1b[1;3", Line{Text: "cut "}},
		{"lone ESC", "lone \x1b", Line{Text: "lone "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseSGR(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseSGR(%q)\n got %+v\nwant %+v", c.in, got, c.want)
			}
			if got := StripSGR(c.in); got != c.want.Text {
				t.Errorf("StripSGR(%q) = %q, want %q", c.in, got, c.want.Text)
			}
		})
	}
}

// onlySGR reports whether every escape in s is a well-formed SGR, the
// contract engine.OutputChunk states for output lines.
func onlySGR(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			continue
		}
		_, end, ok := sgrAt(s, i)
		if !ok {
			return false
		}
		i = end - 1
	}
	return true
}

// TestSanitizedOutputIsOnlySGR asserts the parser's input contract: what
// the engine sends (sanitize.String with KeepSGR) holds no escape but SGR,
// so nothing else can reach the parser to be interpreted.
func TestSanitizedOutputIsOnlySGR(t *testing.T) {
	hostile := []string{
		"a\x1b[2Ab\x1b[Kc", "\x1b[?25lhidden\x1b[?25h", "\x1b]0;pwned\x07ok", "\x1b]52;c;ZXZpbA==\x1b\\ok",
		"\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\", "ok\x1b]0;never ends", "\x1bPq#0;2;0;0;0\x1b\\ok",
		"\x1b(Ba\x1b7b\x1bc", "ok\x1b", "a\u009b31mb", "\x1b[1;8;31mx", "\x1b[31;2;3$|x", "\x1b[>4;2m", "\x1b[=1m",
		"\x1b[1;32m==>\x1b[0m fine", "\x1b[38:2::1:2:3mx\x1b[m",
	}
	for _, in := range hostile {
		out := sanitize.String(in, sanitize.KeepSGR)
		if !onlySGR(out) {
			t.Errorf("sanitize kept a non-SGR escape: %q → %q", in, out)
		}
		if l := ParseSGR(out); strings.IndexByte(l.Text, 0x1b) >= 0 {
			t.Errorf("ESC in parsed text: %q", l.Text)
		}
	}
}

func TestAttrs(t *testing.T) {
	p := FallbackPalette
	if got := (Line{Text: "x"}).Attrs(&p); got != "" {
		t.Errorf("plain line attrs %q", got)
	}
	l := ParseSGR("\x1b[1;31ma\x1b[0m \x1b[7mb\x1b[0m \x1b[2;3;4;9;38;2;1;2;3;48;5;16mc")
	want := "0 1 foreground #c01c28\n0 1 weight bold\n" +
		"2 3 foreground #171421\n2 3 background #d0cfcc\n" +
		"4 5 foreground #010203\n4 5 background #000000\n4 5 foreground-alpha 40000\n4 5 style italic\n4 5 underline single\n4 5 strikethrough true\n"
	if got := l.Attrs(&p); got != want {
		t.Errorf("attrs\n%s\nwant\n%s", got, want)
	}
}

func TestPaletteColor(t *testing.T) {
	p := FallbackPalette
	for c, want := range map[Color]RGB{
		idx(1): p[1], idx(15): p[15], idx(16): {0, 0, 0}, idx(21): {0, 0, 255}, idx(196): {255, 0, 0},
		idx(208): {255, 135, 0}, idx(231): {255, 255, 255}, idx(232): {8, 8, 8}, idx(255): {238, 238, 238},
		rgb(1, 2, 3): {1, 2, 3},
	} {
		if got := p.Color(c); got != want {
			t.Errorf("Color(%+v) = %v, want %v", c, got, want)
		}
	}
}

func writeColours(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dms-colors.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// dank16JSON is a dms-colors.json whose colorN is #0000NN dark, #ff00NN
// light and #00ffNN default (NN = N in hex), without colour skip.
func dank16JSON(mode string, skip int) string {
	var cols []string
	for i := range 16 {
		if i != skip {
			cols = append(cols, fmt.Sprintf(`"color%d": {"dark": "#0000%02x", "light": "#ff00%02x", "default": "#00ff%02x"}`, i, i, i, i))
		}
	}
	return `{"dank16": {` + strings.Join(cols, ", ") + `}, "mode": "` + mode + `"}`
}

func TestLoadDank16(t *testing.T) {
	for _, c := range []struct {
		mode string
		want RGB
	}{{"dark", RGB{0, 0, 5}}, {"light", RGB{255, 0, 5}}, {"", RGB{0, 255, 5}}, {"auto", RGB{0, 255, 5}}} {
		p, err := LoadDank16(writeColours(t, dank16JSON(c.mode, -1)))
		if err != nil {
			t.Fatalf("mode %q: %v", c.mode, err)
		}
		if p[5] != c.want || p[0].B != 0 || p[15].B != 15 {
			t.Errorf("mode %q: color5 %v (want %v), color0 %v, color15 %v", c.mode, p[5], c.want, p[0], p[15])
		}
	}
	for name, body := range map[string]string{
		"missing colour": dank16JSON("dark", 7),
		"bad hex":        strings.Replace(dank16JSON("dark", -1), "#000003", "#00003", 1),
		"not json":       "{",
		"no dank16":      `{"mode": "dark"}`,
	} {
		if _, err := LoadDank16(writeColours(t, body)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if _, err := LoadDank16(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Error("a missing file loaded")
	}
}

func TestLoadPalette(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	if got := Dank16Path(); got != filepath.Join(dir, "DankMaterialShell", "dms-colors.json") {
		t.Errorf("path %q", got)
	}
	if LoadPalette() != FallbackPalette {
		t.Error("no file: not the fallback")
	}
	if err := os.MkdirAll(filepath.Join(dir, "DankMaterialShell"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Dank16Path(), []byte(dank16JSON("dark", -1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := LoadPalette(); p[3] != (RGB{0, 0, 3}) {
		t.Errorf("dank16 not used: %v", p[3])
	}
}

func TestModelKeepsSpans(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{
		engine.QueueStarted{Steps: []engine.PlanStep{{ID: "s", Title: "S"}}},
		engine.StepStarted{ID: "s"},
		engine.OutputChunk{ID: "s", Lines: []string{"\x1b[31mred\x1b[0m", "plain"}},
	})
	if out := m.Output(); !reflect.DeepEqual(out, []string{"── S", "red", "plain"}) {
		t.Fatalf("output %q", out)
	}
	if l, ok := m.ViewLine(1); !ok || l.Text != "red" || len(l.Spans) != 1 {
		t.Errorf("view line 1: %+v %v", l, ok)
	}
	if l, ok := m.ViewLine(0); !ok || l.Spans != nil {
		t.Errorf("a header got spans: %+v", l)
	}
	if _, ok := m.ViewLine(3); ok {
		t.Error("line past the view")
	}
	if _, ok := m.ViewLine(-1); ok {
		t.Error("line before the view")
	}
}

func TestFloodColourScript(t *testing.T) {
	m := NewModel()
	m.Apply(FloodColour(t0, 30, 7, 0).Events())
	out := m.Output()
	for i := 1; i <= 30; i++ {
		if out[i] != FloodColourLine(i, 30) {
			t.Fatalf("line %d %q, want %q", i, out[i], FloodColourLine(i, 30))
		}
		if l, _ := m.ViewLine(i); len(l.Spans) != 7 {
			t.Fatalf("line %d: %d spans, want 7: %+v", i, len(l.Spans), l.Spans)
		}
	}
}

// BenchmarkParseSGR: one colourful flood line (seven spans).
func BenchmarkParseSGR(b *testing.B) {
	line := colourRaw(1234, 50000)
	b.ReportAllocs()
	for b.Loop() {
		ParseSGR(line)
	}
}
