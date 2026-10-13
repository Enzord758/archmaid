package helperclient

import (
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestLineColourNoColorValues (QA b8 tail): the helper's lines follow the
// same NO_COLOR rule as a user step's (sanitize.ColourMode): empty keeps the
// colour, any non-empty value, "0" included, drops it, and each step's
// decoder reads it when the step starts.
func TestLineColourNoColorValues(t *testing.T) {
	const text = ":: \x1b[1;34mnote\x1b[0m"
	line := `{"v":1,"t":"line","stream":"out","text":"` + `:: \u001b[1;34mnote\u001b[0m"}`
	for val, plain := range map[string]bool{"": false, "1": true, "0": true, "false": true, " ": true} {
		t.Setenv("NO_COLOR", val)
		d := newDecoder(steps.Step{Verb: steps.VerbPaccache})
		d.feed([]byte(`{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`))
		got, _ := d.feed([]byte(line))
		want := sanitize.String(text, sanitize.ColourMode())
		if (want != text) != plain || !slices.Equal(got, []string{want}) {
			t.Errorf("NO_COLOR=%q: lines %q, want %q (plain %v)", val, got, want, plain)
		}
	}
}

// TestLineColourReadPerStep documents what the decoder does with a change
// mid-run: it reads NO_COLOR when its step starts (newDecoder), so a step
// already decoding keeps its mode.
func TestLineColourReadPerStep(t *testing.T) {
	const line = `{"v":1,"t":"line","stream":"out","text":"\u001b[1mx\u001b[0m"}`
	t.Setenv("NO_COLOR", "")
	d := newDecoder(steps.Step{Verb: steps.VerbPaccache})
	d.feed([]byte(`{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`))
	t.Setenv("NO_COLOR", "1")
	if got, _ := d.feed([]byte(line)); !slices.Equal(got, []string{"\x1b[1mx\x1b[0m"}) {
		t.Errorf("a running decoder changed mode mid-step: %q", got)
	}
}
