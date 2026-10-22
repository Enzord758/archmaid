package ui

// Task #207, finding 1: Clear caches with both caches empty is a popup over
// the page it started from ("No cache to clear"), not the Run page with a
// discovery notice.

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
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
	var seen []string
	w.stack.NotifyProperty("visible-child-name", func() { seen = append(seen, w.stack.VisibleChildName()) })
	c.seq(900,
		func() {
			w.pickPage("overview")
			seen = nil
			c.ok(w.stack.VisibleChildName() == "overview", "not on the Overview to start with")
			w.startAction(stub.IDClearCachesNone)
		},
		func() {
			c.ok(w.stack.VisibleChildName() == "overview" && len(seen) == 0, "page %q after Clear caches found nothing, want overview, never another; visited %v", w.stack.VisibleChildName(), seen)
			c.ok(!strings.Contains(cacheCell(w), "Measuring"), "cell with the nothing dialog open %q", cacheCell(w))
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
			c.ok(!strings.Contains(cacheCell(w), "Measuring"), "cell after OK %q", cacheCell(w))
			c.ok(w.stack.VisibleChildName() == "overview", "page %q after OK", w.stack.VisibleChildName())
		},
	)
}

func init() { covScenarios["cov-cachesstay"] = covCachesStay }

func TestGUICovCachesStay(t *testing.T) { covChild(t, "cov-cachesstay") }

// covCachesStay (#207 follow-up): Clear caches stays on the Overview while
// it measures, while the pick is open and after a cancel; the Run page
// comes when the queue of a non-empty pick starts.
func covCachesStay(c *covEnv) {
	w := c.w
	c.quiet()
	var seen []string
	w.stack.NotifyProperty("visible-child-name", func() { seen = append(seen, w.stack.VisibleChildName()) })
	ranPage := func() bool {
		for _, p := range seen {
			if p == "run" {
				return true
			}
		}
		return false
	}
	c.seq(900,
		func() {
			w.pickPage("overview")
			seen = nil
			w.startAction(actions.IDClearCaches)
			c.ok(w.stack.VisibleChildName() == "overview" && !ranPage(), "measuring: page %q, visited %v", w.stack.VisibleChildName(), seen)
			c.ok(w.busy, "not busy while measuring")
		},
		func() {
			c.ok(w.dialogs[1] != nil, "no pick dialog")
			c.ok(w.stack.VisibleChildName() == "overview" && !ranPage(), "pick: page %q, visited %v", w.stack.VisibleChildName(), seen)
			c.ok(!strings.Contains(cacheCell(w), "Measuring"), "cell while the pick is open %q", cacheCell(w))
			shoot(w, "cov-cachesstay", "pick")
			if d := w.dialogs[1]; d != nil {
				c.press(d, "cancel")
			}
		},
		func() {
			c.ok(!w.busy && w.stack.VisibleChildName() == "overview" && !ranPage(), "after Cancel: busy %v page %q, visited %v", w.busy, w.stack.VisibleChildName(), seen)
			c.ok(!strings.Contains(cacheCell(w), "Measuring"), "cell after Cancel %q", cacheCell(w))
			w.startAction(actions.IDClearCaches)
		},
		func() {
			c.ok(w.dialogs[1] != nil && len(w.checks) > 0, "no second pick")
			c.ok(!ranPage(), "the Run page showed before the queue: %v", seen)
			if d := w.dialogs[1]; d != nil && len(w.checks) > 0 {
				w.checks[0].SetActive(true)
				c.press(d, "ok")
			}
		},
		func() {
			c.ok(ranPage() && w.queued && w.stack.VisibleChildName() == "run", "queue started: page %q queued %v, visited %v", w.stack.VisibleChildName(), w.queued, seen)
		},
	)
}
