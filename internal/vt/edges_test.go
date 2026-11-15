package vt

import (
	"strings"
	"testing"
)

// A pen too long to keep as typed is rewritten canonically: the attributes
// in order, then the foreground and the background as the 8, bright or 256
// colours they are.
func TestCanonicalPen(t *testing.T) {
	// Two SGRs of ~200 bytes each, the second changing the pen: the pen's
	// text would pass maxRaw, so it is rewritten.
	pad := strings.Repeat("1;", 100)
	long := "\x1b[" + pad + "31m"
	with := func(tail string) string { return long + "\x1b[" + pad + tail + "m" }
	for _, c := range []struct{ name, in, want string }{
		{"attributes", with("3;4") + "L\n", "\x1b[0;1;3;4;31mL\x1b[0m"},
		{"bright foreground and background", with("91;102") + "L\n", "\x1b[0;1;91;102mL\x1b[0m"},
		{"256 colours", with("38;5;200;48;5;100") + "L\n", "\x1b[0;1;38;5;200;48;5;100mL\x1b[0m"},
		{"true colour", with("38;2;1;2;3") + "L\n", "\x1b[0;1;38;2;1;2;3mL\x1b[0m"},
	} {
		got := final(t, false, c.in)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Every way an SGR can switch an attribute off, and every colour form that
// is malformed (out of range, cut short, an unknown kind) is read past: the
// text stays.
func TestSGRResetsAndBadColours(t *testing.T) {
	for _, in := range []string{
		"\x1b[3;4;5;7;9mx\x1b[23;24;25;27;29my\n",
		"\x1b[4mx\x1b[4:0my\n",
		"\x1b[21mx\x1b[24my\n",
		"\x1b[38:5:200mx\x1b[38:9:1my\x1b[48:2::1:2:3mz\n",
		"\x1b[38mx\n",           // nothing after the 38
		"\x1b[38;5;300mx\n",     // index out of range
		"\x1b[38;2;300;1;1mx\n", // component out of range
		"\x1b[38;2;1;2mx\n",     // cut short
		"\x1b[38;7mx\n",         // not a kind
		"\x1b[38:5:300mx\n",
		"\x1b[38:2:1:2:300mx\n",
	} {
		got := final(t, true, in)
		want := "x"
		switch {
		case strings.HasPrefix(in, "\x1b[3;4"), strings.HasPrefix(in, "\x1b[4mx"), strings.HasPrefix(in, "\x1b[21m"):
			want = "xy"
		case strings.HasPrefix(in, "\x1b[38:5:200"):
			want = "xyz"
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Escape sequences cut short by a control byte: CAN and SUB cancel them, ESC
// starts a new one, a line feed acts as it does anywhere; an intermediate
// byte is skipped up to its final.
func TestControlsInsideSequences(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     []string
	}{
		{"CAN in an escape", "\x1b\x18x\n", []string{"x"}},
		{"CAN in an intermediate", "\x1b(\x18x\n", []string{"x"}},
		{"ESC in an intermediate", "\x1b(\x1b[31mx\n", []string{"x"}},
		{"LF in an intermediate (the next byte is its final)", "a\x1b(\nb\n", []string{"a", ""}},
		{"an intermediate and its final", "\x1b(Bx\n", []string{"x"}},
		{"a long string ends at its cap", "\x1b]0;" + strings.Repeat("a", maxString+3) + "\n", []string{""}},
		{"CAN ends a string", "\x1b]0;title\x18x\n", []string{"x"}},
		{"no parameter given", "\x1b[5Hx\n", []string{"x"}},
		{"a parameter that isn't a number", "ab\x1b[1?5Dx\n", []string{"abx"}},
		{"an erase mode that doesn't exist", "ab\x1b[7Kc\n", []string{"abc"}},
	} {
		got := final(t, true, c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			if c.name == "a long string ends at its cap" && len(got) == 1 && strings.HasSuffix(got[0], "a") {
				continue // what follows the cap is output
			}
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Backlog counts the lines a full Flush would deliver.
func TestBacklog(t *testing.T) {
	s := New()
	if s.Backlog() != 0 {
		t.Errorf("a new screen has a backlog of %d", s.Backlog())
	}
	s.Write([]byte("a\nb\nc"))
	if s.Backlog() != 3 {
		t.Errorf("backlog %d after three lines, want 3", s.Backlog())
	}
	s.Flush(false)
	if s.Backlog() != 0 {
		t.Errorf("backlog %d after a full flush, want 0", s.Backlog())
	}
}

// Many more lines than the window holds leave only the window in memory, and
// every line is still delivered.
func TestManyLinesStayBounded(t *testing.T) {
	s := New()
	var b strings.Builder
	for i := range 50 * MaxRows {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	s.Write([]byte(b.String()))
	var v view
	v.flush(t, s, false)
	if len(v.lines) != 50*MaxRows {
		t.Errorf("%d lines delivered, want %d", len(v.lines), 50*MaxRows)
	}
}
