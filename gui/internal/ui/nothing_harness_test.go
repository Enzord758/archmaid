package ui

// Task #207, finding 1: Clear caches with both caches empty is a popup over
// the page it started from ("No cache to clear"), not the Run page with a
// discovery notice.

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine/stub"
)

func init() { covScenarios["cov-nocache"] = covNoCache }

func TestGUICovNoCache(t *testing.T) { covChild(t, "cov-nocache") }

// covNoCache: Clear caches that finds nothing to clear (the stub's
// clearcaches-none: DiscoveryResult Nothing, Notice, Finished Notice) leaves
// the window on the page it started from, says "No cache to clear" in an
// AdwAlertDialog, and leaves the Run page as it was (no discovery step, no
// finished run).
func covNoCache(c *covEnv) {
	w := c.w
	c.quiet()
	c.seq(900,
		func() {
			w.pickPage("overview")
			c.ok(w.stack.VisibleChildName() == "overview", "not on the Overview to start with")
			w.startAction(stub.IDClearCachesNone)
		},
		func() {
			c.ok(w.stack.VisibleChildName() == "overview", "page %q after Clear caches found nothing, want overview", w.stack.VisibleChildName())
			d := w.nothingDialog
			c.ok(d != nil, "no \"No cache to clear\" dialog")
			if d != nil {
				c.ok(d.alert != nil && d.alert.Heading() == "No cache to clear", "dialog heading %q", d.alert.Heading())
				c.ok(strings.Contains(d.alert.Body(), "both empty"), "dialog body %q", d.alert.Body())
			}
			c.ok(w.model.Discovery == nil && len(w.model.Rows) == 0 && w.model.Finished == nil,
				"the Run page shows a run: discovery %v rows %d finished %v", w.model.Discovery != nil, len(w.model.Rows), w.model.Finished != nil)
			c.ok(!w.busy, "still busy")
			c.ok(strings.Contains(w.status.Text(), "No cache to clear"), "status %q", w.status.Text())
			shoot(w, "cov-nocache", "dialog")
			if d != nil {
				c.press(d, "ok")
			}
		},
		func() {
			c.ok(w.nothingDialog == nil, "the dialog stayed after OK")
			c.ok(w.stack.VisibleChildName() == "overview", "page %q after OK", w.stack.VisibleChildName())
		},
	)
}
