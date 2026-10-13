package realdriver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestNoColourStripsUserStepSGR (Dev 5's VM round b8): a topgrade whose
// output has bold SGR whatever its environment says (yay's ":: Searching
// AUR for updates..." inside it) reaches the Run page's model with its bold
// span, and under NO_COLOR with no span at all.
func TestNoColourStripsUserStepSGR(t *testing.T) {
	for _, noColour := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			dir := t.TempDir()
			body := "#!/bin/sh\nprintf ':: \\033[1mSearching AUR for updates...\\033[0m\\n'\n"
			if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
						break loop
					}
				case <-timeout:
					t.Fatal("no Finished")
				}
			}
			l, ok := m.StepLine(steps.IDTopgrade, 0)
			if !ok || l.Text != ":: Searching AUR for updates..." {
				t.Fatalf("topgrade's line: %+v (%v); its text %q", l, ok, m.StepText(steps.IDTopgrade))
			}
			attrs := l.Attrs(&runview.FallbackPalette)
			if noColour != "" {
				if len(l.Spans) != 0 || attrs != "" {
					t.Errorf("under NO_COLOR the line has spans %+v (%q), want none", l.Spans, attrs)
				}
				return
			}
			if len(l.Spans) != 1 || !l.Spans[0].Style.Bold {
				t.Errorf("spans %+v, want one bold span", l.Spans)
			}
		})
	}
}
