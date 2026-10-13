package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
)

func init() {
	covScenarios["cov-keptnote"] = covKeptNote
	covOptions["cov-keptnote"] = func(o *Options, _ *report) { o.KeepsPassword = true }
}

func TestGUICovKeptNote(t *testing.T) { covChild(t, "cov-keptnote") }

// covKeptNote (#144b): on a Driver that keeps the password, the dialog
// claims it only when the prompt says it will be kept. A prompt that says
// it won't (the PATH guard tripped, or the kept password was refused) shows
// the off line with the reason instead, and one that says nothing (an older
// Driver, anything in doubt) claims nothing.
func covKeptNote(c *covEnv) {
	w := c.w
	c.quiet()
	shows := func(id engine.PromptID, want, notWant string) {
		d := w.dialogs[id]
		if d == nil {
			c.fail("prompt %d has no dialog", id)
			return
		}
		labels := labelsUnder(&d.Widget)
		c.ok(slices.Contains(labels, want), "prompt %d: %q not shown among %q", id, want, labels)
		c.ok(!slices.Contains(labels, notWant), "prompt %d shows %q", id, notWant)
		c.press(d, "cancel")
	}
	why := "the PATH check found something that could stand in for sudo"
	c.seq(150,
		func() { c.feed(engine.NeedsPassword{ID: 1, Kind: engine.PasswordUpFront, Keeps: true}) },
		func() { shows(1, KeptNote, KeptOffNote(why)) },
		func() { c.feed(engine.NeedsPassword{ID: 2, Kind: engine.PasswordAskpass, Keeps: false, KeepOff: why}) },
		func() {
			shows(2, KeptOffNote(why), KeptNote)
			c.ok(strings.Contains(KeptOffNote(why), why), "the off line lacks the reason")
		},
		func() { c.feed(engine.NeedsPassword{ID: 3, Kind: engine.PasswordAskpass}) },
		func() { shows(3, KeptOffNote(""), KeptNote) },
	)
}

// KeptOffNote is one line with the reason, never the kept note's claim.
func TestKeptOffNote(t *testing.T) {
	got := KeptOffNote("PATH\nhas a sudo")
	if strings.Contains(got, "\n") || !strings.Contains(got, "PATH has a sudo") || strings.Contains(got, "keeps this password") {
		t.Errorf("off note %q", got)
	}
	if !strings.Contains(KeptOffNote(""), "refused or has run out") {
		t.Errorf("default off note %q", KeptOffNote(""))
	}
	if keptNoteFor(engine.NeedsPassword{}) == KeptNote {
		t.Error("a prompt that says nothing claims the password is kept")
	}
	if keptNoteFor(engine.NeedsPassword{Keeps: true}) != KeptNote {
		t.Error("a prompt that says it is kept doesn't show the kept note")
	}
}
