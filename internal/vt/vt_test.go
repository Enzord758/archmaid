package vt

import (
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// view is what a consumer of the flushes holds: every flush applied as
// Flush documents it.
type view struct{ lines []string }

func (v *view) apply(t testing.TB, replace int, lines []string) {
	t.Helper()
	if replace > len(v.lines) {
		t.Fatalf("replace %d of %d lines", replace, len(v.lines))
	}
	if len(lines) < replace {
		t.Fatalf("replace %d with %d lines: lines may never disappear", replace, len(lines))
	}
	v.lines = append(v.lines[:len(v.lines)-replace], lines...)
}

// flush flushes s into v.
func (v *view) flush(t testing.TB, s *Screen, complete bool) {
	t.Helper()
	rep, lines := s.Flush(complete)
	v.apply(t, rep, lines)
}

// final feeds the chunks to a Screen and returns its lines after one full
// flush at the end, as plain text when plain.
func final(t testing.TB, plain bool, chunks ...string) []string {
	t.Helper()
	s := New()
	for _, c := range chunks {
		s.Write([]byte(c))
	}
	var v view
	v.flush(t, s, false)
	if plain {
		for i, l := range v.lines {
			v.lines[i] = sanitize.String(l, sanitize.Plain)
		}
	}
	return v.lines
}

func TestLineSemantics(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"plain lines", []string{"a\nb\n"}, []string{"a", "b"}},
		{"empty lines kept", []string{"a\n\n\nb"}, []string{"a", "", "", "b"}},
		{"no trailing newline", []string{"a\nb"}, []string{"a", "b"}},
		{"CRLF (a pty's ONLCR)", []string{"one\r\ntwo\r\n"}, []string{"one", "two"}},
		{"\\r progress bar", []string{"[#   ]  25%\r[##  ]  50%\r", "[####] 100%\n"}, []string{"[####] 100%"}},
		{"\\r over longer text keeps its tail, as a terminal", []string{"abcdef\rXY\n"}, []string{"XYcdef"}},
		{"\\r then EL", []string{"abcdef\rXY\x1b[K\n"}, []string{"XY"}},
		{"EL 1 and 2", []string{"abcdef\x1b[3D\x1b[1K|\nxyz\x1b[2Kq\n"}, []string{"   |ef", "   q"}},
		{"backspace", []string{"ab\bc\n"}, []string{"ac"}},
		{"tab to the next stop", []string{"a\tb\n"}, []string{"a       b"}},
		{"cursor up rewrites", []string{"line1\nline2\n\x1b[1Aline2-new\n"}, []string{"line1", "line2-new"}},
		{"cursor up two, CNL down", []string{"a\nb\nc\n\x1b[3FA\x1b[2EC\n"}, []string{"A", "b", "C"}},
		{"CHA", []string{"hello\x1b[1GJ\x1b[5Go!\n"}, []string{"Jello!"}},
		{"CUF over blanks", []string{"a\x1b[3Cb\n"}, []string{"a   b"}},
		{"ECH, DCH, ICH", []string{"abcdef\x1b[1G\x1b[2X\n", "abcdef\x1b[1G\x1b[2P\n", "abcdef\x1b[1G\x1b[2@\n"}, []string{"  cdef", "cdef", "  abcdef"}},
		{"save and restore", []string{"a\x1b7\nb\nc\x1b8X\n"}, []string{"aX", "b", "c"}},
		{"ED 0 clears below", []string{"a\nb\nc\x1b[2A\x1b[J\n"}, []string{"a", "", ""}},
		{"ED 2 is ignored", []string{"keep\n\x1b[2J\x1b[3Jnext\n"}, []string{"keep", "next"}},
		{"CUD stops at the line after the last", []string{"a\n\x1b[9Bb\n"}, []string{"a", "b"}},
		{"OSC title and clipboard dropped", []string{"\x1b]0;title\x07x\x1b]52;c;aGk=\x1b\\y\n"}, []string{"xy"}},
		{"private CSI dropped", []string{"\x1b[?25l\x1b[?2026hok\x1b[?2026l\x1b[?25h\n"}, []string{"ok"}},
		{"DCS dropped", []string{"\x1bPq#0;2;0;0;0\x1b\\z\n"}, []string{"z"}},
		{"an unterminated OSC ends with its line", []string{"a\x1b]8;;https://x", "\nb\n"}, []string{"a", "b"}},
		{"unterminated DCS, SOS, PM, APC end with their line", []string{"1\x1bPq\n2\x1bXs\n3\x1b^p\n4\x1b_a\n5\n"}, []string{"1", "2", "3", "4", "5"}},
		{"split escape across writes", []string{"x\n\x1b", "[", "1", "A", "y\n"}, []string{"y"}},
		{"split rune across writes", []string{"\xe2", "\xa0", "\x8b spin\n"}, []string{"⠋ spin"}},
		{"invalid UTF-8", []string{"a\xffb\n"}, []string{"a�b"}},
		{"C1 and hidden runes dropped", []string{"a\u0085b‮c​d\n"}, []string{"abcd"}},
		{"BEL and NUL dropped", []string{"a\a\x00b\n"}, []string{"ab"}},
		{"wide runes take two cells", []string{"日本\x1b[2Dx\n"}, []string{"日x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := final(t, true, c.in...)
			if !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSGR(t *testing.T) {
	cases := []struct{ in, want string }{
		// the child's own sequences are kept as it wrote them
		{"\x1b[1;31mred\x1b[0m plain\n", "\x1b[1;31mred\x1b[0m plain"},
		{"\x1b[1m\x1b[34m== x ==\x1b[0m\n", "\x1b[1m\x1b[34m== x ==\x1b[0m"},
		{"\x1b[38;5;196mx\x1b[48;2;1;2;3my\x1b[m\n", "\x1b[38;5;196mx\x1b[48;2;1;2;3my\x1b[0m"},
		{"\x1b[38:2::10:20:30mc\x1b[39m\n", "\x1b[38:2::10:20:30mc\x1b[0m"},
		{"\x1b[92;101mb\x1b[22;49m\n", "\x1b[92;101mb\x1b[0m"},
		{"\x1b[1mB\x1b[22mn\n", "\x1b[1mB\x1b[0mn"},                            // back to the default is a reset
		{"\x1b[31mr\x1b[32mg\n", "\x1b[31mr\x1b[32mg\x1b[0m"},                  // a pen that continues the last
		{"\x1b[32mgg\x1b[0m\r\x1b[31mr\n", "\x1b[31mr\x1b[0m\x1b[32mg\x1b[0m"}, // one that doesn't: reset first
		{"\x1b[0;33my\x1b[0;34mb\n", "\x1b[0;33my\x1b[0;34mb\x1b[0m"},          // one that resets itself
		{strings.Repeat("\x1b[1m", 100) + "L\n", "\x1b[1mL\x1b[0m"},            // what changes nothing is not kept
		{"\x1b[8mhidden\x1b[28m\n", "hidden"},                                  // conceal is never kept
		{"\x1b[8;1mB\x1b[08;31mr\n", "\x1b[1mB\x1b[31mr\x1b[0m"},               // nor with another change
		{"\x1b[7m  \x1b[0m\n", "\x1b[7m  \x1b[0m"},                             // reverse blanks show
		{"x\x1b[41m\x1b[K\x1b[0m\n", "x"},                                      // EL with no cells to fill
		{"ab\x1b[44m\x1b[1G\x1b[1K\n", "\x1b[0;44m \x1b[0mb"},                  // an erase shows the background
		{"\x1b[1;2;3;4;5;7;9mS\n", "\x1b[1;2;3;4;5;7;9mS\x1b[0m"},              // every attribute
		{"\x1b[58;5;3m\x1b[58;2;1;2;3mu\n", "u"},                               // underline colour parsed past
		{"\x1b[38;5mx\n", "x"},                                                 // short sequence
	}
	for _, c := range cases {
		got := final(t, false, c.in)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
		if got := final(t, false, c.in); len(got) == 1 && sanitize.String(got[0], sanitize.KeepSGR) != got[0] {
			t.Errorf("%q: output %q is not clean SGR", c.in, got[0])
		}
	}
	// a pen whose text keeps growing is cut back to its canonical SGR
	got := final(t, false, strings.Repeat("\x1b[31m\x1b[32m", 1000)+"L\n")
	if len(got) != 1 || len(got[0]) > maxRaw+maxCSI+16 || sanitize.String(got[0], sanitize.Plain) != "L" {
		t.Errorf("a long run of SGR: %q", got)
	}
}

// TestOllamaLikeSpinner: ollama's progress redraws its block of states
// with ESC[A per line above, ESC[1G and ESC[K after each state, joined by
// "\n". Only the final frame remains.
func TestOllamaLikeSpinner(t *testing.T) {
	frames := []string{"⠋", "⠙", "⠹", "⠸"}
	var in []string
	pos := 0
	for i, f := range frames {
		var b strings.Builder
		b.WriteString("\x1b[?25l")
		for range pos {
			b.WriteString("\x1b[A")
		}
		b.WriteString("\x1b[1G")
		states := []string{"pulling manifest " + f}
		if i >= 2 {
			states = []string{"pulling manifest", fmt.Sprintf("pulling 6a07 %d%% ▕██▏", i*25)}
		}
		for j, s := range states {
			b.WriteString(s + "\x1b[K")
			if j < len(states)-1 {
				b.WriteString("\n")
			}
		}
		pos = len(states) - 1
		in = append(in, b.String())
	}
	in = append(in, "\x1b[?25h\n")
	got := final(t, true, in...)
	want := []string{"pulling manifest", "pulling 6a07 75% ▕██▏"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestFlushRewrites: a delivered line that changes comes back as a
// rewrite of the tail; complete lines go out without the unfinished one.
func TestFlushRewrites(t *testing.T) {
	s := New()
	var v view
	s.Write([]byte("a\nb\nprog 1"))
	if s.Ready() != 2 || !s.Partial() || s.Rewrite() {
		t.Fatalf("ready %d partial %v rewrite %v", s.Ready(), s.Partial(), s.Rewrite())
	}
	rep, lines := s.Flush(true)
	if rep != 0 || !slices.Equal(lines, []string{"a", "b"}) {
		t.Fatalf("complete flush: %d %q", rep, lines)
	}
	v.apply(t, rep, lines)
	v.flush(t, s, false) // the partial line
	s.Write([]byte("\rprog 2"))
	if !s.Rewrite() || s.Ready() != 0 {
		t.Fatal("a changed delivered line is a rewrite")
	}
	rep, lines = s.Flush(false)
	if rep != 1 || !slices.Equal(lines, []string{"prog 2"}) {
		t.Fatalf("rewrite: %d %q", rep, lines)
	}
	v.apply(t, rep, lines)
	s.Write([]byte("\n\x1b[3Ax"))
	rep, lines = s.Flush(false)
	if rep != 3 || !slices.Equal(lines, []string{"x", "b", "prog 2"}) {
		t.Fatalf("cursor-up rewrite: %d %q", rep, lines)
	}
	v.apply(t, rep, lines)
	if want := []string{"x", "b", "prog 2"}; !slices.Equal(v.lines, want) {
		t.Fatalf("view %q, want %q", v.lines, want)
	}
	if rep, lines := s.Flush(false); rep != 0 || lines != nil {
		t.Fatalf("nothing changed, got %d %q", rep, lines)
	}
}

// TestCursorStaysInItsWindow: the cursor reaches only the last MaxRows
// lines; a line older than that never changes (and a step's Screen is
// its own, so no earlier step's line is reachable at all).
func TestCursorStaysInItsWindow(t *testing.T) {
	s := New()
	var v view
	for i := range 200 {
		fmt.Fprintf(s, "line %d\n", i)
		if i%7 == 0 {
			v.flush(t, s, true)
		}
	}
	v.flush(t, s, false)
	s.Write([]byte("\x1b[999AX\x1b[999F\x1b7\x1b8Y\x1bMZ\n"))
	rep, lines := s.Flush(false)
	if rep > MaxRows {
		t.Fatalf("replace %d reaches past the window (%d)", rep, MaxRows)
	}
	v.apply(t, rep, lines)
	if len(v.lines) != 200 {
		t.Fatalf("%d lines, want 200", len(v.lines))
	}
	for i := 0; i < 200-MaxRows; i++ {
		if want := fmt.Sprintf("line %d", i); v.lines[i] != want {
			t.Fatalf("line %d changed to %q", i, v.lines[i])
		}
	}
	if got := v.lines[200-MaxRows]; !strings.HasPrefix(got, "YZ") {
		t.Fatalf("the window's first line %q, want the cursor's writes there", got)
	}
}

// TestBounds: a hostile stream can't grow a line, the window, the styles
// or a parsed sequence past their caps.
func TestBounds(t *testing.T) {
	s := New()
	s.Write([]byte(strings.Repeat("x", 3*MaxCols)))
	s.Write([]byte("日\n"))
	_, lines := s.Flush(false)
	if utf8.RuneCountInString(lines[0]) != MaxCols {
		t.Fatalf("a line holds %d cells, want MaxCols", utf8.RuneCountInString(lines[0]))
	}
	// every cell its own truecolour style
	var b strings.Builder
	for i := range MaxCols {
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm#", i&0xff, i>>8&0xff, 7)
	}
	b.WriteString("\n")
	s.Write([]byte(b.String()))
	_, lines = s.Flush(false)
	if len(lines[0]) > MaxLineBytes+4*MaxCols+64 {
		t.Fatalf("a rendered line is %d bytes", len(lines[0]))
	}
	if len(s.styles) > MaxStyles {
		t.Fatalf("%d styles kept", len(s.styles))
	}
	// an endless CSI and an endless OSC: the OSC is dropped up to
	// maxString bytes, then its bytes are output (TestUnterminatedString)
	s.Write([]byte("\x1b[" + strings.Repeat("1;", 1<<16) + "mA\n"))
	s.Write([]byte("\x1b]" + strings.Repeat("o", 1<<16)))
	s.Write([]byte("\aB\n"))
	_, lines = s.Flush(false)
	if got := []string{sanitize.String(lines[0], sanitize.Plain), lines[1]}; !slices.Equal(got, []string{"A", strings.Repeat("o", MaxCols)}) {
		t.Fatalf("after long sequences: %q", lines)
	}
	if cap(s.csi) > 2*maxCSI {
		t.Fatalf("CSI buffer grew to %d", cap(s.csi))
	}
}

// TestUnterminatedString (review #135): a string (OSC, DCS, SOS, PM,
// APC) the child never terminates is dropped up to maxString bytes, split
// across writes or not, and what follows is output again. Before, one
// "ESC ]" swallowed the rest of the step, on the Run page and in its log.
func TestUnterminatedString(t *testing.T) {
	for _, intro := range []string{"\x1b]", "\x1bP", "\x1bX", "\x1b^", "\x1b_"} {
		body := strings.Repeat("x", maxString+100)
		got := final(t, true, "a\n"+intro+body[:1000], body[1000:], "tail\nnext\n")
		if want := []string{"a", strings.Repeat("x", 100) + "tail", "next"}; !slices.Equal(got, want) {
			t.Errorf("%q: got %q…, want %q…", intro, head(got), head(want))
		}
	}
	// a terminated string as long as the cap is still dropped whole
	if got := final(t, true, "\x1b]0;"+strings.Repeat("t", maxString-3)+"\aok\n"); !slices.Equal(got, []string{"ok"}) {
		t.Errorf("a terminated string just under the cap: %q", head(got))
	}
}

func head(lines []string) []string {
	out := make([]string, 0, 4)
	for _, l := range lines[:min(len(lines), 4)] {
		if len(l) > 40 {
			l = l[:20] + "…" + l[len(l)-20:]
		}
		out = append(out, l)
	}
	return out
}

// TestNoNewlineForever: output that never ends a line, rewritten forever,
// is one line.
func TestNoNewlineForever(t *testing.T) {
	s := New()
	var v view
	for i := range 10000 {
		fmt.Fprintf(s, "\r%3d%%", i%100)
		if i%100 == 0 {
			v.flush(t, s, false)
		}
	}
	v.flush(t, s, false)
	if len(v.lines) != 1 || v.lines[0] != " 99%" {
		t.Fatalf("got %q", v.lines)
	}
}

// checkInvariants feeds data in random pieces with random flushes and
// checks what every Screen promises: the caps hold, every flush is a valid
// rewrite of only reachable lines, every line is clean SGR text, and the
// view built from the flushes ends equal to one flush of the same input.
func checkInvariants(t *testing.T, data []byte, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 1))
	s := New()
	var v view
	for rest := data; len(rest) > 0; {
		n := min(len(rest), 1+rng.IntN(64))
		s.Write(rest[:n])
		rest = rest[n:]
		if len(s.win) > MaxRows {
			t.Fatalf("window %d rows", len(s.win))
		}
		for _, row := range s.win {
			if len(row) > MaxCols {
				t.Fatalf("row of %d cells", len(row))
			}
			if i := halfWide(s, row); i >= 0 {
				t.Fatalf("cell %d of %q is half a wide rune", i, s.render(row))
			}
		}
		if s.r < 0 || s.r > len(s.win) || s.c < 0 || s.c > MaxCols {
			t.Fatalf("cursor %d,%d outside %d rows", s.r, s.c, len(s.win))
		}
		if len(s.styles) > MaxStyles || len(s.csi) > maxCSI {
			t.Fatalf("%d styles, csi %d", len(s.styles), len(s.csi))
		}
		if rng.IntN(3) == 0 {
			sent := s.sent
			rep, lines := s.Flush(rng.IntN(2) == 0)
			if rep > MaxRows+len(s.pend) && rep > sent-(s.base-MaxRows) {
				t.Fatalf("replace %d reaches before the window", rep)
			}
			for _, l := range lines {
				if sanitize.String(l, sanitize.KeepSGR) != l {
					t.Fatalf("line %q is not clean", l)
				}
				if len(l) > MaxLineBytes+4*MaxCols+64 {
					t.Fatalf("line of %d bytes", len(l))
				}
			}
			v.apply(t, rep, lines)
		}
	}
	v.flush(t, s, false)
	if want := final(t, false, string(data)); !slices.Equal(v.lines, want) {
		t.Fatalf("flushes built %q, one flush gives %q", v.lines, want)
	}
}

// halfWide is the first cell of row that is half of a wide rune without
// its other half, or -1: every wide rune is its head and a cont cell.
func halfWide(s *Screen, row []cell) int {
	wide := func(i int) bool { return row[i].r > 0 && s.width.RuneWidth(row[i].r) == 2 }
	for i := range row {
		switch {
		case row[i].r == cont && (i == 0 || !wide(i-1)):
			return i
		case wide(i) && (i+1 >= len(row) || row[i+1].r != cont):
			return i
		}
	}
	return -1
}

// TestDCHICHKeepWideRunesWhole: deleting or inserting cells keeps every
// wide rune whole or blanks both halves: where the cells go or come, and
// where the line is cut at MaxCols.
func TestDCHICHKeepWideRunesWhole(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"delete from inside a wide rune", "日本x\x1b[2G\x1b[2P\n", "  x"}, // not "日x": 日 never takes 本's second half
		{"delete up to inside a wide rune", "a日本\x1b[1G\x1b[2P\n", " 本"},
		{"insert inside a wide rune", "日本\x1b[2G\x1b[2@\n", "    本"},
		{"insert before a wide rune", "a日\x1b[2G\x1b[1@\n", "a 日"},
	}
	for _, c := range cases {
		if got := final(t, true, c.in); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q gives %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// a full line of wide runes pushed one cell right: the last rune
	// loses its second half off the line, and is blanked
	s := New()
	s.Write([]byte(strings.Repeat("日", MaxCols/2) + "\r\x1b[@"))
	row := s.win[s.r]
	if len(row) != MaxCols || row[0] != (cell{}) || row[1].r != '日' || row[MaxCols-1] != (cell{}) || halfWide(s, row) >= 0 {
		t.Errorf("a full line after ICH: %d cells, first %v %v, last %v, half at %d", len(row), row[0], row[1], row[MaxCols-1], halfWide(s, row))
	}
}

func TestInvariantsRandom(t *testing.T) {
	alphabet := []string{"a", "bc", "日", "⠋", "\n", "\r", "\b", "\t", "\x1b[A", "\x1b[3A", "\x1b[B", "\x1b[2C", "\x1b[D",
		"\x1b[1G", "\x1b[K", "\x1b[1K", "\x1b[2K", "\x1b[J", "\x1b[1J", "\x1b[2X", "\x1b[P", "\x1b[3@", "\x1b7", "\x1b8",
		"\x1bM", "\x1bD", "\x1bE", "\x1b[s", "\x1b[u", "\x1b[1;31m", "\x1b[0m", "\x1b[48;5;4m", "\x1b[38;2;1;2;3m",
		"\x1b]0;t\a", "\x1b]8;;u", "\x1bPq", "\x1b[08;1m", "\x1b[?25l", "\xff", "\xe2\xa0", "\x1b", "\x1b[", "\x9b", "‮", "\x1b[99E", "\x1b[99F"}
	for seed := range uint64(300) {
		rng := rand.New(rand.NewPCG(seed, 2))
		var b strings.Builder
		for range 50 + rng.IntN(400) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
			if rng.IntN(40) == 0 {
				b.WriteString(strings.Repeat("\n", rng.IntN(3*MaxRows)))
			}
		}
		checkInvariants(t, []byte(b.String()), seed)
	}
}

// FuzzScreen: the same invariants for any bytes.
func FuzzScreen(f *testing.F) {
	for _, s := range []string{"a\nb", "x\r\x1b[K\x1b[1A", "\x1b[38;2;1;2;3mz\x1b[0m\n", "\x1b]0;x\a\x1bP\x1b\\", "日本\x1b[1D\x1b[@", "\xe2\xa0\x8b\r",
		"a\x1b]8;;https://x\nb\n", "\x1b_" + strings.Repeat("a", maxString+8) + "\n"} {
		f.Add([]byte(s), uint64(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed uint64) {
		checkInvariants(t, data, seed)
	})
}

func BenchmarkScreenPlainLines(b *testing.B) {
	line := []byte(strings.Repeat("downloading package-1.2.3 ", 3) + "\n")
	data := []byte(strings.Repeat(string(line), 1000))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		s := New()
		s.Write(data)
		s.Flush(false)
	}
}

// BenchmarkScreenDCHICH (review #135): the costliest input per byte, a
// full line (wide runes, so the halves must stay whole) under insert and
// delete at its start: 7 bytes shift the whole line each way.
func BenchmarkScreenDCHICH(b *testing.B) {
	full := []byte(strings.Repeat("日x", MaxCols/3) + "\r")
	data := []byte(strings.Repeat("\x1b[@\x1b[P\r", 1000))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		s := New()
		s.Write(full)
		s.Write(data)
	}
}
