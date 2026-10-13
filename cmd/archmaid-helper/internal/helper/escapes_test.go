package helper

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	clientsan "github.com/Enzord758/archmaid/internal/sanitize"
)

// Task #112: the security property of SanitizeLine, and of SanitizeLine
// followed by the client's clientsan.KeepSGR (what a line event goes through
// on its way to the GUI and the TUI). The only escape that may leave the
// helper in a line is a well-formed SGR; no control, C1 or format character
// may either. Inputs: a table of hostile shapes, seeded random token soups
// and a fuzz target (seeds run with plain `go test`).

var sgrOnly = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func lineLeak(s string) string {
	if !utf8.ValidString(s) {
		return "invalid UTF-8"
	}
	for _, r := range sgrOnly.ReplaceAllString(s, "") {
		switch {
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

var hostileLineTokens = []string{
	"\x1b", "\x1b", "\x1b", "[", "]", "P", "_", "^", "X", "\\", "\x07", "\x9b", "\x9d", "\x90",
	"\xc2\x9b", "\xc2\x9d", "\xc2\x9c", "m", "0", "1", "8", "38;5;", ";", ":", "?", ">", "$", "(", "B", "7",
	"\n", "\r", "\x00", "\x7f", " ", "a", "é", "‮", "​", "\xe2", "\xff", "\x1b[", "\x1b]", "\x1b]8;;",
	"\x1b\\", "\x1bP", "\x1b_", "\x1b^", "\x1bX", "\x1b[31m", "\x1b[0m", "\x1b[8m", "\x1b[2K", "\x1b[?25l", "\x1b[m", "\t",
}

func lineSoup(r *rand.Rand, maxTokens int) string {
	var b strings.Builder
	for n := r.Intn(maxTokens) + 1; n > 0; n-- {
		b.WriteString(hostileLineTokens[r.Intn(len(hostileLineTokens))])
	}
	return b.String()
}

// checkLine asserts the promises for one raw child line.
func checkLine(t *testing.T, in string) {
	t.Helper()
	out := SanitizeLine(in)
	if why := lineLeak(out); why != "" {
		t.Fatalf("SanitizeLine(%q) = %q: %s", in, out, why)
	}
	if again := SanitizeLine(out); again != out {
		t.Fatalf("SanitizeLine not idempotent on %q: %q then %q", in, out, again)
	}
	if plain := Sanitize(in); strings.Contains(plain, "\x1b") || lineLeak(plain) != "" {
		t.Fatalf("Sanitize(%q) = %q holds an escape", in, plain)
	}
	// the client filters it once more: it may only lose the conceal
	if got := clientsan.String(out, clientsan.KeepSGR); lineLeak(got) != "" {
		t.Fatalf("client KeepSGR of %q = %q: %s", out, got, lineLeak(got))
	}
	// the child's output cut anywhere into two lines (the helper sends a
	// line event per line, the Driver batches them): no escape forms from
	// the halves, neither in the helper's two events joined by a newline
	// nor after the client has sanitized the joined batch again.
	for k := 0; k <= len(in); k++ {
		a, b := SanitizeLine(in[:k]), SanitizeLine(in[k:])
		batch := a + "\n" + b
		if why := lineLeak(a + b); why != "" {
			t.Fatalf("cut %d of %q: %q + %q: %s", k, in, a, b, why)
		}
		if got := clientsan.String(batch, clientsan.KeepSGR); lineLeak(strings.ReplaceAll(got, "\n", "")) != "" {
			t.Fatalf("cut %d of %q: client output %q: %s", k, in, got, lineLeak(got))
		}
		if strings.Count(batch, "\x1b") != len(sgrOnly.FindAllString(batch, -1)) {
			t.Fatalf("cut %d of %q: %q holds an ESC that starts no whole SGR", k, in, batch)
		}
	}
}

func TestSanitizeLineHostile(t *testing.T) {
	cases := map[string]string{
		"huge parameter list":          "\x1b[" + strings.Repeat("1;", 300000) + "mx",
		"huge list, no final":          "\x1b[" + strings.Repeat("1;", 300000) + "x",
		"nested CSI":                   "\x1b[31\x1b[32mx",
		"nested CSI 2":                 "\x1b[\x1b[31mx",
		"unterminated CSI":             "ok\x1b[31",
		"CSI with a control inside":    "\x1b[3\x001m",
		"CSI with a BEL inside":        "\x1b[3\x071mx",
		"double ESC":                   "\x1b\x1b[31mx",
		"ESC ESC ESC [":                "\x1b\x1b\x1b[31mx",
		"8-bit CSI":                    "\x9b31mx",
		"8-bit CSI, UTF-8":             "\u009b31mx",
		"8-bit OSC":                    "\x9d0;pwned\x07x",
		"8-bit DCS":                    "\x90qpwned\x9cx",
		"OSC 8 hyperlink, ST":          "\x1b]8;;http://evil\x1b\\link\x1b]8;;\x1b\\",
		"OSC 8 hyperlink, BEL":         "\x1b]8;;http://evil\x07link\x1b]8;;\x07",
		"OSC title BEL":                "\x1b]0;pwned\x07x",
		"DCS":                          "\x1bPpwned\x1b\\x",
		"APC":                          "\x1b_pwned\x1b\\x",
		"PM":                           "\x1b^pwned\x1b\\x",
		"SOS":                          "\x1bXpwned\x1b\\x",
		"lone ESC at the end":          "x\x1b",
		"ESC [ at the end":             "x\x1b[",
		"private marker":               "\x1b[?1mx\x1b[>1mx\x1b[=1mx",
		"intermediate":                 "\x1b[1 mx\x1b[1$mx",
		"final in the wrong case":      "\x1b[1Mx",
		"SGR between bidi":             "‮\x1b[31m‬x",
		"SGR splitting a UTF-8 rune":   "\xe2\x1b[31m\x82\xac",
		"ESC before a multibyte rune":  "\x1b​x",
		"ESC before a zero-width char": "\x1b[31​mx",
		"ESC then a tab":               "\x1b\t[31mx",
		"SGR with a tab inside":        "\x1b[3\t1mx",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if len(in) > 1000 {
				start := time.Now()
				out := SanitizeLine(in)
				if why := lineLeak(out); why != "" {
					t.Fatalf("%s", why)
				}
				if got := clientsan.String(out, clientsan.KeepSGR); lineLeak(got) != "" {
					t.Fatalf("client: %s", lineLeak(got))
				}
				if d := time.Since(start); d > 2*time.Second {
					t.Errorf("took %v", d)
				}
				return
			}
			checkLine(t, in)
		})
	}
	for _, in := range []string{
		"\x1b]0;pwned\x07", "\x1b]8;;pwned\x1b\\", "\x1bPpwned\x1b\\", "\x1b_pwned\x1b\\", "\x1b^pwned\x1b\\", "\x1bXpwned\x1b\\",
	} {
		if out := SanitizeLine("a" + in + "b"); strings.Contains(out, "pwned") {
			t.Errorf("SanitizeLine(%q) = %q: the payload became text", in, out)
		}
	}
}

func TestSanitizeLineSoup(t *testing.T) {
	r := rand.New(rand.NewSource(112))
	for i := 0; i < 3000; i++ {
		checkLine(t, lineSoup(r, 14))
	}
}

func TestSanitizeLineStaysLinear(t *testing.T) {
	for _, unit := range []string{"\x1b[", "\x1b]", "\x1b[1", "\x1b[1;", "\x1b]0;x\x1b", "\x1bP", "\x1b\x1b", "\x1b[31m\x1b[", "\x9b", "\x1b[1\x07"} {
		in := strings.Repeat(unit, 200000)
		start := time.Now()
		out := SanitizeLine(in)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%q x200000 took %v", unit, d)
		}
		if why := lineLeak(out); why != "" {
			t.Errorf("%q x200000: %s", unit, why)
		}
	}
}

func FuzzSanitizeLine(f *testing.F) {
	for _, tok := range hostileLineTokens {
		f.Add(tok)
	}
	f.Add("\x1b[31mred\x1b[0m \x1b]8;;http://x\x1b\\l\x1b]8;;\x1b\\ \x1b[1")
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 200 {
			in = in[:200]
		}
		checkLine(t, in)
	})
}
