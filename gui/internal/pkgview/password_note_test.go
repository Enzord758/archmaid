package pkgview

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

func askNote(t *testing.T, s *Sheet) string {
	t.Helper()
	for _, n := range s.View().Notes {
		if n.Tag == "asks" {
			return n.Text
		}
	}
	t.Fatal("no password note")
	return ""
}

// TestSheetPasswordNote (B7, B8): the sheet says what really happens: one
// password when the GUI keeps it, up to two when it does not, none in a dry
// run.
func TestSheetPasswordNote(t *testing.T) {
	for _, c := range []struct {
		name      string
		dry, kept bool
		aur       bool
		want      string
		not       string
	}{
		{"aur kept", false, true, true, "asked for your password once", "twice"},
		{"aur not kept", false, false, true, "up to twice", "once"},
		{"aur dry", true, true, true, "Nothing asks for a password", "prompt"},
		{"repo dry", true, false, false, "Nothing asks for a password", "1 password prompt"},
		{"repo", false, true, false, "1 password prompt", "Nothing asks"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := startSheet(t, RequestFor(ModeInstall, []string{"paru"}, false))
			s.Dry, s.Kept = c.dry, c.kept
			plan := aurPlan(true)
			if !c.aur {
				plan = steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"paru"}, Rows: plan.Rows}
			}
			s.Apply(Preview{Prompt: 2, Plan: plan})
			got := askNote(t, s)
			if !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
				t.Errorf("note %q: want %q, not %q", got, c.want, c.not)
			}
		})
	}
}

// TestControllerFeedsTheSheetItsPasswordFacts: Dry and Kept reach the sheet
// when the preview arrives (the Driver is made after Begin).
func TestControllerFeedsTheSheetItsPasswordFacts(t *testing.T) {
	r := newRig()
	dry, kept := false, true
	c := NewController(Config{Search: r.s, Catalog: r.cat, Backend: &fakeBackend{}, Clock: r.clock,
		Dry: func() bool { return dry }, Kept: func() bool { return kept }})
	c.begin(RequestFor(ModeInstall, []string{"paru"}, false))
	dry = true
	c.Handle(Preview{Prompt: 1, Plan: aurPlan(true)})
	if !c.Sheet.Dry || !c.Sheet.Kept {
		t.Fatalf("Dry %v Kept %v", c.Sheet.Dry, c.Sheet.Kept)
	}
}
