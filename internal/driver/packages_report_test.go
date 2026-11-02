package driver

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// B1: a Packages page run (repo install, remove, AUR) gets a run report like
// topgrade's: what pacman.log says changed since the run started, the size
// change the confirmed plan promised, and a summary in last-run.json. A dry
// run has none. A run that failed before changing anything has none; one
// that changed something before it failed has the report of what changed.

// logHelper is a helper whose step appends logLines to the pacman log (what
// pacman does) and ends with err.
func logHelper(t *testing.T, w *reportWorld, logLines string, err error) Helper {
	return func(_ context.Context, seq uint64, _ steps.Step, emit func(engine.Input)) helperclient.Outcome {
		emit(engine.PrivilegeGranted{Seq: seq})
		if logLines != "" {
			f, e := os.OpenFile(w.log, os.O_APPEND|os.O_WRONLY, 0)
			if e != nil {
				t.Error(e)
			} else {
				f.WriteString(logLines)
				f.Close()
			}
		}
		emit(engine.StepExited{Seq: seq, Err: err})
		return helperclient.Outcome{Granted: true, Err: err}
	}
}

func sizedPlan(mode steps.PkgMode) steps.PkgPlan {
	p := pkgPlan()
	p.Mode = mode
	p.Rows = []steps.PkgRow{
		{Name: "foo", Version: "1-1", Repo: "extra", Target: true, Installed: 10 << 20},
		{Name: "libfoo", Version: "2-1", Repo: "core", Installed: 4 << 20},
	}
	return p
}

func reportRig(t *testing.T, w *reportWorld, dry bool, plan steps.PkgPlan, h Helper, aur bool) *rig {
	t.Helper()
	env := actions.Env{PlanPackages: func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) { return plan, nil }}
	if aur {
		env.PlanAUR = func(context.Context, []string) (actions.AURPrepared, error) {
			return actions.AURPrepared{
				Plans: []steps.AURPlan{{Base: "foo", Names: []string{"foo"}}},
				Queue: func(*steps.Step) []steps.Step {
					return []steps.Step{
						{ID: steps.IDAURFetchPrefix + "foo", Title: "pin", Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }},
						{ID: steps.IDPkgInstallLocal, Verb: steps.VerbPkgInstallLocal, Title: "install", NeedsPriv: true, PkgLocal: &steps.PkgLocal{}},
					}
				},
			}, nil
		}
	}
	opts := engine.Options{Auth: engine.AuthPolkit, Env: env, DryRun: dry}
	return newRig(t, opts, Deps{Polkit: true, Helper: h, RunReport: &w.cfg})
}

func reportOf(evs []engine.Event) (engine.RunReport, bool) {
	for _, ev := range evs {
		if rr, ok := ev.(engine.RunReport); ok {
			return rr, true
		}
	}
	return engine.RunReport{}, false
}

func TestPackagesRunReport(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, false, sizedPlan(steps.PkgInstall),
			logHelper(t, w, "[t] [ALPM] installed foo (1-1)\n[t] [ALPM] installed libfoo (2-1)\n", nil), false)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
		rr, ok := reportOf(evs)
		if !ok {
			t.Fatal("no RunReport for a repo install")
		}
		p := rr.Report.Packages
		if rr.Report.Action != actions.IDPackages || p == nil || len(p.Installed) != 2 || len(p.Removed) != 0 {
			t.Fatalf("report %+v packages %+v", rr.Report, p)
		}
		if !p.SizeKnown || p.SizeChange != 14<<20 {
			t.Errorf("size change %d known %v, want +14 MiB", p.SizeChange, p.SizeKnown)
		}
		rec, err := r.log.ReadLastRun()
		if err != nil || rec.Summary == nil || rec.Summary.Installed != 2 || rec.Summary.Removed != 0 || rec.Outcome != logstore.OutcomeOK {
			t.Fatalf("last-run %+v (%v): want a summary with 2 installed", rec, err)
		}
	})
	t.Run("remove", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, false, sizedPlan(steps.PkgRemove),
			logHelper(t, w, "[t] [ALPM] removed foo (1-1)\n[t] [ALPM] removed libfoo (2-1)\n", nil), false)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo"}}, true)
		rr, ok := reportOf(evs)
		if !ok || rr.Report.Packages == nil || len(rr.Report.Packages.Removed) != 2 {
			t.Fatalf("report %+v", rr)
		}
		if p := rr.Report.Packages; !p.SizeKnown || p.SizeChange != -(14<<20) {
			t.Errorf("size change %d, want -14 MiB", p.SizeChange)
		}
		if rec, _ := r.log.ReadLastRun(); rec.Summary == nil || rec.Summary.Removed != 2 || rec.Summary.Installed != 0 {
			t.Errorf("summary %+v", rec.Summary)
		}
	})
	t.Run("aur", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, false, sizedPlan(steps.PkgInstall),
			logHelper(t, w, "[t] [ALPM] installed foo (1-1)\n", nil), true)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}, AUR: true}, true)
		rr, ok := reportOf(evs)
		if !ok || rr.Report.Packages == nil || len(rr.Report.Packages.Installed) != 1 {
			t.Fatalf("report %+v", rr)
		}
		if rr.Report.Packages.SizeKnown {
			t.Error("an AUR install has no planned size, yet SizeKnown")
		}
		if rec, _ := r.log.ReadLastRun(); rec.Summary == nil || rec.Summary.Installed != 1 {
			t.Errorf("summary %+v", rec.Summary)
		}
	})
	t.Run("dry run has none", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, true, sizedPlan(steps.PkgInstall), logHelper(t, w, "", nil), false)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
		if _, ok := reportOf(evs); ok {
			t.Error("a dry run made a report")
		}
		if rec, _ := r.log.ReadLastRun(); rec.Summary != nil {
			t.Errorf("summary %+v in a dry run", rec.Summary)
		}
	})
	t.Run("failed before changing anything has none", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, false, sizedPlan(steps.PkgInstall), logHelper(t, w, "", errors.New("pacman failed")), false)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
		rr, _ := reportOf(evs)
		if rr.Report.Packages != nil {
			t.Errorf("packages %+v for a run that changed nothing", rr.Report.Packages)
		}
		rec, _ := r.log.ReadLastRun()
		if rec.Summary != nil || rec.Outcome != logstore.OutcomeFailed {
			t.Errorf("record %+v: want failed, no summary", rec)
		}
	})
	t.Run("failed after changing something reports the change", func(t *testing.T) {
		w := newReportWorld(t)
		r := reportRig(t, w, false, sizedPlan(steps.PkgInstall),
			logHelper(t, w, "[t] [ALPM] installed foo (1-1)\n", errors.New("scriptlet failed")), false)
		evs := r.runPackages(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}, true)
		rr, _ := reportOf(evs)
		p := rr.Report.Packages
		if p == nil || len(p.Installed) != 1 || p.SizeKnown {
			t.Fatalf("packages %+v: the change stands, the planned size does not (the step failed)", p)
		}
		if rec, _ := r.log.ReadLastRun(); rec.Summary == nil || rec.Summary.Installed != 1 || rec.Outcome != logstore.OutcomeFailed {
			t.Errorf("record %+v", rec)
		}
	})
}

var _ = report.ActionPackages
