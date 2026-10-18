package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// reportWorld is a machine in a temp dir for the run report: a pacman log,
// a cache folder with packages, and a config whose commands answer from
// fixtures. Nothing here is the machine's own.
type reportWorld struct {
	log, cache string
	cfg        report.Config
}

func newReportWorld(t *testing.T) *reportWorld {
	t.Helper()
	root := t.TempDir()
	w := &reportWorld{log: filepath.Join(root, "pacman.log"), cache: filepath.Join(root, "cache")}
	if err := os.MkdirAll(w.cache, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c", "d"} {
		if err := os.WriteFile(filepath.Join(w.cache, n+"-1-1-x86_64.pkg.tar.zst"), make([]byte, 64<<10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(w.log, []byte("[t] [ALPM] upgraded before-the-run (1 -> 2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cfg = report.Config{
		PacmanLog:    w.log,
		ModulesDir:   filepath.Join(root, "modules"),
		TmpDirs:      []string{filepath.Join(root, "tmp")},
		JournalDirs:  []string{filepath.Join(root, "journal")},
		UserCacheDir: filepath.Join(root, "usercache"),
		YayDir:       func() string { return filepath.Join(root, "yay") },
		Running:      func() string { return "" },
		Query: func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "pacman-conf":
				return []byte(w.cache + "/\n"), nil
			case "pacman":
				return []byte("aur-thing\n"), nil
			case "paccache":
				return []byte("\n==> finished dry run: 2 candidates (disk space saved: 128 KiB)\n"), nil
			}
			return nil, errors.New("no such command " + name)
		},
	}
	return w
}

// reportQueue: a topgrade stand-in that appends to the log, then paccache
// stand-in that removes two of the four packages.
func (w *reportWorld) queue() []steps.Step {
	topgrade := shStep(steps.IDTopgrade,
		"printf '%s\\n' '[t] [ALPM] upgraded aur-thing (1-1 -> 2-1)' '[t] [ALPM] upgraded ripgrep (14-1 -> 15-1)' '[t] [ALPM] installed zenity (4-1)' >> "+w.log)
	paccache := shStep(steps.IDPaccache, "rm "+filepath.Join(w.cache, "a-1-1-x86_64.pkg.tar.zst")+" "+filepath.Join(w.cache, "b-1-1-x86_64.pkg.tar.zst"))
	return []steps.Step{topgrade, paccache}
}

// A run with a report configured: RunReport comes after the last StepDone
// and right before Finished, carries the log's lines since the start and
// the freed bytes, and last-run.json gets the summary object.
func TestRunReport(t *testing.T) {
	w := newReportWorld(t)
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", w.queue()...))
	evs := r.run("q", yes)

	var rr engine.RunReport
	idx, finished, lastDone := -1, -1, -1
	for i, ev := range evs {
		switch ev := ev.(type) {
		case engine.RunReport:
			rr, idx = ev, i
		case engine.Finished:
			finished = i
		case engine.StepDone:
			lastDone = i
		}
	}
	if idx < 0 {
		t.Fatalf("no RunReport event\n%s", kinds(evs))
	}
	if !(lastDone < idx && idx == finished-1) {
		t.Errorf("RunReport at %d, last StepDone at %d, Finished at %d: it must follow every step and sit right before Finished", idx, lastDone, finished)
	}
	p := rr.Report.Packages
	if p == nil || len(p.Upgraded) != 2 || p.AURUpgraded != 1 || len(p.Installed) != 1 {
		t.Fatalf("packages %+v: the line before the run must not count", p)
	}
	if rr.Report.Steps != (report.StepTally{Done: 2}) || rr.Report.Action != "q" {
		t.Errorf("report %+v", rr.Report)
	}
	if rr.Report.FreedBytes < 128<<10 || rr.Report.Cache == nil || rr.Report.Cache.Packages != 2 {
		t.Errorf("freed %d, cache %+v", rr.Report.FreedBytes, rr.Report.Cache)
	}

	rec, err := r.log.ReadLastRun()
	if err != nil || rec.Summary == nil {
		t.Fatalf("last run = %+v (%v), want a summary", rec, err)
	}
	if rec.Summary.Upgraded != 2 || rec.Summary.AUR != 1 || rec.Summary.FreedBytes != rr.Report.FreedBytes || rec.Summary.Attention == nil {
		t.Errorf("summary %+v", rec.Summary)
	}
	if rec.V != 1 || rec.Outcome != logstore.OutcomeOK {
		t.Errorf("record %+v: the schema stays v1, the outcome as before", rec)
	}
}

// A slow step before the cleaning steps (topgrade, a password wait) does not
// spend the measuring cap: the cache step still gets a known freed size.
func TestRunReportSlowStepBeforeCacheSteps(t *testing.T) {
	w := newReportWorld(t)
	w.cfg.MeasureTotal = 300 * time.Millisecond // shorter than the slow step
	q := append([]steps.Step{shStep(steps.IDIntegrity, "sleep 1")}, w.queue()...)
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", q...))
	rr := find[engine.RunReport](t, r.run("q", yes))
	a := rr.Report.Freed
	if len(a) == 0 || !a[0].Known || a[0].Bytes < 128<<10 {
		t.Errorf("freed rows %+v, want the package cache measured (128 KB)", a)
	}
	if rr.Report.FreedPartial || rr.Report.FreedBytes < 128<<10 {
		t.Errorf("freed %d partial %v", rr.Report.FreedBytes, rr.Report.FreedPartial)
	}
}

// Fail-before for the whole feature: without Deps.RunReport the Driver adds
// no event, reads no log and the record carries no summary key at all (a
// Driver built for a test must never read the machine's pacman log).
func TestNoRunReportByDefault(t *testing.T) {
	w := newReportWorld(t)
	r := newRig(t, engine.Options{}, Deps{}, action("q", w.queue()...))
	evs := r.run("q", yes)
	for _, ev := range evs {
		if _, ok := ev.(engine.RunReport); ok {
			t.Fatal("a Driver without RunReport made one")
		}
	}
	b, err := os.ReadFile(filepath.Join(r.log.Root(), logstore.LastRunFile))
	if err != nil || strings.Contains(string(b), "summary") {
		t.Errorf("last-run.json = %s (%v), want no summary", b, err)
	}
}

// A dry run changes nothing, so it has no report.
func TestRunReportNotInADryRun(t *testing.T) {
	w := newReportWorld(t)
	r := newRig(t, engine.Options{DryRun: true}, Deps{RunReport: &w.cfg}, action("q", w.queue()...))
	for _, ev := range r.run("q", yes) {
		if _, ok := ev.(engine.RunReport); ok {
			t.Fatal("a dry run made a report")
		}
	}
	if rec, err := r.log.ReadLastRun(); err != nil || rec.Summary != nil {
		t.Errorf("dry-run record %+v (%v)", rec, err)
	}
}

// A queue with nothing to report (no package step, no cleaning step) still
// gets a RunReport with its tally, but its record has no summary.
func TestRunReportQuietAction(t *testing.T) {
	w := newReportWorld(t)
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", shStep("noop", "true")))
	evs := r.run("q", yes)
	rr := find[engine.RunReport](t, evs)
	if rr.Report.Packages != nil || len(rr.Report.Freed) != 0 || rr.Report.Steps.Done != 1 {
		t.Errorf("report %+v", rr.Report)
	}
	if rec, _ := r.log.ReadLastRun(); rec.Summary != nil {
		t.Errorf("summary %+v for a run with nothing to report", rec.Summary)
	}
}

// The audit step's findings reach the report through the step's Result.
func TestRunReportAudit(t *testing.T) {
	w := newReportWorld(t)
	audit := steps.Step{ID: steps.IDAURAudit, Title: "Audit…", LogLabel: "AUR audit",
		Run: func(context.Context, steps.Env) steps.Result {
			return steps.Result{Output: "x", Audit: &steps.AuditNote{Total: 3,
				Findings: []steps.AuditFinding{{Name: "evil", High: true}}}}
		}}
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", audit))
	rr := find[engine.RunReport](t, r.run("q", yes))
	if a := rr.Report.Audit; a == nil || a.High != 1 || !reflect.DeepEqual(a.Names, []string{"evil"}) {
		t.Errorf("audit %+v", a)
	}
	if len(rr.Report.Attention) != 1 || rr.Report.Attention[0].Kind != report.KindAudit {
		t.Errorf("attention %+v", rr.Report.Attention)
	}
}

// A Close while a report would be made does not hold the teardown up: the
// walks are cancelled with the Driver.
func TestRunReportCloseDoesNotBlock(t *testing.T) {
	w := newReportWorld(t)
	w.cfg.MeasureEach = time.Hour
	w.cfg.MeasureTotal = time.Hour
	r := newRig(t, engine.Options{}, Deps{RunReport: &w.cfg}, action("q", shStep(steps.IDPaccache, "sleep 30")))
	r.d.Send(engine.Start{Action: "q"})
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("events closed")
		}
		if c, ok := ev.(engine.NeedsConfirm); ok {
			r.d.Send(engine.Confirm{Prompt: c.ID, Yes: true})
		}
		if _, ok := ev.(engine.StepStarted); ok {
			break
		}
	}
	done := make(chan struct{})
	go func() { r.d.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
}
