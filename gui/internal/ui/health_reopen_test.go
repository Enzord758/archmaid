package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// covHealthReopen (#286): the Mirror row showed the old mirror until "Check
// again" after pacman.conf changed. Opening the sheet on a result that is
// over healthSheetStale old runs the checks again, the old rows meanwhile
// with "Checking…"; a result that is fresh is left alone; never two runs at
// once.
func covHealthReopen(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	var d *amDialog
	rowIDs := func() []string {
		var ids []string
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if n := gtk.BaseWidget(wd).Name(); strings.HasPrefix(n, "health-") {
				ids = append(ids, strings.TrimPrefix(n, "health-"))
			}
		})
		return ids
	}
	base := 0
	c.seq(300,
		func() {}, func() {}, func() {}, // the first run (400 ms) is over
		func() {
			base, _, _ = f.counts()
			c.ok(!w.health.run.Running(), "the first run is still going")
			f.set(twoLooks) // pacman.conf changed: the checks would say something else now
		},
		func() {}, // the result is now over 300 ms old
		func() {
			w.openHealth()
			d = w.health.sheet
			c.ok(w.health.run.Running(), "opening the sheet on an old result did not run the checks")
			again := button2(d, "Check again")
			c.ok(again != nil && again.Label() == "Checking…" && !again.Sensitive(), "while it runs the button is %v", again)
			c.ok(slices.Contains(rowIDs(), "disk") && slices.Contains(rowIDs(), "pacnew"), "the old rows are not shown meanwhile: %v", rowIDs())
			c.ok(!slices.Contains(rowIDs(), "failed-units"), "the new rows are there before the run ended: %v", rowIDs())
			w.refreshHealth() // another request meanwhile: one more run after this one, never two at once
		},
		func() {}, func() {}, func() {}, func() {},
		func() {
			c.ok(slices.Contains(rowIDs(), "failed-units") && slices.Contains(rowIDs(), "news"), "the new answer is not in the sheet: %v", rowIDs())
			runs, peak, _ := f.counts()
			c.ok(peak == 1, "%d checks ran at once", peak)
			c.ok(runs == base+2, "the open's run and the request meanwhile ran %d times, want 2", runs-base)
			base = runs
			c.press(d, "close")
		},
		func() {},
		func() {
			// A fresh result (the run just ended; the limit is 300 ms but a
			// longer one holds) is left alone.
			healthSheetStale = time.Minute
			w.openHealth()
			d = w.health.sheet
			runs, _, _ := f.counts()
			c.ok(runs == base, "opening the sheet on a fresh result ran the checks (%d, %d)", base, runs)
			again := button2(d, "Check again")
			c.ok(again != nil && again.Label() == "Check again" && again.Sensitive(), "no run is going, but the button is %v", again)
			c.press(d, "close")
		},
		func() {
			if w.health.sheet != nil {
				w.health.sheet.ForceClose()
			}
		},
	)
}
