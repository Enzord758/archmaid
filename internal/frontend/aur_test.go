package frontend

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/steps"
)

type fakePreparer struct {
	prep aurbuild.Prepared
	err  error
	got  []string
}

func (f *fakePreparer) Prepare(_ context.Context, names []string) (aurbuild.Prepared, error) {
	f.got = names
	return f.prep, f.err
}

// TestAURPlanner: a prepared install becomes the actions' AURPrepared with
// the fetcher's queue; aurbuild's refusals become *steps.PkgRefusal; any other
// error passes through; the audit's note reaches the sheet.
func TestAURPlanner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "aur")
	fetcher := aurbuild.Fetcher{Root: root}
	reviewed := &aurbuild.Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}
	plans := []steps.AURPlan{{Base: "app", Names: []string{"app"}, Commit: strings.Repeat("a", 40)}}
	fp := &fakePreparer{prep: aurbuild.Prepared{Plans: plans, RepoDeps: []string{"libfoo"}, AuditNote: "audit\x1b[0m could not run"}}
	plan := AURPlanner(fp, fetcher, reviewed)

	got, err := plan(context.Background(), []string{"app"})
	if err != nil || strings.Join(fp.got, ",") != "app" {
		t.Fatalf("%v, asked %v", err, fp.got)
	}
	if len(got.Plans) != 1 || strings.Join(got.RepoDeps, ",") != "libfoo" || strings.Join(got.Notes, "|") != "audit could not run" {
		t.Fatalf("prepared = %+v", got)
	}
	deps := &steps.Step{ID: steps.IDPkgInstall}
	q := got.Queue(deps)
	var ids []string
	for _, st := range q {
		ids = append(ids, st.ID)
	}
	if strings.Join(ids, ",") != "pkg-install,aur-fetch:app,aur-build:app,aur-collect,aur-remember" {
		t.Fatalf("queue = %v", ids)
	}

	for _, tc := range []struct {
		err    error
		reason steps.RefuseReason
		names  string
	}{
		{&aurbuild.Refusal{Reason: aurbuild.ReasonCycle, Package: "a", Detail: "a circle"}, steps.RefuseAUR, "a"},
		{&aurbuild.Refusal{Reason: aurbuild.ReasonNotFound, Package: "ghost", Detail: "ghost is not in the AUR"}, steps.RefuseNotFound, "ghost"},
		{&aurbuild.Refusal{Reason: aurbuild.ReasonBadRequest, Detail: "ask for 1 to 32 packages"}, steps.RefuseAUR, ""},
	} {
		fp.err = tc.err
		_, err := plan(context.Background(), []string{"x"})
		var pr *steps.PkgRefusal
		if !errors.As(err, &pr) || pr.Reason != tc.reason || strings.Join(pr.Names, ",") != tc.names || pr.Detail == "" {
			t.Errorf("%v -> %+v", tc.err, err)
		}
	}
	fp.err = errors.New("git: network down")
	if _, err := plan(context.Background(), []string{"x"}); err == nil || errors.As(err, new(*steps.PkgRefusal)) {
		t.Fatalf("a plain failure must stay a plain error: %v", err)
	}
}
