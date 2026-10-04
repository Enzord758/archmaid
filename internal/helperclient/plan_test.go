package helperclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The orphan removal under polkit: plan-orphans (a fake helper script, run
// directly) → the probe's removal step (its confirm lists) → Argv → a fake
// pkexec. Neither the real helper, pkexec nor pacman is ever reached.

const (
	startPlan   = `{"v":1,"t":"start","verb":"plan-orphans","version":"0.1.0"}`
	startRemove = `{"v":1,"t":"start","verb":"remove-orphans","version":"0.1.0"}`
)

var (
	goodDirect  = []string{"libfoo", "python-bar"}
	goodCascade = []string{"libbaz 1.0-1", "libfoo 2:3.1-2", "python-bar 0.9-1"}
)

func goodPlan() steps.OrphanPlan {
	return steps.OrphanPlan{Hash: PlanHash(goodDirect, goodCascade),
		Direct: slices.Clone(goodDirect), Cascade: slices.Clone(goodCascade)}
}

// planLine is the helper's plan event for targets/items, with hash as given
// (PlanHash of them when ""), and total len(items).
func planLine(t *testing.T, targets, items []string, hash string) string {
	t.Helper()
	if hash == "" {
		hash = PlanHash(targets, items)
	}
	b, err := json.Marshal(map[string]any{"v": 1, "t": "plan", "targets": targets, "items": items,
		"total": len(items), "hash": hash})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeHelper writes a stand-in for the helper's binary that records its
// argv and runs body, and returns its path and the argv file.
func fakeHelper(t *testing.T, body string) (path, argv string) {
	t.Helper()
	dir := t.TempDir()
	path, argv = filepath.Join(dir, "archmaid-helper"), filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, argv
}

func readArgv(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func plan(t *testing.T, helper string) (steps.OrphanPlan, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return planOrphans(ctx, helper)
}

// removal is the step Full Cleanup's polkit probe inserts for planner.
func removal(t *testing.T, planner func(context.Context) (steps.OrphanPlan, error)) steps.Step {
	t.Helper()
	a, _ := actions.ByID(actions.IDFullCleanup)
	env := actions.Env{Polkit: true, PlanOrphans: planner,
		LookPath:  func(string) (string, error) { return "", errors.New("none") },
		AuditStep: func() steps.Step { return steps.Step{ID: steps.IDAURAudit} }}
	for _, s := range a.Steps(env) {
		if s.ID == steps.IDOrphansProbe {
			res := s.Run(context.Background(), steps.Env{})
			if res.Err != nil || res.InsertNext == nil {
				t.Fatalf("probe result %+v", res)
			}
			return *res.InsertNext
		}
	}
	t.Fatal("no orphan probe")
	return steps.Step{}
}

// TestOrphanHashFlows: the hash the helper's plan-orphans printed is the one
// on the removal step whose confirm lists exactly the plan, and the one
// pkexec gets — unchanged — as remove-orphans' only argument. plan-orphans
// itself runs directly, not through pkexec.
func TestOrphanHashFlows(t *testing.T) {
	pkexecArgv := shim(t, lines("0", startRemove,
		`{"v":1,"t":"progress","step":1,"of":1,"label":"Removing 2 orphaned package(s)"}`,
		`{"v":1,"t":"line","stream":"out","text":"removing libfoo"}`,
		`{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}`))
	hash := PlanHash(goodDirect, goodCascade)
	helper, helperArgv := fakeHelper(t, lines("0", startPlan, planLine(t, goodDirect, goodCascade, hash), resultOK))

	st := removal(t, func(ctx context.Context) (steps.OrphanPlan, error) { return planOrphans(ctx, helper) })
	if got := readArgv(t, helperArgv); !slices.Equal(got, []string{"plan-orphans"}) {
		t.Fatalf("plan-orphans argv %q", got)
	}
	if _, err := os.Stat(pkexecArgv); !os.IsNotExist(err) {
		t.Fatal("plan-orphans went through pkexec")
	}
	// What the engine's ConfirmOrphans lists (Direct = Items, Cascade).
	if !slices.Equal(st.Items, goodDirect) || !slices.Equal(st.Cascade, goodCascade) || st.Plan == nil || st.Plan.Hash != hash {
		t.Fatalf("removal step %+v", st)
	}
	r := runStep(t, st)
	if ex := r.exited(t); ex.Err != nil {
		t.Fatalf("removal: %v", ex.Err)
	}
	want := []string{HelperPath, steps.VerbRemoveOrphans, hash}
	if got := readArgv(t, pkexecArgv); !slices.Equal(got, want) {
		t.Fatalf("pkexec argv %q, want %q", got, want)
	}
}

// TestPlanOrphansRefused: a plan-orphans output that is anything but one
// valid plan from a successful run is an error, so no removal is offered.
// The hostile names come with the hash of themselves, so only the name
// check can catch them.
func TestPlanOrphansRefused(t *testing.T) {
	hostile := func(direct ...string) string {
		return planLine(t, direct, []string{"libfoo 1-1"}, "")
	}
	cases := []struct{ name, body string }{
		{"hash not of the lists", lines("0", startPlan,
			planLine(t, goodDirect, goodCascade, PlanHash([]string{"libfoo"}, goodCascade)), resultOK)},
		{"hash uppercase", lines("0", startPlan,
			planLine(t, goodDirect, goodCascade, strings.ToUpper(PlanHash(goodDirect, goodCascade))), resultOK)},
		{"hash not sha256", lines("0", startPlan, planLine(t, goodDirect, goodCascade, "md5:abc"), resultOK)},
		{"flag name", lines("0", startPlan, hostile("--root=/x"), resultOK)},
		{"dash name", lines("0", startPlan, hostile("-Rdd"), resultOK)},
		{"shell name", lines("0", startPlan, hostile("a;rm"), resultOK)},
		{"space name", lines("0", startPlan, hostile("a b"), resultOK)},
		{"escape name", lines("0", startPlan, hostile("a\x1b[2Jb"), resultOK)},
		{"bidi name", lines("0", startPlan, hostile("a‮b"), resultOK)},
		{"path name", lines("0", startPlan, hostile("../etc"), resultOK)},
		{"empty name", lines("0", startPlan, hostile(""), resultOK)},
		{"name twice", lines("0", startPlan, hostile("libfoo", "libfoo"), resultOK)},
		{"bad cascade line", lines("0", startPlan, planLine(t, goodDirect, []string{"libfoo"}, ""), resultOK)},
		{"bad cascade version", lines("0", startPlan, planLine(t, goodDirect, []string{"libfoo 1 2"}, ""), resultOK)},
		{"total off", lines("0", startPlan,
			strings.Replace(planLine(t, goodDirect, goodCascade, ""), `"total":3`, `"total":2`, 1), resultOK)},
		{"no hash", lines("0", startPlan, `{"v":1,"t":"plan","targets":["a"],"items":["a 1-1"],"total":1}`, resultOK)},
		{"two plans", lines("0", startPlan, planLine(t, goodDirect, goodCascade, ""),
			planLine(t, goodDirect, goodCascade, ""), resultOK)},
		{"no plan", lines("0", startPlan, resultOK)},
		{"plan after result", lines("0", startPlan, resultOK, planLine(t, goodDirect, goodCascade, ""))},
		{"wrong verb", lines("0", startCheck, planLine(t, goodDirect, goodCascade, ""), resultOK)},
		{"helper failed", lines("1", startPlan,
			`{"v":1,"t":"result","ok":false,"code":1,"child_exit":null,"skipped":[],"error":"pacman -Qdtq exited 2"}`)},
		{"exit disagrees", lines("1", startPlan, planLine(t, goodDirect, goodCascade, ""), resultOK)},
		{"exit 127 silent", "exit 127"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			helper, _ := fakeHelper(t, c.body)
			p, err := plan(t, helper)
			if err == nil {
				t.Fatalf("plan %+v accepted", p)
			}
			if errors.Is(err, ErrNotGranted) {
				t.Errorf("%v: plan-orphans has no pkexec to refuse it", err)
			}
		})
	}
}

// TestPlanOrphansEmpty: no orphans is a valid, empty plan.
func TestPlanOrphansEmpty(t *testing.T) {
	helper, _ := fakeHelper(t, lines("0", startPlan, planLine(t, []string{}, []string{}, ""), resultOK))
	p, err := plan(t, helper)
	if err != nil || len(p.Direct) != 0 || len(p.Cascade) != 0 || p.Hash != PlanHash(nil, nil) {
		t.Fatalf("plan %+v, %v", p, err)
	}
}

// TestPlanOrphansMissingHelper: no helper installed is an error.
func TestPlanOrphansMissingHelper(t *testing.T) {
	if _, err := plan(t, filepath.Join(t.TempDir(), "none")); err == nil {
		t.Fatal("a missing helper gave a plan")
	}
}

// TestArgvRemoveOrphans: a removal step's argv is its plan's hash, and only
// a plan that is valid, removes something and is exactly what the confirm
// lists gets one. Each refusal comes before pkexec could start.
func TestArgvRemoveOrphans(t *testing.T) {
	good := removal(t, func(context.Context) (steps.OrphanPlan, error) { return goodPlan(), nil })
	got, err := Argv(good)
	if want := []string{Pkexec, HelperPath, steps.VerbRemoveOrphans, PlanHash(goodDirect, goodCascade)}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Argv = %q, %v; want %q", got, err, want)
	}

	with := func(f func(st *steps.Step)) steps.Step {
		st := good
		p := goodPlan()
		st.Plan, st.Items, st.Cascade = &p, slices.Clone(goodDirect), slices.Clone(goodCascade)
		f(&st)
		return st
	}
	hostile := func(names ...string) func(st *steps.Step) {
		return func(st *steps.Step) {
			*st.Plan = steps.OrphanPlan{Hash: PlanHash(names, goodCascade), Direct: names, Cascade: goodCascade}
			st.Items = names
		}
	}
	otherHash := PlanHash([]string{"libfoo"}, goodCascade)
	cases := []struct {
		name string
		st   steps.Step
		want error
	}{
		{"no plan", with(func(st *steps.Step) { st.Plan = nil }), ErrUnsupported},
		{"tampered hash", with(func(st *steps.Step) { st.Plan.Hash = otherHash }), ErrBadPlan},
		{"hash one digit off", with(func(st *steps.Step) {
			h := []byte(st.Plan.Hash)
			h[len(h)-1] ^= 1
			st.Plan.Hash = string(h)
		}), ErrBadPlan},
		{"hash uppercase", with(func(st *steps.Step) { st.Plan.Hash = strings.ToUpper(st.Plan.Hash) }), ErrBadPlan},
		{"hash newline", with(func(st *steps.Step) { st.Plan.Hash += "\n" }), ErrBadPlan},
		{"hash flag", with(func(st *steps.Step) { st.Plan.Hash = "--help" }), ErrBadPlan},
		{"hash empty", with(func(st *steps.Step) { st.Plan.Hash = "" }), ErrBadPlan},
		{"hash short", with(func(st *steps.Step) { st.Plan.Hash = st.Plan.Hash[:70] }), ErrBadPlan},
		{"hash plus arg", with(func(st *steps.Step) { st.Plan.Hash += " --root=/" }), ErrBadPlan},
		{"confirm showed fewer", with(func(st *steps.Step) { st.Items = st.Items[:1] }), ErrBadPlan},
		{"confirm showed other", with(func(st *steps.Step) { st.Items = []string{"libfoo", "glibc"} }), ErrBadPlan},
		{"confirm showed no cascade", with(func(st *steps.Step) { st.Cascade = nil }), ErrBadPlan},
		{"plan grew after confirm", with(func(st *steps.Step) {
			st.Plan.Direct = append(st.Plan.Direct, "glibc")
			st.Plan.Hash = PlanHash(st.Plan.Direct, st.Plan.Cascade)
		}), ErrBadPlan},
		{"flag name", with(hostile("--root=/x")), ErrBadPlan},
		{"dash name", with(hostile("-Rdd")), ErrBadPlan},
		{"escape name", with(hostile("a\x1b]0;x\x07")), ErrBadPlan},
		{"bidi name", with(hostile("a‮b")), ErrBadPlan},
		{"name twice", with(hostile("libfoo", "libfoo")), ErrBadPlan},
		{"hostile cascade", with(func(st *steps.Step) {
			c := []string{"libfoo 1-1; rm -rf /"}
			*st.Plan = steps.OrphanPlan{Hash: PlanHash(goodDirect, c), Direct: goodDirect, Cascade: c}
			st.Cascade = c
		}), ErrBadPlan},
		{"removes nothing", with(func(st *steps.Step) {
			*st.Plan = steps.OrphanPlan{Hash: PlanHash(nil, nil)}
			st.Items, st.Cascade = nil, nil
		}), ErrBadPlan},
		{"plan on another verb", steps.Step{Verb: steps.VerbRepair, Items: []string{"a"}, Plan: good.Plan}, ErrBadPlan},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := Argv(c.st); !errors.Is(err, c.want) || got != nil {
				t.Fatalf("Argv = %q, %v; want %v", got, err, c.want)
			}
			argvFile := shim(t, "exit 0")
			r := runStep(t, c.st)
			if ex := r.exited(t); !errors.Is(ex.Err, c.want) || len(r.inputs) != 1 {
				t.Fatalf("Run inputs %#v", r.inputs)
			}
			if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
				t.Fatal("pkexec ran")
			}
		})
	}
}

// staleRun is what remove-orphans prints when the plan it recomputes as
// root no longer has the confirmed hash (helper.go removeOrphans).
func staleRun(t *testing.T, childExit string) string {
	return lines("3", startRemove,
		`{"v":1,"t":"progress","step":1,"of":1,"label":"Orphan removal skipped: the plan changed since confirm"}`,
		planLine(t, []string{"libfoo"}, []string{"libfoo 2:3.1-2"}, ""),
		`{"v":1,"t":"result","ok":false,"code":3,"child_exit":`+childExit+`,"skipped":[{"name":"remove-orphans","reason":"the orphan plan changed since it was confirmed"}],"error":"orphan plan changed since confirm; nothing removed"}`)
}

// TestRemoveOrphansStale: the helper found the system changed between the
// confirm and the removal (exit 3, no root command run): the step fails
// with ErrOrphansChanged's message, and the skip is in its output.
func TestRemoveOrphansStale(t *testing.T) {
	st := removal(t, func(context.Context) (steps.OrphanPlan, error) { return goodPlan(), nil })
	shim(t, staleRun(t, "null"))
	r := runStep(t, st)
	ex := r.exited(t)
	if !errors.Is(ex.Err, ErrOrphansChanged) || ex.NotGranted || r.out.Granted != true {
		t.Fatalf("exited %+v, outcome %+v", ex, r.out)
	}
	if ex.Err.Error() != "The orphan list changed since you confirmed; nothing was removed" {
		t.Errorf("message %q", ex.Err)
	}
	if out := r.output(); !slices.Contains(out, "Skipped remove-orphans: the orphan plan changed since it was confirmed") {
		t.Errorf("output %q", out)
	}
	if n := slices.IndexFunc(r.inputs, func(in engine.Input) bool { _, ok := in.(engine.HelperCheck); return ok }); n >= 0 {
		t.Error("a HelperCheck from remove-orphans")
	}
}

// TestRemoveOrphansStaleOnlyWhenNothingRan: an exit 3 after a root command
// ran, or from another verb, is not "nothing was removed".
func TestRemoveOrphansStaleOnlyWhenNothingRan(t *testing.T) {
	st := removal(t, func(context.Context) (steps.OrphanPlan, error) { return goodPlan(), nil })
	shim(t, staleRun(t, "0"))
	if ex := runStep(t, st).exited(t); ex.Err == nil || errors.Is(ex.Err, ErrOrphansChanged) {
		t.Fatalf("child_exit 0: %v", ex.Err)
	}
	shim(t, lines("3", `{"v":1,"t":"start","verb":"repair","version":"0.1.0"}`,
		`{"v":1,"t":"result","ok":false,"code":3,"child_exit":null,"skipped":[],"error":"stale"}`))
	if ex := runStep(t, actions.RepairStep([]string{"a"})).exited(t); ex.Err == nil || errors.Is(ex.Err, ErrOrphansChanged) {
		t.Fatalf("repair exit 3: %v", ex.Err)
	}
}

// TestParsePlan: the exported parser is PlanOrphans' own.
func TestParsePlan(t *testing.T) {
	in := startPlan + "\n" + planLine(t, goodDirect, goodCascade, "") + "\n" + resultOK + "\n"
	p, err := ParsePlan(strings.NewReader(in))
	if err != nil || p.Hash != goodPlan().Hash || !slices.Equal(p.Direct, goodDirect) || !slices.Equal(p.Cascade, goodCascade) {
		t.Fatalf("ParsePlan = %+v, %v", p, err)
	}
	if _, err := ParsePlan(strings.NewReader(startPlan + "\n" + resultOK + "\n")); !errors.Is(err, ErrProtocol) {
		t.Fatalf("no plan: %v", err)
	}
}
