package realdriver

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
)

// hostileShims puts a topgrade and a pkexec first on PATH whose output is
// everything a hostile AUR build script (topgrade) or a hostile package
// name in a pacman message (the helper's line events, which the helper
// itself would already have cleaned: here the client alone has to) could
// print: colour, titles, hyperlinks, an 8-bit CSI, erases, private modes,
// DCS/APC/PM strings, a cut CSI and a lone ESC at a line's end.
func hostileShims(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	topgrade := `#!/bin/sh
printf '\033[1;32mOK\033[0m colour\n'
printf 'a\033]0;pwned-title\007b\n'
printf 'c\033]8;;http://evil.example/\033\\link\033]8;;\033\\\n'
printf 'd\233 31mraw-c1\n'
printf 'g\033[2J\033[H\033[?25l\033[6n\033[1Ahide\n'
printf 'h\033Pq#0;2;0;0;0pwned-dcs\033\\i\033_pwned-apc\033\\j\033^pwned-pm\033\\k\n'
printf 'l\033[31\nm\033\nn\033[3\n1mfoo\n'
`
	pkexec := `#!/bin/sh
[ "$1" = ` + helperclient.DisableInternalAgent + ` ] && [ "$2" = ` + helperclient.HelperPath + ` ] || exit 126
[ "$3" = full-cleanup ] || exit 127
p=
case " $* " in *" --orphans "*) p=remove-orphans ;; esac
p="$p paccache"
case " $* " in *" --uninstalled "*) p="$p clean-uninstalled" ;; esac
p="$p clean-tmp vacuum-journal check-files"
n=0
for x in $p; do n=$((n+1)); done
printf '%s\n' '{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}'
i=0
for x in $p; do
	i=$((i+1))
	printf '{"v":1,"t":"progress","step":%d,"of":%d,"label":"doing %s\\u001b[2J\\u001b]0;pwned-label\\u0007","part":"%s"}\n' $i $n $x $x
	printf '%s\n' '{"v":1,"t":"line","stream":"out","text":"\u001b[1;34m::\u001b[0m ok \u001b]0;pwned-title\u0007\u001b]8;;http://evil/\u001b\\link\u001b[2J\u001b[?25l\u009b31m\u001bPpwned-dcs\u001b\\\u001b[31"}'
	printf '%s\n' '{"v":1,"t":"line","stream":"err","text":"x\u001b"}'
	printf '{"v":1,"t":"done","step":%d,"of":%d,"part":"%s","ok":true,"code":0}\n' $i $n $x
done
printf '%s\n' '{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[],"error":"\u001b[31mfine\u001b]0;pwned-err\u0007"}'
`
	for name, body := range map[string]string{"pkexec": pkexec, "topgrade": topgrade, "yay": "#!/bin/sh\necho yay cleaned\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stand := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(stand, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	helpertest.UseHelperFile(t, stand)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var sgrOnly = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func escapeIn(text string) string {
	rest := sgrOnly.ReplaceAllString(text, "")
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

// TestHostileOutputNeverReachesALogOrAnEvent: Full Cleanup and topgrade on
// the GUI's real Driver, with the output above. Every output line of every
// event keeps well-formed SGR only (the colour itself survives), and no
// file under the scratch state dir (the logs, last-run.json) holds an ESC,
// a BEL, a C1 control, their JSON-escaped forms (\u001b, \u009b, \u0007) or
// the text hidden inside a string sequence.
func TestHostileOutputNeverReachesALogOrAnEvent(t *testing.T) {
	for _, action := range []string{actions.IDTopgrade, actions.IDFullCleanup} {
		t.Run(action, func(t *testing.T) {
			hostileShims(t)
			t.Setenv("NO_COLOR", "")
			cfg := fakes(t, Config{NoAskpass: true, TTYAgent: "-"})
			w := Start(cfg)
			defer w.Close()
			d := w.New(w.Options())
			defer d.Close()

			d.Send(engine.Start{Action: action})
			var lines []string
			timeout := time.After(30 * time.Second)
		loop:
			for {
				select {
				case ev, ok := <-d.Events():
					if !ok {
						t.Fatal("Events closed early")
					}
					switch ev := ev.(type) {
					case engine.NeedsConfirm:
						d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
					case engine.OutputChunk:
						lines = append(lines, ev.Lines...)
					case engine.Finished:
						if ev.Outcome != engine.OutcomeCompleted {
							t.Fatalf("outcome %v", ev.Outcome)
						}
						break loop
					}
				case <-timeout:
					t.Fatal("no Finished")
				}
			}
			if len(lines) == 0 {
				t.Fatal("no output at all")
			}
			coloured := false
			for _, l := range lines {
				if why := escapeIn(l); why != "" {
					t.Errorf("output line %q holds %s", l, why)
				}
				coloured = coloured || strings.Contains(l, "\x1b[1;")
			}
			if !coloured {
				t.Errorf("the colour is gone: %q", lines)
			}

			files := 0
			err := filepath.WalkDir(cfg.StateDir, func(p string, e fs.DirEntry, err error) error {
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
			if files < 2 {
				t.Errorf("only %d files under %s: nothing to check", files, cfg.StateDir)
			}
		})
	}
}
