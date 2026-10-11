package main

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestClientPlanHashIsTheHelpers: the hash the client checks a plan with is
// the one the helper computes, whatever the order.
func TestClientPlanHashIsTheHelpers(t *testing.T) {
	for _, c := range []struct{ direct, cascade []string }{
		{nil, nil},
		{[]string{"a"}, []string{"a 1-1"}},
		{[]string{"python-bar", "libfoo"}, []string{"libfoo 2:3.1-2", "libbaz 1.0-1", "python-bar 0.9-1"}},
	} {
		if c, h := helperclient.PlanHash(c.direct, c.cascade), helper.PlanHash(c.direct, c.cascade); c != h {
			t.Errorf("client %s, helper %s", c, h)
		}
	}
}

// orphanSystem is a fake pacman for the orphan verbs: -Qdtq lists orphans,
// -Rs --print prints cascade, and every Stream (a root command) is recorded.
type orphanSystem struct {
	orphans, cascade []string
	streamed         [][]string
}

func (s *orphanSystem) Query(argv []string) (string, string, int, error) {
	switch {
	case slices.Equal(argv, []string{helper.Pacman, "-Qdtq"}):
		if len(s.orphans) == 0 {
			return "", "", 1, nil
		}
		return strings.Join(s.orphans, "\n") + "\n", "", 0, nil
	case len(argv) > 3 && argv[1] == "-Rs" && argv[2] == "--print":
		return strings.Join(s.cascade, "\n") + "\n", "", 0, nil
	}
	return "", "unexpected query", 2, nil
}

func (s *orphanSystem) Stream(argv []string, _ func(stream, text string)) (int, error) {
	s.streamed = append(s.streamed, slices.Clone(argv))
	return 0, nil
}

// runHelper runs the helper in-process as a dev build (no root) on sys.
func runHelper(t *testing.T, sys *orphanSystem, args []string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	code := helper.Run(args, helper.Options{
		Runner: sys, Stdout: &out, Stderr: &errOut, Euid: 1000, Dev: true,
		LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
	})
	return code, out.String()
}

// TestOrphanRemovalEndToEnd: the helper's own plan-orphans output, read by
// the client's parser, becomes the polkit removal step whose confirm lists
// exactly that plan; the argv the client builds for it is one the helper's
// remove-orphans accepts on the same system and removes exactly the
// orphans with; on a system that changed since, the helper removes nothing
// and exits ExitStale.
func TestOrphanRemovalEndToEnd(t *testing.T) {
	sys := &orphanSystem{orphans: []string{"python-bar", "libfoo"},
		cascade: []string{"libfoo 2:3.1-2", "libbaz 1.0-1", "python-bar 0.9-1"}}
	code, out := runHelper(t, sys, []string{steps.VerbPlanOrphans})
	if code != helper.ExitOK {
		t.Fatalf("plan-orphans exited %d: %s", code, out)
	}
	plan, err := helperclient.ParsePlan(strings.NewReader(out))
	if err != nil {
		t.Fatalf("the client refused the helper's plan: %v\n%s", err, out)
	}

	a, _ := actions.ByID(actions.IDFullCleanup)
	env := actions.Env{Polkit: true,
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) { return plan, nil },
		LookPath:    func(string) (string, error) { return "/usr/bin/x", nil },
		AuditStep:   func() steps.Step { return steps.Step{ID: steps.IDAURAudit} }}
	var st *steps.Step
	for _, s := range a.Steps(env) {
		if s.ID == steps.IDOrphansProbe {
			st = s.Run(context.Background(), steps.Env{}).InsertNext
		}
	}
	if st == nil {
		t.Fatal("the polkit probe inserted no removal")
	}
	if !slices.Equal(st.Items, []string{"libfoo", "python-bar"}) || len(st.Cascade) != 3 {
		t.Fatalf("confirm lists %q / %q", st.Items, st.Cascade)
	}
	argv, err := helperclient.Argv(*st)
	if err != nil {
		t.Fatal(err)
	}
	args := helperArgs(t, argv)
	if code, out := runHelper(t, sys, args); code != helper.ExitOK {
		t.Fatalf("remove-orphans %q exited %d: %s", args, code, out)
	}
	want := [][]string{{helper.Pacman, "-Rns", "--noconfirm", "--", "libfoo", "python-bar"}}
	if !slices.EqualFunc(sys.streamed, want, slices.Equal) {
		t.Fatalf("root commands %q, want %q", sys.streamed, want)
	}

	// Between the confirm and the removal a package became an orphan.
	sys.streamed = nil
	sys.orphans = append(sys.orphans, "glibc")
	sys.cascade = append(sys.cascade, "glibc 2.40-1")
	code, out = runHelper(t, sys, args)
	if code != helper.ExitStale || len(sys.streamed) != 0 {
		t.Fatalf("stale plan: exit %d, root commands %q: %s", code, sys.streamed, out)
	}
	if !strings.Contains(out, `"child_exit":null`) {
		t.Errorf("stale result %s: the client reads child_exit null as nothing removed", out)
	}
}
