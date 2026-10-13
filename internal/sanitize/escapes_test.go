package sanitize

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// Task #112: the security property of KeepSGR. No escape other than a
// well-formed SGR (ESC [ digits ; : m) may survive, whatever the input, however
// it is cut into chunks or lines, and no control, C1 or format character may
// either. The checks below are the oracle; the hostile inputs are a table
// (the named cases), random token soups (a seeded generator, so a failure
// repeats) and a native fuzz target whose seeds run with plain `go test`.

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// leak says what in s is not allowed in text that may carry SGR colour, or "".
func leak(s string) string {
	if !utf8.ValidString(s) {
		return "invalid UTF-8"
	}
	rest := sgrSeq.ReplaceAllString(s, "")
	for _, r := range rest {
		switch {
		case r == '\t' || r == '\n':
		case r < 0x20 || r == 0x7f:
			return "control character or a stray ESC"
		case r >= 0x80 && r <= 0x9f:
			return "C1 control"
		case unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029:
			return "format or separator character"
		}
	}
	return ""
}

var hostileTokens = []string{
	"\x1b", "\x1b", "\x1b", "[", "]", "P", "_", "^", "X", "\\", "\x07", "\x9b", "\x9d", "\x90",
	"\xc2\x9b", "\xc2\x9d", "\xc2\x9c", "\xc2\x90", "m", "0", "1", "8", "38;5;", "2;1;2;3", ";", ":", "?", ">",
	"$", "(", "B", "7", "c", "\n", "\r", "\x00", "\x08", "\x7f", " ", "a", "é", "‮", "​", "\xef\xbb\xbf",
	"\xe2", "\xff", "\x1b[", "\x1b]", "\x1b]8;;", "\x1b\\", "\x1bP", "\x1b_", "\x1b^", "\x1bX",
	"\x1b[31m", "\x1b[0m", "\x1b[8m", "\x1b[2K", "\x1b[?25l", "\x1b[1;2m", "\x1b[m", "\t",
}

func soup(r *rand.Rand, maxTokens int) string {
	var b strings.Builder
	for n := r.Intn(maxTokens) + 1; n > 0; n-- {
		b.WriteString(hostileTokens[r.Intn(len(hostileTokens))])
	}
	return b.String()
}

// check asserts everything KeepSGR and Plain promise for one input.
func check(t *testing.T, in string) {
	t.Helper()
	out := String(in, KeepSGR)
	if why := leak(out); why != "" {
		t.Fatalf("KeepSGR(%q) = %q: %s", in, out, why)
	}
	if plain := String(in, Plain); strings.Contains(plain, "\x1b") || leak(plain) != "" {
		t.Fatalf("Plain(%q) = %q holds an escape", in, plain)
	}
	if again := String(out, KeepSGR); again != out {
		t.Fatalf("KeepSGR not idempotent on %q: %q then %q", in, out, again)
	}
	// every cut of the input into two chunks, each sanitized on its own
	// (a Driver batches, a pipe splits anywhere), then joined by a newline
	// or by nothing: no escape forms across the cut.
	for k := 0; k <= len(in); k++ {
		a, b := String(in[:k], KeepSGR), String(in[k:], KeepSGR)
		for _, sep := range []string{"\n", ""} {
			j := a + sep + b
			if why := leak(j); why != "" {
				t.Fatalf("cut %d of %q: %q + %q joined by %q: %s", k, in, a, b, sep, why)
			}
			if again := String(j, KeepSGR); again != j {
				t.Fatalf("cut %d of %q: joined chunks %q change when sanitized again: %q", k, in, j, again)
			}
		}
	}
}

func TestKeepSGRHostileTable(t *testing.T) {
	cases := map[string]string{
		"huge parameter list":           "\x1b[" + strings.Repeat("1;", 300000) + "mx",
		"huge list, no final":           "\x1b[" + strings.Repeat("1;", 300000) + "x",
		"nested CSI":                    "\x1b[31\x1b[32mx",
		"nested CSI 2":                  "\x1b[\x1b[31mx",
		"unterminated CSI":              "ok\x1b[31",
		"unterminated CSI before NL":    "\x1b[31\nrest",
		"CSI with a control inside":     "\x1b[3\x001m",
		"CSI with a BEL inside":         "\x1b[3\x071mx",
		"double ESC":                    "\x1b\x1b[31mx",
		"ESC then newline then SGR":     "\x1b\n[31mx",
		"8-bit CSI":                     "\x9b31mx",
		"8-bit CSI, UTF-8 encoded":      "\u009b31mx",
		"8-bit OSC":                     "\x9d0;pwned\x07x",
		"8-bit DCS":                     "\x90qpwned\x9cx",
		"OSC 8 hyperlink, ST":           "\x1b]8;;http://evil\x1b\\link\x1b]8;;\x1b\\",
		"OSC 8 hyperlink, BEL":          "\x1b]8;;http://evil\x07link\x1b]8;;\x07",
		"OSC title BEL":                 "\x1b]0;pwned\x07x",
		"OSC 52 clipboard":              "\x1b]52;c;ZXZpbA==\x07x",
		"DCS":                           "\x1bPpwned\x1b\\x",
		"APC":                           "\x1b_pwned\x1b\\x",
		"PM":                            "\x1b^pwned\x1b\\x",
		"SOS":                           "\x1bXpwned\x1b\\x",
		"DCS ended by BEL (not an end)": "\x1bPpwned\x07x",
		"lone ESC at the end":           "x\x1b",
		"ESC [ at the end":              "x\x1b[",
		"SGR with a private marker":     "\x1b[?1mx\x1b[>1mx\x1b[=1mx",
		"SGR with an intermediate":      "\x1b[1 mx\x1b[1$mx",
		"SGR final in the wrong case":   "\x1b[1Mx",
		"SGR with a space":              "\x1b[3 1mx",
		"SGR between bidi":              "‮\x1b[31m‬x",
		"SGR plus conceal":              "\x1b[8;31mx\x1b[38;5;8;8mx",
		"CR rewrite over SGR":           "\x1b[31mred\r\x1b[2Kgreen",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if len(in) > 1000 { // the cut loop is quadratic: the big ones get the plain checks
				start := time.Now()
				out := String(in, KeepSGR)
				if why := leak(out); why != "" {
					t.Fatalf("%s", why)
				}
				if d := time.Since(start); d > 2*time.Second {
					t.Errorf("took %v", d)
				}
				return
			}
			check(t, in)
		})
	}
	// text hidden inside a string sequence must not come out as text
	for _, in := range []string{
		"\x1b]0;pwned\x07", "\x1b]8;;pwned\x1b\\", "\x1bPpwned\x1b\\", "\x1b_pwned\x1b\\", "\x1b^pwned\x1b\\", "\x1bXpwned\x1b\\",
		"\x1b]52;c;pwned", "\x1b[31pwned",
	} {
		if out := String("a"+in+"b", KeepSGR); strings.Contains(out, "pwned") {
			t.Errorf("KeepSGR(%q) = %q: the payload became text", in, out)
		}
	}
}

func TestKeepSGRSoup(t *testing.T) {
	r := rand.New(rand.NewSource(112))
	for i := 0; i < 4000; i++ {
		check(t, soup(r, 14))
	}
}

// pathological shapes must stay linear: a repeated opener, a repeated
// unterminated string, a repeated half-sequence.
func TestKeepSGRStaysLinear(t *testing.T) {
	for _, unit := range []string{"\x1b[", "\x1b]", "\x1b[1", "\x1b[1;", "\x1b]0;x\x1b", "\x1bP", "\x1b\x1b", "\x1b[31m\x1b[", "\x9b", "\x1b[1\x07"} {
		in := strings.Repeat(unit, 200000)
		start := time.Now()
		out := String(in, KeepSGR)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%q x200000 took %v", unit, d)
		}
		if why := leak(out); why != "" {
			t.Errorf("%q x200000: %s", unit, why)
		}
	}
}

func FuzzKeepSGR(f *testing.F) {
	for _, tok := range hostileTokens {
		f.Add(tok)
	}
	f.Add("\x1b[31mred\x1b[0m \x1b]8;;http://x\x1b\\l\x1b]8;;\x1b\\ \x1b[1")
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 200 {
			in = in[:200]
		}
		check(t, in)
	})
}
