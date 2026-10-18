package realdriver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// helperColourShims puts pkexec, topgrade and yay first on PATH. pkexec
// checks its first two arguments, then speaks the root helper's
// full-cleanup stream for the parts its arguments name, in the helper's
// order: each part a progress, one line, a done. The orphan removal's and
// the pacman -Sc part's lines are what the helper relays from pacman with
// --color always (task #111): an SGR-coloured "::", plus a title (OSC)
// that the helper would already have dropped, here to prove the client
// drops it too.
func helperColourShims(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
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
	printf '{"v":1,"t":"progress","step":%d,"of":%d,"label":"doing %s","part":"%s"}\n' $i $n $x $x
	if [ $x = remove-orphans ] || [ $x = clean-uninstalled ]; then
		printf '%s\n' '{"v":1,"t":"line","stream":"out","text":"\u001b]0;pwned\u0007\u001b[1;34m::\u001b[0m\u001b[1m Processing package changes...\u001b[0m"}'
	else
		printf '{"v":1,"t":"line","stream":"out","text":"plain %s"}\n' $x
	fi
	printf '{"v":1,"t":"done","step":%d,"of":%d,"part":"%s","ok":true,"code":0}\n' $i $n $x
done
printf '%s\n' '{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}'
`
	for name, body := range map[string]string{
		"pkexec":   pkexec,
		"topgrade": "#!/bin/sh\necho updated\n",
		"yay":      "#!/bin/sh\necho yay cleaned\n",
	} {
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

// TestRunPageHelperColour: a whole Full Cleanup on the GUI's real Driver
// (polkit, one full-cleanup helper call through a pkexec shim), its Events
// applied to the Run page's model as the window's pump does. The root
// helper's coloured line (pacman -Rns's "::" in bold blue) reaches the
// orphan removal's row with its colour as spans and Pango attributes, the
// title escape gone; no file the run wrote (the orphans log, which holds
// the line's plain text, last-run.json) holds an escape. Under NO_COLOR
// the line has no span.
func TestRunPageHelperColour(t *testing.T) {
	for _, noColour := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			helperColourShims(t)
			t.Setenv("NO_COLOR", noColour)
			cfg := fakes(t, Config{NoAskpass: true, TTYAgent: "-"})
			w := Start(cfg)
			defer w.Close()
			d := w.New(w.Options())
			defer d.Close()

			m := runview.NewModel()
			d.Send(engine.Start{Action: actions.IDFullCleanup})
			timeout := time.After(30 * time.Second)
		loop:
			for {
				select {
				case ev, ok := <-d.Events():
					if !ok {
						t.Fatal("Events closed early")
					}
					m.Apply([]engine.Event{ev})
					switch ev := ev.(type) {
					case engine.NeedsConfirm:
						d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
					case engine.Finished:
						if ev.Outcome != engine.OutcomeCompleted || ev.Errs != 0 {
							t.Fatalf("outcome %v, %d errors; Sc row %q", ev.Outcome, ev.Errs, m.StepText(steps.IDPkgCacheUninstalled))
						}
						break loop
					}
				case <-timeout:
					t.Fatal("no Finished")
				}
			}

			const text = ":: Processing package changes..."
			var l runview.Line
			found := false
			for i := 0; ; i++ {
				li, ok := m.StepLine(steps.IDOrphansRemove, i)
				if !ok {
					break
				}
				if li.Text == text {
					l, found = li, true
				}
			}
			if !found {
				t.Fatalf("no %q on the orphan removal row: %q", text, m.StepText(steps.IDOrphansRemove))
			}
			attrs := l.Attrs(&runview.FallbackPalette)
			if noColour != "" {
				if len(l.Spans) != 0 || attrs != "" {
					t.Errorf("under NO_COLOR the line has spans %+v (%q), want none", l.Spans, attrs)
				}
			} else {
				blue := runview.FallbackPalette.Color(runview.Color{Kind: runview.ColorIndex, Index: 4}).Hex()
				if len(l.Spans) < 2 || !strings.Contains(attrs, "0 2 foreground "+blue) || !strings.Contains(attrs, "0 2 weight bold") ||
					!strings.Contains(attrs, "2 32 weight bold") {
					t.Errorf("spans %+v, Pango attributes %q: want :: bold in %s, the rest bold", l.Spans, attrs, blue)
				}
			}

			var logs int
			err := filepath.WalkDir(cfg.StateDir, func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				if strings.ContainsAny(string(b), "\x1b\x07") || strings.Contains(string(b), `\u001b`) {
					t.Errorf("%s holds an escape: %q", p, b)
				}
				if strings.Contains(string(b), text) {
					logs++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if logs == 0 {
				t.Errorf("no log under %s holds %q", cfg.StateDir, text)
			}
		})
	}
}
