package helperclient

import (
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestLineColour: a line event's SGR colour (the helper keeps it since
// task #111: pacman's --color always) reaches the engine, filtered by
// sanitize.KeepSGR (conceal and every other escape dropped); under
// NO_COLOR the client drops the colour too, since the helper's switch is
// a constant the client can't turn off.
func TestLineColour(t *testing.T) {
	line := `{"v":1,"t":"line","stream":"out","text":"\u001b[1;34m::\u001b[0m \u001b[8mhidden\u001b[0m \u001b]0;t\u0007done"}`
	for noColour, want := range map[string]string{
		"":  "\x1b[1;34m::\x1b[0m hidden\x1b[0m done",
		"1": ":: hidden done",
	} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			t.Setenv("NO_COLOR", noColour)
			d := newDecoder(steps.Step{Verb: steps.VerbPaccache})
			d.feed([]byte(`{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`))
			got, _ := d.feed([]byte(line))
			if !slices.Equal(got, []string{want}) {
				t.Errorf("lines %q, want %q", got, []string{want})
			}
		})
	}
}
