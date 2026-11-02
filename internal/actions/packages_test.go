package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages run's discovery and queue (packages.go): the helper's plan
// becomes the sheet and one root step; a refusal or a failure is never a
// queue.

func tPlan(mode steps.PkgMode) steps.PkgPlan {
	return steps.PkgPlan{Mode: mode, Targets: []string{"foo"}, Hash: "sha256:" + strings.Repeat("ab", 32),
		Rows: []steps.PkgRow{{Name: "foo", Version: "1-1", Repo: "extra", Target: true}}}
}

func discover(env Env, req steps.PkgRequest) Discovery {
	return PackagesDiscover(req)(context.Background(), env)
}

func TestPackagesDiscoverPreview(t *testing.T) {
	for _, mode := range []steps.PkgMode{steps.PkgInstall, steps.PkgRemove} {
		var asked steps.PkgRequest
		env := Env{Polkit: true, PlanPackages: func(_ context.Context, r steps.PkgRequest) (steps.PkgPlan, error) {
			asked = r
			return tPlan(mode), nil
		}}
		req := steps.PkgRequest{Mode: mode, Names: []string{"foo"}}
		pv, ok := discover(env, req).(PkgPreview)
		if !ok {
			t.Fatalf("mode %d: not a PkgPreview", mode)
		}
		if asked.Names[0] != "foo" || asked.Mode != mode {
			t.Errorf("asked %+v", asked)
		}
		if len(pv.Steps) != 1 {
			t.Fatalf("steps = %+v", pv.Steps)
		}
		st := pv.Steps[0]
		wantVerb, wantID := steps.PkgVerbInstall, steps.IDPkgInstall
		if mode == steps.PkgRemove {
			wantVerb, wantID = steps.PkgVerbRemove, steps.IDPkgRemove
		}
		if st.Verb != wantVerb || st.ID != wantID || !st.NeedsPriv || !st.AlwaysLog || st.PkgPlan == nil ||
			st.PkgPlan.Hash != pv.Plan.Hash || fmt.Sprint(st.Items) != "[foo]" || st.Argv != nil || st.Run != nil {
			t.Errorf("step = %+v", st)
		}
	}
}

func TestPackagesDiscoverRefusal(t *testing.T) {
	ref := &steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}, Detail: "x"}
	env := Env{Polkit: true, PlanPackages: func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) {
		return steps.PkgPlan{}, fmt.Errorf("plan-install: %w", ref) // wrapped is still a refusal
	}}
	d, ok := discover(env, steps.PkgRequest{Names: []string{"gvim"}}).(PkgRefused)
	if !ok || d.Refusal != ref || d.Req.Names[0] != "gvim" {
		t.Errorf("discovery = %#v", d)
	}
}

func TestPackagesDiscoverNotices(t *testing.T) {
	boom := Env{Polkit: true, PlanPackages: func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) {
		return steps.PkgPlan{}, errors.New("the root helper is not installed\x1b[31m")
	}}
	empty := Env{Polkit: true, PlanPackages: func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) {
		return steps.PkgPlan{Targets: []string{"foo"}}, nil
	}}
	for name, tc := range map[string]struct {
		env     Env
		wantErr string
	}{
		"plan failed":      {boom, "not installed"},
		"nothing to do":    {empty, ""},
		"no planner":       {Env{Polkit: true}, "polkit"},
		"sudo, not polkit": {Env{PlanPackages: boom.PlanPackages}, "polkit"},
	} {
		t.Run(name, func(t *testing.T) {
			n, ok := discover(tc.env, steps.PkgRequest{Names: []string{"foo"}}).(Notice)
			if !ok || n.Text == "" || !strings.Contains(n.Err, tc.wantErr) || strings.Contains(n.Err, "\x1b") {
				t.Errorf("discovery = %#v", n)
			}
			if tc.wantErr != "" && n.Err == "" {
				t.Error("a failed look has no Err")
			}
		})
	}
}

// TestPackagesNeverBuildsAnArgv: the queue carries a verb and a plan, never
// a command line, so nothing but the helper's own argv builder (checked
// against the plan) reaches pkexec.
func TestPackagesNeverBuildsAnArgv(t *testing.T) {
	for _, mode := range []steps.PkgMode{steps.PkgInstall, steps.PkgRemove} {
		for _, st := range Packages(tPlan(mode)) {
			if len(st.Argv) != 0 || st.Run != nil || st.Verb == "" {
				t.Errorf("step %+v", st)
			}
		}
	}
}
