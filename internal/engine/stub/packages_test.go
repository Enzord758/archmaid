package stub

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
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
