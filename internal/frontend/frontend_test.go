package frontend

import (
	"context"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestGlueEnv: Env fills in the glue and keeps the front end's own fields;
// the menu audit's config is read once, here, the step's per queue; the
// delete step reads base's Downloads when it has one and records an abort
// in Store.
func TestGlueEnv(t *testing.T) {
	store := testStore(t, false)
	reads := 0
	g := Glue{
		AuditConfig: func() audit.Config {
			reads++
			return AuditConfig(store, AuditOptions{Foreign: foreign()})
		},
		Render:    func(audit.Result) string { return "drawn" },
		Downloads: func() downloads.Options { t.Fatal("Glue.Downloads read, base has its own"); return downloads.Options{} },
		Store:     store,
		Deletions: &Deletions{},
	}
	plan, opts := oldFiles(t, "a.txt")
	lookPath := func(string) (string, error) { return "", nil }
	e := g.Env(actions.Env{DryRun: true, LookPath: lookPath, RepairWorkers: 3, Downloads: opts})
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if !e.DryRun || e.LookPath == nil || e.RepairWorkers != 3 || e.Polkit {
		t.Fatalf("the front end's own fields changed: %+v", e)
	}
	if reads != 1 {
		t.Fatalf("Env read the audit config %d times, want 1 (the menu audit's)", reads)
	}
	if s := e.AuditStep(); s.ID != steps.IDAURAudit || reads != 2 {
		t.Fatalf("AuditStep = %q, config read %d times", s.ID, reads)
	}
	if n, ok := e.AuditReport(context.Background()).(actions.Notice); !ok || !strings.Contains(n.Text, "No foreign") {
		t.Fatalf("AuditReport = %#v", n)
	}

	ctx, abort := context.WithCancelCause(context.Background())
	e.DeleteDownloads(plan)(ctx, steps.Env{})
	abort(nil)
	g.Deletions.Wait()
	if log := cleanupLog(store); !strings.Contains(log, "deleted: a.txt") {
		t.Fatalf("the abort record isn't in Store:\n%s", log)
	}
}

// TestGlueEnvDownloads: without base Downloads, Env uses Glue's, and the
// delete step reads it too.
func TestGlueEnvDownloads(t *testing.T) {
	plan, opts := oldFiles(t, "a.txt")
	reads := 0
	g := Glue{
		AuditConfig: func() audit.Config { return AuditConfig(testStore(t, false), AuditOptions{}) },
		Downloads:   func() downloads.Options { reads++; return opts() },
		Store:       testStore(t, false),
		Deletions:   &Deletions{},
	}
	e := g.Env(actions.Env{})
	e.Downloads()
	if r := e.DeleteDownloads(plan)(context.Background(), steps.Env{}); !strings.Contains(r.Output, "deleted: a.txt") {
		t.Fatalf("delete: %+v", r)
	}
	if reads != 2 {
		t.Fatalf("Glue.Downloads read %d times, want 2", reads)
	}
}
