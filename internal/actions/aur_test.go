package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The AUR install's discovery (aur.go): the AUR planner's answer and the
// helper's plan of the repository dependencies become one sheet and one queue.

var aurPlans = []steps.AURPlan{{Base: "app", Names: []string{"app"}, Commit: strings.Repeat("a", 40)}}

func aurReq() steps.PkgRequest {
	return steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"app"}, AUR: true}
}

func aurEnv(prep AURPrepared, perr error, calls *[]string) Env {
	return Env{Polkit: true,
		PlanAUR: func(_ context.Context, names []string) (AURPrepared, error) {
			*calls = append(*calls, "aur:"+strings.Join(names, ","))
			return prep, perr
		},
		PlanPackages: func(_ context.Context, r steps.PkgRequest) (steps.PkgPlan, error) {
			*calls = append(*calls, fmt.Sprintf("repo:%s asdeps=%v", strings.Join(r.Names, ","), r.AsDeps))
			p := tPlan(steps.PkgInstall)
			p.Targets, p.AsDeps = r.Names, r.AsDeps
			return p, nil
		}}
}

func queueOf(got *[]*steps.Step) func(*steps.Step) []steps.Step {
	return func(deps *steps.Step) []steps.Step {
		*got = append(*got, deps)
		q := []steps.Step{{ID: "aur-build:app"}}
		if deps != nil {
			q = append([]steps.Step{*deps}, q...)
		}
		return q
	}
}

func TestAURDiscoverWithAndWithoutRepoDeps(t *testing.T) {
	var calls []string
	var deps []*steps.Step
	// no repository dependencies: the helper is not asked
	env := aurEnv(AURPrepared{Plans: aurPlans, Queue: queueOf(&deps), Notes: []string{"audit\nnote"}}, nil, &calls)
	pv, ok := discover(env, aurReq()).(PkgPreview)
	if !ok || len(pv.Steps) != 1 || deps[0] != nil {
		t.Fatalf("no deps: %+v, deps %v", pv, deps)
	}
	if strings.Join(calls, "|") != "aur:app" {
		t.Fatalf("calls = %v", calls)
	}
	if len(pv.Plan.AUR) != 1 || pv.Plan.AUR[0].Base != "app" || len(pv.Plan.Rows) != 0 || pv.Plan.Mode != steps.PkgInstall || strings.Join(pv.Plan.Warnings, "|") != "audit note" {
		t.Fatalf("plan = %+v", pv.Plan)
	}

	// repository dependencies: planned with --asdeps, their root step comes first,
	// the sheet lists the AUR names as the targets
	calls, deps = nil, nil
	env = aurEnv(AURPrepared{Plans: aurPlans, RepoDeps: []string{"libfoo"}, Queue: queueOf(&deps)}, nil, &calls)
	pv, ok = discover(env, aurReq()).(PkgPreview)
	if !ok || strings.Join(calls, "|") != "aur:app|repo:libfoo asdeps=true" {
		t.Fatalf("calls = %v (%v)", calls, ok)
	}
	if len(pv.Steps) != 2 || pv.Steps[0].ID != steps.IDPkgInstall || pv.Steps[0].PkgPlan == nil || !pv.Steps[0].PkgPlan.AsDeps || deps[0] == nil {
		t.Fatalf("steps = %+v", pv.Steps)
	}
	if fmt.Sprint(pv.Plan.Targets) != "[app]" || len(pv.Plan.Rows) != 1 || len(pv.Plan.AUR) != 1 || fmt.Sprint(pv.Steps[0].Items) != "[libfoo]" {
		t.Fatalf("plan targets %v rows %v; root step items %v", pv.Plan.Targets, pv.Plan.Rows, pv.Steps[0].Items)
	}
}

func TestAURDiscoverFailures(t *testing.T) {
	var calls []string
	var deps []*steps.Step
	refusal := &steps.PkgRefusal{Reason: steps.RefuseAUR, Detail: "cycle"}
	// the AUR planner refused
	if r, ok := discover(aurEnv(AURPrepared{}, refusal, &calls), aurReq()).(PkgRefused); !ok || r.Refusal != refusal || !r.Req.AUR {
		t.Fatalf("planner refusal = %+v", r)
	}
	// it failed
	n, ok := discover(aurEnv(AURPrepared{}, errors.New("network\x1b[31m down"), &calls), aurReq()).(Notice)
	if !ok || n.Err != "network down" {
		t.Fatalf("planner error = %+v", n)
	}
	// nothing to do
	if n, ok := discover(aurEnv(AURPrepared{}, nil, &calls), aurReq()).(Notice); !ok || n.Err != "" {
		t.Fatalf("empty = %+v", n)
	}
	// the helper refuses the repository dependencies (updates pending)
	upd := &steps.PkgRefusal{Reason: steps.RefuseUpdatesPending, Count: 3}
	env := aurEnv(AURPrepared{Plans: aurPlans, RepoDeps: []string{"libfoo"}, Queue: queueOf(&deps)}, nil, &calls)
	env.PlanPackages = func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) { return steps.PkgPlan{}, upd }
	if r, ok := discover(env, aurReq()).(PkgRefused); !ok || r.Refusal != upd {
		t.Fatalf("helper refusal = %+v", r)
	}
	env.PlanPackages = func(context.Context, steps.PkgRequest) (steps.PkgPlan, error) {
		return steps.PkgPlan{}, errors.New("helper down")
	}
	if n, ok := discover(env, aurReq()).(Notice); !ok || n.Err != "helper down" {
		t.Fatalf("helper error = %+v", n)
	}
	// no polkit, no planner, no helper plan: a notice, nothing called
	for name, e := range map[string]Env{
		"sudo":        {PlanAUR: func(context.Context, []string) (AURPrepared, error) { panic("called") }, PlanPackages: env.PlanPackages},
		"no PlanAUR":  {Polkit: true, PlanPackages: env.PlanPackages},
		"no PlanPkgs": {Polkit: true, PlanAUR: func(context.Context, []string) (AURPrepared, error) { panic("called") }},
	} {
		if n, ok := discover(e, aurReq()).(Notice); !ok || !strings.Contains(n.Err, "root helper") {
			t.Errorf("%s: %+v", name, n)
		}
	}
}
