package overview

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

func TestCards(t *testing.T) {
	var ids []string
	for _, c := range Cards() {
		ids = append(ids, c.ID)
		if c.Desc == "" || c.Label == "" || c.Icon == "" {
			t.Errorf("%s: desc %q label %q icon %q", c.ID, c.Desc, c.Label, c.Icon)
		}
		if c.Kind != KindRun {
			t.Errorf("%s: kind %v, want a run", c.ID, c.Kind)
		}
	}
	want := []string{actions.IDFullCleanup, actions.IDUserCache, actions.IDRepair, actions.IDDownloads, actions.IDWipeCache,
		actions.IDTopgrade, actions.IDServices, actions.IDAURAudit}
	if !slices.Equal(ids, want) {
		t.Errorf("cards %q, want %q (the menu without View log, Edit config and Exit)", ids, want)
	}
}

// previewEnv is the Env a run would get, with every func a run needs; the
// orphan plan and the lookups fail the test if the preview calls them.
func previewEnv(t *testing.T, polkit, hyprpm bool) actions.Env {
	return actions.Env{
		Polkit: polkit,
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) {
			t.Error("the preview planned the orphans")
			return steps.OrphanPlan{}, errors.New("no")
		},
		LookPath: func(file string) (string, error) {
			if file == "hyprpm" && hyprpm {
				return "/usr/bin/hyprpm", nil
			}
			return "", errors.New("not found")
		},
		AuditStep: func() steps.Step {
			return steps.Step{ID: steps.IDAURAudit, Title: "Auditing AUR packages…", Run: func(context.Context, steps.Env) steps.Result {
				t.Error("the preview ran the audit")
				return steps.Result{}
			}}
		},
	}
}

func titles(p Preview) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Title)
	}
	return out
}

func TestPlanPreviewFullCleanupPolkit(t *testing.T) {
	p, ok := PlanPreview(previewEnv(t, true, true), actions.IDFullCleanup)
	if !ok {
		t.Fatal("no preview for Full Cleanup")
	}
	want := []string{"Auditing AUR packages…", "Updating system with topgrade…", "Finding orphaned packages…",
		"Cleaning package cache…", "Removing uninstalled packages from cache…",
		"Cleaning old temp files…", "Vacuuming logs…", "Checking package integrity…", "Cleaning AUR cache…",
		"Updating Hyprland plugins…"}
	if got := titles(p); !slices.Equal(got, want) {
		t.Fatalf("plan %q\nwant %q", got, want)
	}
	// The five steps of the one helper call are root (the cache wipe is not
	// one of them: it is its own menu action); topgrade gets root through
	// its own sudo, tagged sudo (the gate's "5 steps run as root" beside 5
	// root tags, not 6).
	if p.Root != 5 || p.Sudo != 1 || !p.Orphans {
		t.Errorf("root %d sudo %d orphans %v, want 5, 1 and true", p.Root, p.Sudo, p.Orphans)
	}
	if s := p.Steps[1]; s.Title != "Updating system with topgrade…" || s.Root || !s.Sudo {
		t.Errorf("topgrade previewed as %+v, want sudo, not root", s)
	}
	combined, asks := 0, []string{}
	for _, s := range p.Steps {
		if s.Combined {
			combined++
			if !s.Root {
				t.Errorf("%q is combined but not root", s.Title)
			}
		}
		if s.Asks {
			asks = append(asks, s.Title)
		}
	}
	if combined != 5 || len(asks) != 0 {
		t.Errorf("combined %d asks %q", combined, asks)
	}
}

func TestPlanPreviewFullCleanupSudo(t *testing.T) {
	p, ok := PlanPreview(previewEnv(t, false, false), actions.IDFullCleanup)
	if !ok {
		t.Fatal("no preview")
	}
	if n := len(p.Steps); n != 8 || p.Steps[n-1].Title != "Checking package integrity…" {
		t.Errorf("sudo plan without hyprpm: %q", titles(p))
	}
	for _, s := range p.Steps {
		if s.Combined {
			t.Errorf("%q combined outside polkit", s.Title)
		}
	}
	if p.Root != 4 || p.Sudo != 1 || !p.Orphans {
		t.Errorf("root %d sudo %d orphans %v, want 4, 1 and true", p.Root, p.Sudo, p.Orphans)
	}
}

func TestPlanPreviewOthers(t *testing.T) {
	env := previewEnv(t, true, false)
	p, ok := PlanPreview(env, actions.IDTopgrade)
	if !ok || !slices.Equal(titles(p), []string{"Auditing AUR packages…", "Updating system with topgrade…"}) || p.Root != 0 || p.Sudo != 1 || p.Orphans {
		t.Errorf("topgrade: ok %v %+v", ok, p)
	}
	if p, ok := PlanPreview(env, actions.IDUserCache); !ok || len(p.Steps) != 1 || p.Root != 0 || !p.Steps[0].Asks {
		t.Errorf("user cache: ok %v %+v", ok, p)
	}
	for _, id := range []string{actions.IDRepair, actions.IDDownloads, actions.IDWipeCache, actions.IDServices, actions.IDAURAudit,
		actions.IDViewLog, actions.IDEditConfig, "nope"} {
		if _, ok := PlanPreview(env, id); ok {
			t.Errorf("%s: a preview for an action without a static queue", id)
		}
	}
}

// The cards' static root flag agrees with the plan for every action that
// has one.
func TestCardsRootMatchesPlan(t *testing.T) {
	env := previewEnv(t, true, false)
	for _, c := range Cards() {
		p, ok := PlanPreview(env, c.ID)
		if !ok {
			continue
		}
		if c.Root != (p.Root+p.Sudo > 0) {
			t.Errorf("%s: card root %v, plan root %d sudo %d", c.ID, c.Root, p.Root, p.Sudo)
		}
		// The cell's tag text is the plan's: root when it has a helper
		// step, sudo when its privileged steps are all sudo-only.
		want := ""
		switch {
		case p.Root > 0:
			want = "root"
		case p.Sudo > 0:
			want = "sudo"
		}
		if c.Tag != want {
			t.Errorf("%s: card tag %q, plan root %d sudo %d wants %q", c.ID, c.Tag, p.Root, p.Sudo, want)
		}
	}
}
