package runview

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Task #112: whatever reaches the Run page, hostile or not, ParseSGR yields
// text without a single escape or control character (the page draws Text
// verbatim), and its spans stay inside the text. Fed both after the
// sanitizer, as the engine does, and raw, which the engine never does but
// ParseSGR promises to survive.
func TestParseSGRNeverKeepsAnEscape(t *testing.T) {
	toks := []string{"\x1b", "\x1b[", "\x1b]", "\x1bP", "\x1b_", "\x1b^", "\x1b\\", "\x07", "\x9b", "\xc2\x9b", "[", "]", "m", "1", "8", ";", ":", "?",
		"\x1b[31m", "\x1b[0m", "\x1b[38;5;", "\x1b[38;2;1;2;3m", "\x1b[2K", "\x1b[?25l", "a", " ", "\n", "\r", "\x00", "‮", "\xff", "\x1b]8;;http://x\x1b\\"}
	r := rand.New(rand.NewSource(112))
	for i := 0; i < 5000; i++ {
		var b strings.Builder
		for n := r.Intn(14) + 1; n > 0; n-- {
			b.WriteString(toks[r.Intn(len(toks))])
		}
		raw := b.String()
		for name, in := range map[string]string{"raw": raw, "sanitized": sanitize.String(raw, sanitize.KeepSGR)} {
			l := ParseSGR(in)
			for _, c := range l.Text {
				if c == 0x1b || (name == "sanitized" && (c == 0x07 || (c < 0x20 && c != 0x09 && c != 0x0a) || c == 0x7f || (c >= 0x80 && c <= 0x9f))) {
					t.Fatalf("%s %q: Text %q holds %U", name, in, l.Text, c)
				}
			}
			if name == "sanitized" && !utf8.ValidString(l.Text) {
				t.Fatalf("sanitized %q: Text %q is not UTF-8", in, l.Text)
			}
			end := 0
			for _, sp := range l.Spans {
				if sp.Start < end || sp.End < sp.Start || sp.End > len(l.Text) {
					t.Fatalf("%s %q: spans %+v outside Text %q", name, in, l.Spans, l.Text)
				}
				end = sp.End
			}
		}
	}
}
