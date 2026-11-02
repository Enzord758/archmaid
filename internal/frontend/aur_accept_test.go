package frontend

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/steps"
)

// acceptFixture is an AUR install whose audit found a takeover of "victim":
// the discovery's queue and the snapshot file the accept would advance.
func acceptFixture(t *testing.T, high, hasAudit, dry bool) (actions.PkgPreview, string) {
	t.Helper()
	cfg := takeover(t)
	cfg.DryRun = dry
	res, err := audit.Run(context.Background(), cfg, time.Now())
	if err != nil || res.High() != 1 {
		t.Fatalf("audit: %v, high %d", err, res.High())
	}
	plan := steps.AURPlan{Base: "victim", Names: []string{"victim"}, Commit: strings.Repeat("a", 40)}
	if high {
		plan.Audit = []steps.AURFinding{{Name: "victim", High: true, Reason: "maintainer bob → mallory"}}
	} else {
		plan.Audit = []steps.AURFinding{{Name: "victim", Reason: "unmaintained"}}
	}
	fp := &fakePreparer{prep: aurbuild.Prepared{Plans: []steps.AURPlan{plan}, Audit: res, HasAudit: hasAudit}}
	reviewed := &aurbuild.Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}
	env := actions.Env{Polkit: true,
		PlanAUR:      AURPlanner(fp, aurbuild.Fetcher{Root: filepath.Join(t.TempDir(), "aur")}, reviewed),
		PlanPackages: func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) { return steps.PkgPlan{}, nil }}
	req := steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"victim"}, AUR: true}
	pv, ok := actions.AURDiscover(req)(context.Background(), env).(actions.PkgPreview)
	if !ok {
		t.Fatal("no preview")
	}
	return pv, cfg.SnapshotPath
}

func maintainer(t *testing.T, snap string) string {
	t.Helper()
	m, _ := audit.LoadSnapshot(snap)
	return m["victim"].Maintainer
}

// TestAURAcceptCommitsTheAuditSnapshot: with high findings the first step's
// OnAccept is the topgrade accept's commit (highs advance); nothing is
// written before it runs, and running it once is enough.
func TestAURAcceptCommitsTheAuditSnapshot(t *testing.T) {
	pv, snap := acceptFixture(t, true, true, false)
	if pv.Steps[0].OnAccept == nil {
		t.Fatalf("no accept on %s", pv.Steps[0].ID)
	}
	if got := maintainer(t, snap); got != "bob" {
		t.Fatalf("snapshot moved before the accept: maintainer %q", got)
	}
	if err := pv.Steps[0].OnAccept(); err != nil {
		t.Fatal(err)
	}
	if got := maintainer(t, snap); got != "mallory" {
		t.Fatalf("after the accept the snapshot has maintainer %q; want mallory", got)
	}
}

// TestAURNoAcceptWithoutHighFindings: no high finding, no audit result or a
// dry run leaves the queue as it was.
func TestAURNoAcceptWithoutHighFindings(t *testing.T) {
	for name, c := range map[string]struct{ high, hasAudit, dry bool }{
		"no high findings in the plans": {false, true, false},
		"no audit result":               {true, false, false},
		"dry run":                       {true, true, true},
	} {
		pv, snap := acceptFixture(t, c.high, c.hasAudit, c.dry)
		for _, st := range pv.Steps {
			if st.OnAccept != nil {
				t.Errorf("%s: %s has an OnAccept", name, st.ID)
			}
		}
		if got := maintainer(t, snap); got != "bob" {
			t.Errorf("%s: snapshot changed (%q)", name, got)
		}
	}
}
