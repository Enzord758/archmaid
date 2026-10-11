package engine_test

import (
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
)

// TestDiscoveryOutcomeString: the outcome words last-run.json and the GUI
// show, and a value outside the set is "discovery?", never an index panic.
func TestDiscoveryOutcomeString(t *testing.T) {
	for o, want := range map[DiscoveryOutcome]string{
		DiscoveryFound: "found", DiscoveryNothing: "nothing-found", DiscoveryFailed: "failed",
		DiscoveryOutcome(-1): "discovery?", DiscoveryFailed + 1: "discovery?",
	} {
		if got := o.String(); got != want {
			t.Errorf("DiscoveryOutcome(%d) = %q, want %q", int(o), got, want)
		}
	}
}

// TestRepairCheckSummaryNamesThreeThenEllipsis: a recheck of many packages
// names the first three and an ellipsis in its notice, not the whole list.
func TestRepairCheckSummaryNamesThreeThenEllipsis(t *testing.T) {
	a := queue(actions.IDRepair, actions.RepairCheckStep([]string{"a", "b", "c", "d", "e"}))
	a.Record = logstore.Repair
	h := newHarness(t, Options{Auth: AuthPolkit}, a)
	h.start(actions.IDRepair)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	h.handle(PrivilegeGranted{Seq: seq})
	h.handle(HelperCheck{Seq: seq})
	o := h.handle(StepExited{Seq: seq})
	want := "No broken packages: the 5 flagged as the user (a, b, c, …) are complete as root."
	if got := event[Notice](t, o); got.Text != want {
		t.Errorf("Notice = %q, want %q", got.Text, want)
	}
	enginetest.CheckContract(t, h.evs)
}
