package realdriver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// colourShim puts a topgrade first on PATH that colours its line only
// when CLICOLOR_FORCE asks, as topgrade's console crate does into a pipe.
func colourShim(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nif [ \"${CLICOLOR_FORCE:-0}\" != 0 ]; then\n" +
		"\tprintf 'System update: \\033[1m\\033[32mOK\\033[0m\\n'\nelse\n\techo 'System update: OK'\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestRunPageColour: Run topgrade on the GUI's real Driver, its Events
// applied to the Run page's model (runview.Model, as the window's pump
// does). The topgrade step's child gets CLICOLOR_FORCE (actions/colour.go),
// so its line reaches the model with its SGR and the view's line has a
// bold green span, drawn as Pango foreground and weight attributes; under
// NO_COLOR the same line has no span at all.
func TestRunPageColour(t *testing.T) {
	for _, noColour := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			colourShim(t)
			t.Setenv("NO_COLOR", noColour)
			w := Start(fakes(t, Config{NoAskpass: true}))
			defer w.Close()
			d := w.New(w.Options())
			defer d.Close()

			m := runview.NewModel()
			d.Send(engine.Start{Action: actions.IDTopgrade})
			timeout := time.After(20 * time.Second)
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
						if ev.Outcome != engine.OutcomeCompleted {
							t.Fatalf("outcome %v", ev.Outcome)
						}
						break loop
					}
				case <-timeout:
					t.Fatal("no Finished")
				}
			}

			l, ok := m.StepLine(steps.IDTopgrade, 0)
			if !ok || l.Text != "System update: OK" {
				t.Fatalf("topgrade's line: %+v (%v); its text %q", l, ok, m.StepText(steps.IDTopgrade))
			}
			attrs := l.Attrs(&runview.FallbackPalette)
			if noColour != "" {
				if len(l.Spans) != 0 || attrs != "" {
					t.Errorf("under NO_COLOR the line has spans %+v (%q), want none", l.Spans, attrs)
				}
				return
			}
			want := runview.Span{Start: len("System update: "), End: len(l.Text),
				Style: runview.Style{FG: runview.Color{Kind: runview.ColorIndex, Index: 2}, Bold: true}}
			if len(l.Spans) != 1 || l.Spans[0] != want {
				t.Errorf("spans %+v, want one bold green span over OK: %+v", l.Spans, want)
			}
			green := runview.FallbackPalette.Color(want.Style.FG).Hex()
			if !strings.Contains(attrs, "15 17 foreground "+green) || !strings.Contains(attrs, "15 17 weight bold") {
				t.Errorf("Pango attributes %q, want OK (15–17) in %s, bold", attrs, green)
			}
		})
	}
}
