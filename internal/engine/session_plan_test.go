package engine_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"

	"github.com/Enzord758/archmaid/internal/steps"
)

// Results that change the plan (E2): a step inserted behind its prober, a
// gate put on the next step, and the refusals that fail closed.

func removal(items ...string) *steps.Step {
	s := sudo(child(steps.IDOrphansRemove, append([]string{"sudo", "pacman", "-Rns", "--"}, items...)...))
	s.Confirm, s.Optional, s.AlwaysLog = "Remove them?", true, true
	s.Items, s.Cascade = items, append(slices.Clone(items), "dep")
	return &s
}

func TestSessionResultInsertsAStep(t *testing.T) {
	h := newHarness(t, Options{Auth: AuthPolkit}, queue("q", inproc("probe"), child("b", "true")))
	h.start("q")
	o := h.result(steps.Result{Output: "2 orphans", InsertNext: removal("liba", "evil\x1b]0;x\a")})
	var kinds []string
	for _, ev := range o.Events {
		kinds = append(kinds, reflectName(ev))
	}
	if want := []string{"OutputChunk", "StepDone", "StepInserted", "NeedsConfirm"}; !slices.Equal(kinds, want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	ins := event[StepInserted](t, o)
	if ins.Index != 1 || ins.Step.ID != steps.IDOrphansRemove || !ins.Step.NeedsPriv || !ins.Step.Confirm {
		t.Errorf("StepInserted = %+v", ins)
	}
	nc := event[NeedsConfirm](t, o)
	if nc.Kind != ConfirmOrphans || nc.Step != steps.IDOrphansRemove || !nc.Destructive || nc.Default ||
		!slices.Equal(nc.Direct, []string{"liba", `"evil\x1b]0;x\a"`}) || len(nc.Cascade) != 3 || nc.Items != nil {
		t.Errorf("orphan confirm = %+v", nc)
	}
	o = h.confirm(true)
	if got := event[StepStarted](t, o); got.ID != steps.IDOrphansRemove || got.Index != 1 {
		t.Errorf("StepStarted = %+v", got)
	}
	o = h.ok()
	if got := event[StepStarted](t, o); got.ID != "b" || got.Index != 2 {
		t.Errorf("the step behind the insertion = %+v", got)
	}
	h.ok()
	enginetest.CheckContract(t, h.evs)
}

func TestSessionFailedResultInsertsNothing(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", inproc("probe"), child("b", "true")))
	h.start("q")
	o := h.result(steps.Result{Err: errors.New("pacman failed"), InsertNext: removal("x")})
	if _, ok := find[StepInserted](o.Events); ok {
		t.Error("a failed result inserted its step")
	}
	if got := event[StepStarted](t, o); got.ID != "b" {
		t.Errorf("next = %+v", got)
	}
}

func TestSessionConfirmNextGatesTheNextStep(t *testing.T) {
	top := child("topgrade", "topgrade", "-y")
	top.AlwaysLog = true
	for _, yes := range []bool{true, false} {
		committed := false
		commit := func() error { committed = true; return nil }
		h := newHarness(t, Options{}, queue("q", inproc("audit"), top, child("c", "true")))
		h.start("q")
		o := h.result(steps.Result{Output: "report", ConfirmNext: "foo changed maintainer. Update anyway?", Commit: commit})
		if _, ok := find[RunHook](o.Effects); ok {
			t.Fatal("the commit ran before the gate was answered")
		}
		pc := event[PlanChanged](t, o)
		if pc.Index != 1 || pc.Step.ID != "topgrade" || !pc.Step.Confirm || !pc.Step.Optional {
			t.Errorf("PlanChanged = %+v", pc)
		}
		nc := event[NeedsConfirm](t, o)
		if nc.Kind != ConfirmAuditGate || nc.Step != "topgrade" || nc.Msg != "foo changed maintainer. Update anyway?" || nc.Destructive || !nc.Commits {
			t.Errorf("gate = %+v", nc)
		}
		o = h.confirm(yes)
		hooks := all[RunHook](o.Effects)
		if yes {
			if len(hooks) != 1 || hooks[0].Step != "topgrade" {
				t.Fatalf("yes: hooks %+v, want the commit as topgrade's OnAccept", hooks)
			}
			hooks[0].Fn()
			if !committed {
				t.Error("the OnAccept isn't the result's Commit")
			}
			event[StepStarted](t, o)
			continue
		}
		if len(hooks) != 0 {
			t.Errorf("no: the commit ran")
		}
		if got := event[StepDone](t, o); got.ID != "topgrade" || got.Status != steps.Skipped {
			t.Errorf("no: %+v, want topgrade skipped (optional now)", got)
		}
		if got := event[LogWritten](t, o); got.Header != "SKIP: topgrade done (declined)" {
			t.Errorf("no: log %+v", got)
		}
		if got := event[StepStarted](t, o); got.ID != "c" {
			t.Errorf("no: the queue didn't go on: %+v", got)
		}
	}
}

func TestSessionConfirmNextOnTheLastStepIsDropped(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", inproc("audit")))
	h.start("q")
	o := h.result(steps.Result{ConfirmNext: "Update anyway?", Commit: func() error { return nil }})
	if _, ok := find[RunHook](o.Effects); ok {
		t.Error("a gate with nothing to gate ran its commit")
	}
	if _, ok := find[PlanChanged](o.Events); ok {
		t.Error("PlanChanged with no next step")
	}
	event[Finished](t, o)
}

// As in the TUI, a taken result's Commit runs even when the step failed:
// the downloads delete's says the result was taken.
func TestSessionFailedResultStillCommits(t *testing.T) {
	h := newHarness(t, Options{}, queue("q", inproc("dl")))
	h.start("q")
	effect[RunHook](t, h.result(steps.Result{Err: errors.New("partly"), Commit: func() error { return nil }}))
}

// A result that sets both ConfirmNext and InsertNext is refused, failing
// closed: the step fails, nothing is inserted, Commit never runs, and the
// gate still lands on the step it was meant for.
func TestSessionRefusedResultKeepsTheGate(t *testing.T) {
	for _, yes := range []bool{true, false} {
		h := newHarness(t, Options{}, queue("q", inproc("audit"), child("topgrade", "topgrade"), child("c", "true")))
		h.start("q")
		o := h.result(steps.Result{Output: "report", ConfirmNext: "Update anyway?",
			Commit: func() error { t.Error("a refused result's commit ran"); return nil }, InsertNext: removal("x")})
		if got := event[OutputChunk](t, o); !slices.Equal(got.Lines, []string{"report", "archmaid: " + steps.ErrConfirmAndInsert.Error()}) {
			t.Errorf("output = %q", got.Lines)
		}
		if got := event[StepDone](t, o); got.Status != steps.Failed || got.Err != steps.ErrConfirmAndInsert.Error() {
			t.Errorf("StepDone = %+v", got)
		}
		if _, ok := find[StepInserted](o.Events); ok {
			t.Error("a refused result inserted its step")
		}
		if got := event[LogWritten](t, o); got.Header != "FAIL: audit done" {
			t.Errorf("log = %+v", got)
		}
		if got := event[NeedsConfirm](t, o); got.Kind != ConfirmAuditGate || got.Step != "topgrade" || got.Commits {
			t.Errorf("the gate didn't stand: %+v", got)
		}
		o = h.confirm(yes)
		if hooks := all[RunHook](o.Effects); len(hooks) != 0 {
			for _, hk := range hooks {
				hk.Fn()
			}
			t.Errorf("hooks after a refused result")
		}
		if yes {
			if got := event[StepStarted](t, o); got.ID != "topgrade" {
				t.Errorf("yes: %+v", got)
			}
		} else if got := event[StepDone](t, o); got.ID != "topgrade" || got.Status != steps.Skipped {
			t.Errorf("no: %+v", got)
		}
	}
}

// Nothing needing root may follow hyprpm, which drops the sudo ticket: a
// result inserting such a step once hyprpm has run is refused.
func TestSessionNoRootInsertedAfterHyprpm(t *testing.T) {
	hy := inproc(steps.IDHyprpm) // contrived: the only way to reach past hyprpm
	h := newHarness(t, Options{}, queue("q", hy))
	h.start("q")
	o := h.result(steps.Result{InsertNext: removal("x")})
	if _, ok := find[StepInserted](o.Events); ok {
		t.Fatal("a root step was inserted after hyprpm")
	}
	if got := event[StepDone](t, o); got.Status != steps.Failed || !strings.Contains(got.Err, "after hyprpm") {
		t.Errorf("StepDone = %+v", got)
	}
	event[Finished](t, o)

	// A step that needs no root may still follow.
	h = newHarness(t, Options{}, queue("q", hy))
	h.start("q")
	plain := child("note", "true")
	if got := event[StepInserted](t, h.result(steps.Result{InsertNext: &plain})); got.Index != 1 {
		t.Errorf("StepInserted = %+v", got)
	}
}

func TestSessionDownloadsConfirmListsItems(t *testing.T) {
	dl := inproc(steps.IDDownloads)
	dl.Confirm, dl.Optional, dl.Items = "Delete 2 item(s)?\n\n  a\n  b", true, []string{"a", "b\nforged"}
	h := newHarness(t, Options{}, queue("q", dl))
	nc := event[NeedsConfirm](t, h.start("q"))
	if nc.Kind != ConfirmDownloads || !nc.Destructive || !slices.Equal(nc.Items, []string{"a", `"b\nforged"`}) ||
		nc.Title != "Delete 2 item(s)?" || nc.Body != "  a\n  b" {
		t.Errorf("downloads confirm = %+v", nc)
	}
}
