package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// hostileTopgrade is a topgrade whose output is everything a malicious AUR
// build script could print: colour (which is meant to get through), a
// title (OSC, BEL and ST ended), a hyperlink, an 8-bit CSI and OSC, cursor
// and erase sequences, a private mode, a device status request, DCS, APC and
// PM strings, a CSI cut by the line's end, a lone ESC at a line's end and a
// CSI split over two lines.
var hostileTopgrade = Shims{
	"sudo": sudoRules(true),
	"topgrade": {{Prefix: "-y", Script: `printf '\033[1;32mOK\033[0m colour\n'
printf 'a\033]0;pwned-title\007b\n'
printf 'c\033]8;;http://evil.example/\033\\link\033]8;;\033\\\n'
printf 'd\233 31mraw-c1\n'
printf 'e\235 0;c1-osc-text\007f\n'
printf 'g\033[2J\033[H\033[?25l\033[6n\033[1Ahide\n'
printf 'h\033Pq#0;2;0;0;0pwned-dcs\033\\i\033_pwned-apc\033\\j\033^pwned-pm\033\\k\n'
printf 'l\033[31\nm\033\nn\033[3\n1mfoo\n'
printf 'o\033[38;5;196mred\033[0m\r\nprogress 50%%\rprogress 100%%\n'`}},
}

var onlySGRSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// escapeIn says what in text is more than well-formed SGR: any other ESC,
// BEL, C1 control (as a byte or as U+0080..U+009F) or the payload of a
// string sequence.
func escapeIn(text string) string {
	rest := onlySGRSeq.ReplaceAllString(text, "")
	switch {
	case strings.ContainsAny(rest, "\x1b\x07"):
		return "an ESC or BEL"
	case strings.ContainsFunc(rest, func(r rune) bool { return r >= 0x80 && r <= 0x9f }):
		return "a C1 control"
	case !utf8.ValidString(rest):
		return "invalid UTF-8"
	case strings.Contains(rest, "pwned"):
		return "the payload of a string sequence as text"
	}
	return ""
}

// TestHostileOutputNeverReachesALogOrTheRecord: Run topgrade on the TUI
// with the output above. The pane keeps only well-formed SGR; no file under
// the scratch state dir (topgrade.log, the other logs, last-run.json) holds
// an ESC, a BEL, a C1 byte, the JSON-escaped forms of those (\u001b,
// \u009b, \u0007) or the text hidden inside a string sequence.
func TestHostileOutputNeverReachesALogOrTheRecord(t *testing.T) {
	for _, real := range []bool{false, true} {
		name := "tui"
		if real {
			name = "real-driver"
		}
		t.Run(name, func(t *testing.T) {
			transcriptEnv(t, false)
			t.Setenv("NO_COLOR", "")
			shimLog := installShims(t, hostileTopgrade)
			gate := Answer{"confirm", "Run a system update with topgrade", "yes"}
			var pane string
			if real {
				var tg *realDriverTarget
				r := record(t, func(t *testing.T, shimLog string) tt.Target {
					tg = newRealDriverTarget(t, shimLog)
					return tg
				}, shimLog, "Run topgrade", gate)
				tg.finish(t)
				for _, l := range strings.Split(r.Transcript(), "\n") {
					if s, ok := strings.CutPrefix(l, "out   2 "); ok {
						pane += s + "\n"
					}
				}
			} else {
				pane = run(t, shimLog, "Run topgrade", gate).m.tail.String()
			}
			if !strings.Contains(pane, "\x1b[1;32mOK\x1b[0m colour") {
				t.Errorf("the colour of the first line is gone: %q", pane)
			}
			if why := escapeIn(pane); why != "" {
				t.Errorf("pane holds %s: %q", why, pane)
			}
			if !strings.Contains(pane, "progress 100%") || strings.Contains(pane, "progress 50%") {
				t.Errorf("the carriage return did not rewrite the progress line: %q", pane)
			}

			files := 0
			err := filepath.WalkDir(logs().Root(), func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				files++
				s := string(b)
				if strings.ContainsAny(s, "\x1b\x07\r") || strings.ContainsFunc(s, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) ||
					strings.Contains(s, `\u001b`) || strings.Contains(s, `\u009b`) || strings.Contains(s, `\u0007`) ||
					strings.Contains(s, `\x1b`) || strings.Contains(s, "pwned") {
					t.Errorf("%s holds an escape or a hidden payload: %q", p, s)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if files < 2 { // topgrade.log and last-run.json at least
				t.Errorf("only %d files under %s: the walk saw nothing to check", files, logs().Root())
			}
		})
	}
}
