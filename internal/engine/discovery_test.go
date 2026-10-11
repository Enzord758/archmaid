package engine_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// discoverAction is a Discover action recording to logstore.Services, as
// the menu's services action.
func discoverAction() actions.Action {
	return actions.Action{ID: actions.IDServices, Label: "Services", Busy: "Finding…", Record: logstore.Services,
		Discover: func(context.Context, actions.Env) actions.Discovery { panic("the Session never discovers") }}
}

// eventTypes is the events' types, for an order check.
func eventTypes(evs []Event) []string {
	var out []string
	for _, ev := range evs {
		switch ev.(type) {
		case PhaseChanged, StatusUpdated:
			continue
		}
		out = append(out, strings.TrimPrefix(fmt.Sprintf("%T", ev), "engine."))
	}
	return out
}

// TestDiscoveryRecordNothingFound: a discovery that finds nothing ends the
// action with its record: DiscoveryResult (what was checked), its log
// entry in the action's discovery log (details, then commands and raw
// output), the Notice, Finished — and last-run.json says nothing-found.
func TestDiscoveryRecordNothingFound(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry=%v", dry), func(t *testing.T) {
			h := newHarness(t, Options{DryRun: dry}, discoverAction())
			h.start(actions.IDServices)
			h.wait(3 * time.Second)
			o := h.handle(Discovered{Seq: effect[Discover](t, h.out).Seq, Discovery: actions.Notice{
				Text:    "Checked 2 stopped services: none are enabled. Skipped 1 (systemd-*).",
				Details: []string{"a.service: disabled", "b.service: \x1b[31mstatic\x1b[0m\nx", "systemd-x.service: skipped, excluded: systemd-"},
				Log:     "$ systemctl list-units …\nraw listing\n",
			}})
			if got, want := eventTypes(o.Events), []string{"DiscoveryResult", "LogWritten", "Notice", "Finished"}; !slices.Equal(got, want) {
				t.Fatalf("events %v, want %v", got, want)
			}
			dr := event[DiscoveryResult](t, o)
			want := DiscoveryResult{Action: actions.IDServices, Outcome: DiscoveryNothing,
				Summary:  "Checked 2 stopped services: none are enabled. Skipped 1 (systemd-*).",
				Details:  []string{"a.service: disabled", "b.service: static x", "systemd-x.service: skipped, excluded: systemd-"},
				Category: logstore.Services, Started: t0, Elapsed: 3 * time.Second}
			if fmt.Sprint(dr) != fmt.Sprint(want) {
				t.Errorf("DiscoveryResult\n got %+v\nwant %+v", dr, want)
			}
			wl := effect[WriteLog](t, o)
			if wl.Category != logstore.Services || wl.Kind != "CHECK" || wl.Label != want.Summary ||
				!strings.HasPrefix(wl.Body, "a.service: disabled\n") || !strings.HasSuffix(wl.Body, "\n$ systemctl list-units …\nraw listing\n") {
				t.Errorf("WriteLog = %+v", wl)
			}
			if lw := event[LogWritten](t, o); lw != (LogWritten{Category: logstore.Services, Header: "CHECK: " + want.Summary}) {
				t.Errorf("LogWritten = %+v", lw)
			}
			if n := event[Notice](t, o); n.Text != want.Summary {
				t.Errorf("Notice = %q", n.Text)
			}
			rec := effect[WriteLastRun](t, o).Record
			if rec.Action != actions.IDServices || rec.Outcome != logstore.OutcomeNothingFound || rec.StepsFailed != 0 ||
				rec.DryRun != dry || !rec.Started.Equal(t0) || !rec.Finished.Equal(t0.Add(3*time.Second)) {
				t.Errorf("record = %+v", rec)
			}
			finished(t, o, Finished{Action: actions.IDServices, Outcome: OutcomeNotice})
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestDiscoveryRecordFailed: a failed look is recorded as failed, with its
// error, what was tried, and a FAIL entry in the discovery log.
func TestDiscoveryRecordFailed(t *testing.T) {
	h := newHarness(t, Options{}, discoverAction())
	h.start(actions.IDServices)
	o := h.handle(Discovered{Seq: effect[Discover](t, h.out).Seq, Discovery: actions.Notice{
		Text: "Couldn't list services: Failed to connect to bus", Err: "Failed to connect to bus",
		Details: []string{"Tried: systemctl list-units --type=service"}, Log: "error: Failed to connect to bus\n",
	}})
	dr := event[DiscoveryResult](t, o)
	if dr.Outcome != DiscoveryFailed || dr.Err != "Failed to connect to bus" || dr.Summary != "Couldn't list services: Failed to connect to bus" ||
		!slices.Equal(dr.Details, []string{"Tried: systemctl list-units --type=service"}) {
		t.Errorf("DiscoveryResult = %+v", dr)
	}
	if wl := effect[WriteLog](t, o); wl.Kind != "FAIL" || wl.Category != logstore.Services || !strings.Contains(wl.Body, "error: Failed") {
		t.Errorf("WriteLog = %+v", wl)
	}
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeFailed || rec.StepsFailed != 1 {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestDiscoveryRecordFound: a discovery that finds something says so at
// once (Found), and its record heads the log of the queue it leads to,
// written when that starts, in the first step's log; a pick of nothing
// writes no record and no last-run, as before.
func TestDiscoveryRecordFound(t *testing.T) {
	sel := actions.NeedsSelection{Items: []string{"a.service"},
		Steps: func(picked []string) []steps.Step { return []steps.Step{child("disable", "systemctl")} },
		Checked: actions.Checked{Summary: "Checked 2 stopped services: 1 is enabled.",
			Details: []string{"a.service: enabled", "b.service: disabled"}, Log: "$ systemctl is-enabled -- a.service b.service\n"}}

	h := newHarness(t, Options{}, discoverAction())
	h.start(actions.IDServices)
	o := h.handle(Discovered{Seq: effect[Discover](t, h.out).Seq, Discovery: sel})
	if got := eventTypes(o.Events); !slices.Equal(got, []string{"DiscoveryResult", "NeedsSelection"}) {
		t.Fatalf("events %v", got)
	}
	if dr := event[DiscoveryResult](t, o); dr.Outcome != DiscoveryFound || dr.Summary != sel.Checked.Summary ||
		len(dr.Details) != 2 || dr.Category != "" {
		t.Errorf("DiscoveryResult = %+v", dr)
	}
	if _, ok := find[WriteLog](o.Effects); ok {
		t.Error("a Found record was written before its queue started")
	}
	o = h.do(h.s.Select(event[NeedsSelection](t, o).ID, []string{"a.service"}))
	if got := eventTypes(o.Events); len(got) < 2 || got[0] != "QueueStarted" || got[1] != "LogWritten" {
		t.Fatalf("events %v: want the record right after QueueStarted", got)
	}
	if wl := effect[WriteLog](t, o); wl.Category != logstore.Cleanup || wl.Kind != "CHECK" || wl.Label != sel.Checked.Summary ||
		!strings.Contains(wl.Body, "a.service: enabled\nb.service: disabled\n\n$ systemctl is-enabled") {
		t.Errorf("WriteLog = %+v", wl)
	}
	o = h.ok()
	if rec := effect[WriteLastRun](t, o).Record; rec.Outcome != logstore.OutcomeOK {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)

	// Nothing picked: nothing written, no last-run record.
	h = newHarness(t, Options{}, discoverAction())
	h.start(actions.IDServices)
	o = h.handle(Discovered{Seq: effect[Discover](t, h.out).Seq, Discovery: sel})
	o = h.do(h.s.Select(event[NeedsSelection](t, o).ID, nil))
	if _, ok := find[WriteLog](o.Effects); ok {
		t.Error("a cancelled pick wrote a record")
	}
	if _, ok := find[WriteLastRun](o.Effects); ok {
		t.Error("a cancelled pick wrote a last-run record")
	}
	enginetest.CheckContract(t, h.evs)
}

// TestDiscoveryRecordStages: Repair's user scan (Found) and its root
// recheck: each DiscoveryResult carries every stage's details so far, and
// the record of a recheck that finds nothing holds the user scan's too.
func TestDiscoveryRecordStages(t *testing.T) {
	a, nr := needsRootAction()
	a.Record = logstore.Repair
	nr.Checked = actions.Checked{Summary: "Checked 4 installed packages as the user: 1 flagged (foo), to recheck as root.",
		Details: []string{"foo /boot/foo"}, Log: "$ pacman -Qq\n"}
	h := newHarness(t, Options{}, a)
	h.start("r")
	o := h.discovered(nr)
	if dr := event[DiscoveryResult](t, o); dr.Outcome != DiscoveryFound || !slices.Equal(dr.Details, []string{"foo /boot/foo"}) {
		t.Errorf("user scan: %+v", dr)
	}
	h.handle(ProbeDone{Cached: true})
	o = h.discovered(actions.Notice{Text: "No broken packages: the 1 flagged as the user (foo) are complete as root.",
		Details: []string{"Rechecked as root: nothing missing"}, Log: "$ sudo -n pacman -Qkq -- foo\n"})
	dr := event[DiscoveryResult](t, o)
	if dr.Outcome != DiscoveryNothing || !slices.Equal(dr.Details, []string{"foo /boot/foo", "Rechecked as root: nothing missing"}) ||
		dr.Category != logstore.Repair {
		t.Errorf("recheck: %+v", dr)
	}
	wl := effect[WriteLog](t, o)
	if wl.Category != logstore.Repair || !strings.Contains(wl.Body, "foo /boot/foo") || !strings.Contains(wl.Body, "$ pacman -Qq") ||
		!strings.Contains(wl.Body, "$ sudo -n pacman -Qkq -- foo") {
		t.Errorf("WriteLog = %+v", wl)
	}
	if n := len(all[WriteLog](o.Effects)); n != 1 {
		t.Errorf("%d log entries, want the one record", n)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestDiscoveryRecordAbort: an Abort during the discovery writes no
// record, as before (OutcomeCancelled, no last-run).
func TestDiscoveryRecordAbort(t *testing.T) {
	h := newHarness(t, Options{}, discoverAction())
	h.start(actions.IDServices)
	o := h.do(h.s.Abort())
	if _, ok := find[DiscoveryResult](o.Events); ok {
		t.Error("an aborted discovery has a result")
	}
	if _, ok := find[WriteLastRun](o.Effects); ok {
		t.Error("an aborted discovery wrote a last-run record")
	}
	enginetest.CheckContract(t, h.evs)
}

// TestRepairCheckBrokenRecord: under polkit, the recheck that finds broken
// packages says so (Found) and its record heads the reinstall's log, so the
// run is a real one before the reinstall runs.
func TestRepairCheckBrokenRecord(t *testing.T) {
	h, seq := repairCheck(t)
	h.handle(HelperCheck{Seq: seq, Broken: []string{"foo"}})
	o := h.handle(StepExited{Seq: seq})
	dr := event[DiscoveryResult](t, o)
	if dr.Outcome != DiscoveryFound || dr.Summary != "1 broken (foo), confirmed as root by the root helper." {
		t.Errorf("DiscoveryResult = %+v", dr)
	}
	logs := all[WriteLog](o.Effects)
	if len(logs) != 1 || logs[0].Category != actions.RepairStep(nil).LogCategory() || logs[0].Kind != "CHECK" {
		t.Errorf("WriteLog = %+v", logs)
	}
	if got := eventTypes(o.Events); slices.Index(got, "StepInserted") > slices.Index(got, "DiscoveryResult") {
		t.Errorf("events %v: the reinstall goes in before the result", got)
	}
}
