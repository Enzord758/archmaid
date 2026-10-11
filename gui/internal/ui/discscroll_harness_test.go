package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/glib"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// discScroll is the disc-scroll scenario's hook, called at each run's end
// (n runs ended so far): after the stub Repair's own discovery it injects
// a long one (150 detail lines, one of them far wider than the window, as
// the VM round's package check had), and once that has settled it records
// how the Run page stands. It reports whether it took over the scenario.
func discScroll(w *Window, rep *report, n int) bool {
	switch n {
	case 1:
		glib.TimeoutAdd(50, func() {
			d := engine.DiscoveryResult{Action: "services", Outcome: engine.DiscoveryNothing,
				Summary: "Checked 12 stopped services: none are enabled.", Category: logstore.Services,
				Started: time.Now(), Elapsed: time.Second}
			for i := range 150 {
				d.Details = append(d.Details, fmt.Sprintf("unit-%03d.service: skipped, excluded: initrd", i))
			}
			d.Details[3] = "wide " + strings.Repeat("/opt/unigine-superposition/bin/pro_xml_samples/", 12) + "x.xml"
			w.apply([]engine.Event{d, engine.Notice{Text: d.Summary}, engine.Finished{Outcome: engine.OutcomeNotice}})
		})
		return true
	case 2:
		glib.TimeoutAdd(700, func() {
			v, h := w.tl.scroll.VAdjustment(), w.tl.scroll.HAdjustment()
			header := false
			for _, s := range w.tl.sections {
				header = header || (s.step == runview.DiscoveryStepID && s.header != nil)
			}
			rep.DiscScroll = fmt.Sprintf("value=%.0f upper=%.0f page=%.0f hupper=%.0f hpage=%.0f header=%v follow=%v",
				v.Value(), v.Upper(), v.PageSize(), h.Upper(), h.PageSize(), header, w.follow)
			w.win.Close()
		})
		return true
	}
	return false
}

// TestGUIDiscScroll: a discovery result's page opens at its top with the
// Discovery header in view, whatever the run before it left (the VM round:
// after the first services check the page opened scrolled to the bottom,
// the header out of view, and Home didn't bring it back).
func TestGUIDiscScroll(t *testing.T) {
	needDisplay(t)
	rep, _ := child(t, "disc-scroll")
	var value, upper, page, hupper, hpage float64
	var header, follow bool
	if _, err := fmt.Sscanf(rep.DiscScroll, "value=%g upper=%g page=%g hupper=%g hpage=%g header=%t follow=%t", &value, &upper, &page, &hupper, &hpage, &header, &follow); err != nil {
		t.Fatalf("scroll report %q: %v", rep.DiscScroll, err)
	}
	if upper <= page {
		t.Fatalf("the long page doesn't scroll (%s): the test proves nothing", rep.DiscScroll)
	}
	if value != 0 || !header || follow {
		t.Errorf("the discovery page opened scrolled (%s): want the top, its header drawn, not following", rep.DiscScroll)
	}
	// The page never scrolls sideways: the header's "Done · …" sits at the
	// right edge of the view, so a line wider than the view wraps rather
	// than pushing that edge out of sight (the VM round: clipped).
	if hupper > hpage {
		t.Errorf("the page scrolls sideways (%s): a wide line must wrap, so the header's state text stays in view", rep.DiscScroll)
	}
}
