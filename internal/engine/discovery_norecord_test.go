package engine_test

import (
	"strings"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestDiscoveryWithoutRecordWritesNoLog: an action that ends at its
// discovery without a discovery Record category writes no log at all (a
// record in a real category would count as a real run and could rotate a
// real one out), and says so: a failed result with the error, and the
// error in the Notice.
func TestDiscoveryWithoutRecordWritesNoLog(t *testing.T) {
	for _, rec := range []logstore.Category{"", logstore.Cleanup, "../x"} {
		a := discoverAction()
		a.Record = rec
		h := newHarness(t, Options{}, a)
		h.start(actions.IDServices)
		o := h.handle(Discovered{Seq: effect[Discover](t, h.out).Seq, Discovery: actions.Notice{Text: "Nothing found."}})
		for _, e := range o.Effects {
			if wl, ok := e.(WriteLog); ok {
				t.Errorf("Record %q: wrote a log entry into %q", rec, wl.Category)
			}
		}
		for _, ev := range o.Events {
			if lw, ok := ev.(LogWritten); ok {
				t.Errorf("Record %q: LogWritten %+v", rec, lw)
			}
		}
		dr := event[DiscoveryResult](t, o)
		if dr.Outcome != DiscoveryFailed || dr.Category != "" || !strings.Contains(dr.Err, "no discovery log category") {
			t.Errorf("Record %q: DiscoveryResult = %+v", rec, dr)
		}
		if n := event[Notice](t, o); !strings.Contains(n.Text, "no discovery log category") {
			t.Errorf("Record %q: Notice = %q", rec, n.Text)
		}
		if r := effect[WriteLastRun](t, o).Record; r.Outcome != logstore.OutcomeFailed {
			t.Errorf("Record %q: last run = %+v", rec, r)
		}
		enginetest.CheckContract(t, h.evs)
	}
}
