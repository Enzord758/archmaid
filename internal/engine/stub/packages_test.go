package stub

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	runreport "github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// replayPackages sends StartPackages for req to a Driver on the catalog
// and plays it to Finished, answering every confirm with yes.
func replayPackages(t *testing.T, req steps.PkgRequest, yes bool) []engine.Event {
	t.Helper()
	clock := NewFakeClock(t0)
	d := New(nil, Options{Clock: clock, Scripts: PackagesCatalog(t0)})
	defer closeDrained(t, d)
	send(t, d, engine.StartPackages{Req: req})
	var got []engine.Event
	for {
		ev := next(t, d)
		got = append(got, ev)
		if id := promptID(ev); id != 0 {
			send(t, d, engine.Confirm{Prompt: id, Yes: yes})
		}
		if _, ok := ev.(engine.Finished); ok {
			return got
		}
	}
}

func finished(t *testing.T, evs []engine.Event) engine.Finished {
	t.Helper()
	for _, ev := range evs {
		if f, ok := ev.(engine.Finished); ok {
			return f
		}
	}
	t.Fatal("no Finished")
	return engine.Finished{}
}

func preq(mode steps.PkgMode, name string) steps.PkgRequest {
	return steps.PkgRequest{Mode: mode, Names: []string{name}}
}

// TestPackagesCatalogKeys: a request plays the script its first name or its
// mode names, and every script holds the engine contract.
func TestPackagesCatalogKeys(t *testing.T) {
	cases := map[string]struct {
		req     steps.PkgRequest
		outcome engine.Outcome
		sheet   bool
	}{
		"install":         {preq(steps.PkgInstall, "foo"), engine.OutcomeCompleted, true},
		"remove":          {preq(steps.PkgRemove, "foo"), engine.OutcomeCompleted, true},
		"stale":           {preq(steps.PkgInstall, "stale"), engine.OutcomeCompleted, true},
		"conflict":        {preq(steps.PkgInstall, "conflict"), engine.OutcomeNotice, false},
		"protected":       {preq(steps.PkgRemove, "protected"), engine.OutcomeNotice, false},
		"not-found":       {preq(steps.PkgInstall, "not-found"), engine.OutcomeNotice, false},
		"required-by":     {preq(steps.PkgRemove, "required-by"), engine.OutcomeNotice, false},
		"busy":            {preq(steps.PkgInstall, "busy"), engine.OutcomeNotice, false},
		"updates-pending": {preq(steps.PkgInstall, "updates-pending"), engine.OutcomeNotice, false},
		"plain-error":     {preq(steps.PkgInstall, "plain-error"), engine.OutcomeNotice, false},
		"aur":             {steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"paru-bin"}, AUR: true}, engine.OutcomeCompleted, true},
		"aur-high":        {steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"risky-bin"}, AUR: true}, engine.OutcomeCompleted, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			evs := replayPackages(t, tc.req, true)
			enginetest.CheckContract(t, evs)
			if f := finished(t, evs); f.Outcome != tc.outcome {
				t.Errorf("Finished = %+v, want %s", f, tc.outcome)
			}
			var sheet bool
			for _, ev := range evs {
				if nc, ok := ev.(engine.NeedsConfirm); ok && nc.Kind == engine.ConfirmPackages {
					sheet = true
				}
			}
			if sheet != tc.sheet {
				t.Errorf("sheet shown = %v, want %v", sheet, tc.sheet)
			}
		})
	}
}

// TestPackagesCatalogDeclined: a no at the sheet ends cancelled with no step.
func TestPackagesCatalogDeclined(t *testing.T) {
	evs := replayPackages(t, preq(steps.PkgInstall, "foo"), false)
	enginetest.CheckContract(t, evs)
	if f := finished(t, evs); f.Outcome != engine.OutcomeCancelled {
		t.Errorf("Finished = %+v", f)
	}
	for _, ev := range evs {
		if _, ok := ev.(engine.StepStarted); ok {
			t.Error("a step started after a no")
		}
	}
}

// TestPackagesCatalogPreviewPairsWithConfirm: the preview is followed at
// once by the confirm carrying its prompt.
func TestPackagesCatalogPreviewPairsWithConfirm(t *testing.T) {
	evs := replayPackages(t, preq(steps.PkgInstall, "foo"), true)
	for i, ev := range evs {
		if pv, ok := ev.(engine.PackagesPreview); ok {
			nc, ok := evs[i+1].(engine.NeedsConfirm)
			if !ok || nc.ID != pv.Prompt {
				t.Fatalf("PackagesPreview %+v is not followed by its NeedsConfirm: %#v", pv, evs[i+1])
			}
			return
		}
	}
	t.Fatal("no PackagesPreview")
}

// TestPackagesCatalogAUR: the AUR script's sheet carries the review, and the
// high one a high audit finding; a completed run ends with its report, which
// names the action the engine names it (actions.IDPackages).
func TestPackagesCatalogAUR(t *testing.T) {
	for name, high := range map[string]bool{"paru-bin": false, "risky-bin": true} {
		evs := replayPackages(t, steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{name}, AUR: true}, true)
		var plan *steps.PkgPlan
		var rep *runreport.Report
		for _, ev := range evs {
			switch e := ev.(type) {
			case engine.PackagesPreview:
				plan = &e.Plan
			case engine.RunReport:
				rep = &e.Report
			}
		}
		if plan == nil || len(plan.AUR) != 1 || plan.AUR[0].Base != name || len(plan.AUR[0].Files) < 3 || len(plan.AUR[0].Sources) == 0 || plan.AUR[0].Diff == "" {
			t.Fatalf("%s: plan %+v", name, plan)
		}
		hasHigh := false
		for _, f := range plan.AUR[0].Audit {
			hasHigh = hasHigh || f.High
		}
		if hasHigh != high {
			t.Errorf("%s: high finding = %v, want %v", name, hasHigh, high)
		}
		if rep == nil || rep.Action != actions.IDPackages || rep.Packages == nil || len(rep.Packages.Installed) != 2 {
			t.Errorf("%s: report %+v", name, rep)
		}
	}
}
