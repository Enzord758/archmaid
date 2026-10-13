package sanitize

import (
	"os"
	"testing"
)

// TestColourModeFollowsNoColorSpec: NO_COLOR set to any non-empty value
// (no-color.org: "0", "false" and a lone space included) means Plain; unset
// or empty keeps SGR.
func TestColourModeFollowsNoColorSpec(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want Mode
	}{
		{"", KeepSGR},
		{"1", Plain},
		{"0", Plain},
		{"false", Plain},
		{"no", Plain},
		{" ", Plain},
		{"\x1b[1m", Plain},
	} {
		t.Setenv("NO_COLOR", tc.val)
		if got := ColourMode(); got != tc.want {
			t.Errorf("NO_COLOR=%q: ColourMode = %v, want %v", tc.val, got, tc.want)
		}
	}
	t.Setenv("NO_COLOR", "x")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	if got := ColourMode(); got != KeepSGR {
		t.Errorf("NO_COLOR unset: ColourMode = %v, want KeepSGR", got)
	}
}
