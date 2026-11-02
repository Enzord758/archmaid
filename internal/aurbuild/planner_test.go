package aurbuild

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func plannerFixture(t *testing.T) (*shims, Planner) {
	t.Helper()
	s, f := newShims(t)
	s.repoFile("PKGBUILD", "pkgname=app\n")
	s.repoFile(".SRCINFO", plainSrcinfo)
	db := fixtureDB(t, []pkgdb.Package{syncPkg("libfoo", "1-1")}, nil)
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Depends: []string{"libfoo"}})
	return s, Planner{Fetcher: f, Repo: db, AUR: aur,
		Reviewed: &Reviewed{Path: filepath.Join(t.TempDir(), "r.json")}, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
}

// TestPrepare: resolve, fetch, review end to end through the shims, with the
// last reviewed commit read from the state and the audit's findings attached.
func TestPrepare(t *testing.T) {
	s, p := plannerFixture(t)
	prev := strings.Repeat("1", 40)
	if err := p.Reviewed.Mark(map[string]string{"app": prev}); err != nil {
		t.Fatal(err)
	}
	s.put("diff.out", "+change\n")
	var asked []string
	url := auditServer(t, map[string]string{"app": "mallory"}, &asked)
	snap := filepath.Join(t.TempDir(), audit.SnapshotFile)
	audit.WriteSnapshot(snap, map[string]audit.Entry{"app": {Maintainer: "alice", HasCo: true}})
	p.Audit = &audit.Config{RPCURL: url, SnapshotPath: snap, Deadline: 5 * time.Second}

	got, err := p.Prepare(context.Background(), []string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plans) != 1 || got.Plans[0].Prev != prev || got.Plans[0].Diff != "+change\n" {
		t.Fatalf("plans = %+v", got.Plans)
	}
	if strings.Join(got.RepoDeps, ",") != "libfoo" || !got.HasAudit || got.AuditNote != "" {
		t.Fatalf("prepared = %+v", got)
	}
	if got.HighFindings() != 1 || got.Plans[0].Audit[0].Name != "app" {
		t.Fatalf("audit findings = %+v", got.Plans[0].Audit)
	}
	if len(got.Plans[0].Files) == 0 || got.Plans[0].Commit != strings.Repeat("a", 40) {
		t.Fatalf("plan = %+v", got.Plans[0])
	}
}

// TestPrepareNoAudit and the failures: a dead audit is a note, not a failure;
// a refusal and a git failure stop the plan.
func TestPrepareFailures(t *testing.T) {
	ctx := context.Background()
	s, p := plannerFixture(t)
	got, err := p.Prepare(ctx, []string{"app"})
	if err != nil || got.HasAudit || got.AuditNote != "" || got.HighFindings() != 0 {
		t.Fatalf("no audit: %+v, %v", got, err)
	}

	p.Audit = &audit.Config{RPCURL: "", SnapshotPath: filepath.Join(t.TempDir(), audit.SnapshotFile)}
	got, err = p.Prepare(ctx, []string{"app"})
	if err != nil || got.HasAudit || !strings.Contains(got.AuditNote, "audit could not run") {
		t.Fatalf("dead audit: note %q, err %v", got.AuditNote, err)
	}

	p.Audit = nil
	if _, err := p.Prepare(ctx, []string{"ghost"}); err == nil {
		t.Fatal("unknown package planned")
	} else if r, ok := IsRefusal(err); !ok || r.Reason != ReasonNotFound {
		t.Fatalf("err = %v", err)
	}

	s.put("fetch.rc", "128") // the checkout exists by now
	if _, err := p.Prepare(ctx, []string{"app"}); err == nil || !strings.Contains(err.Error(), "fetching app") {
		t.Fatalf("fetch failure: %v", err)
	}
	s.put("fetch.rc", "0")

	// the fetched .SRCINFO lists a dependency the index did not
	s.repoFile(".SRCINFO", strings.Replace(plainSrcinfo, "depends = libfoo", "depends = libfoo\n\tdepends = extra", 1))
	_, err = p.Prepare(ctx, []string{"app"})
	wantRefusal(t, err, ReasonSrcinfo, "extra")
}
