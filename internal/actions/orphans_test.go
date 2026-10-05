package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The orphan probe under polkit: the removal is built from the root
// helper's plan (Env.PlanOrphans), never from pacman's text.

var testPlan = steps.OrphanPlan{
	Hash:    "sha256:" + strings.Repeat("ab", 32),
	Direct:  []string{"libfoo", "python-bar"},
	Cascade: []string{"libbaz 1.0-1", "libfoo 2:3.1-2", "python-bar 0.9-1"},
}

// probe is Full Cleanup's orphan probe as env builds it.
func probe(t *testing.T, env Env) steps.Step {
	t.Helper()
	a, _ := ByID(IDFullCleanup)
	for _, s := range a.Steps(env) {
		if s.ID == steps.IDOrphansProbe {
			return s
		}
	}
	t.Fatal("Full Cleanup has no orphan probe")
	return steps.Step{}
}

// noPacman puts only a pacman that fails the test first on PATH: under
// polkit the probe must not run it.
func noPacman(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if _, err := os.Stat(marker); err == nil {
			t.Error("the polkit probe ran pacman")
		}
	})
}

// TestOrphanProbePolkit: the inserted removal lists exactly the plan's
// Direct and Cascade, carries its hash in Plan, and names no sudo.
func TestOrphanProbePolkit(t *testing.T) {
	noPacman(t)
	env := testEnv(false)
	env.Polkit = true
	plan := testPlan
	plan.Direct, plan.Cascade = slices.Clone(testPlan.Direct), slices.Clone(testPlan.Cascade)
	calls := 0
	env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { calls++; return plan, nil }
	p := probe(t, env)
	if p.Verb != steps.VerbPlanOrphans || p.Run == nil || p.NeedsPriv {
		t.Fatalf("probe %+v: want an in-process plan-orphans step without root", p)
	}
	if calls != 0 {
		t.Fatal("building the queue ran plan-orphans")
	}
	res := p.Run(context.Background(), steps.Env{})
	if res.Err != nil || res.InsertNext == nil || calls != 1 {
		t.Fatalf("result %+v after %d calls", res, calls)
	}
	st := *res.InsertNext
	if st.ID != steps.IDOrphansRemove || st.Verb != steps.VerbRemoveOrphans || !st.NeedsPriv || !st.Optional || st.Run != nil {
		t.Errorf("removal %+v", st)
	}
	if !slices.Equal(st.Items, testPlan.Direct) || !slices.Equal(st.Cascade, testPlan.Cascade) {
		t.Errorf("confirm lists %q / %q, want the plan's", st.Items, st.Cascade)
	}
	if st.Plan == nil || st.Plan.Hash != testPlan.Hash ||
		!slices.Equal(st.Plan.Direct, testPlan.Direct) || !slices.Equal(st.Plan.Cascade, testPlan.Cascade) {
		t.Errorf("Plan %+v, want %+v", st.Plan, testPlan)
	}
	if want := "Remove 2 orphaned package(s) — 3 total once dependencies cascade?"; st.Confirm != want {
		t.Errorf("Confirm %q, want %q", st.Confirm, want)
	}
	if slices.Contains(st.Argv, "sudo") || !slices.Equal(st.Argv, []string{"pacman", "-Rns", "--noconfirm", "--", "libfoo", "python-bar"}) {
		t.Errorf("Argv %q", st.Argv)
	}
	if !strings.HasPrefix(res.Output, "2 orphaned package(s): libfoo python-bar\npacman -Rns would remove 3 package(s):\n  libbaz 1.0-1") {
		t.Errorf("Output %q", res.Output)
	}
	// The step owns its lists: changing the planner's slices, or the
	// step's Items, leaves the Plan as it was.
	plan.Direct[0], plan.Cascade[0] = "evil", "evil 1-1"
	st.Items[1] = "evil"
	if st.Items[0] != "libfoo" || st.Cascade[0] != "libbaz 1.0-1" || st.Plan.Direct[1] != "python-bar" {
		t.Errorf("the step shares its lists: %q %q %q", st.Items, st.Cascade, st.Plan.Direct)
	}
}

// TestOrphanProbePolkitInsertsNothing: no orphans, a failed plan or no
// planner at all insert no removal; the last two fail the probe.
func TestOrphanProbePolkitInsertsNothing(t *testing.T) {
	noPacman(t)
	boom := errors.New("plan-orphans: helper protocol error: boom")
	for _, c := range []struct {
		name    string
		planner func(context.Context) (steps.OrphanPlan, error)
		err     error
	}{
		{"no orphans", func(context.Context) (steps.OrphanPlan, error) {
			return steps.OrphanPlan{Hash: testPlan.Hash}, nil
		}, nil},
		{"failed plan", func(context.Context) (steps.OrphanPlan, error) { return testPlan, boom }, boom},
		{"no planner", nil, errNoPlanner},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := testEnv(false)
			env.Polkit, env.PlanOrphans = true, c.planner
			res := probe(t, env).Run(context.Background(), steps.Env{})
			if res.InsertNext != nil || !errors.Is(res.Err, c.err) || (c.err == nil) != (res.Err == nil) {
				t.Fatalf("result %+v, want err %v and nothing inserted", res, c.err)
			}
		})
	}
}

// TestOrphanProbeSudoIgnoresPlanner: under sudo the probe is pacman's, as
// before; a planner that is set is never called.
func TestOrphanProbeSudoIgnoresPlanner(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n-Qdtq) echo libfoo ;;\n-Rs) echo libfoo-1.0-1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := testEnv(false)
	env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) {
		t.Error("the sudo probe called PlanOrphans")
		return testPlan, nil
	}
	res := probe(t, env).Run(context.Background(), steps.Env{})
	if res.Err != nil || res.InsertNext == nil {
		t.Fatalf("result %+v", res)
	}
	st := res.InsertNext
	if st.Plan != nil || !slices.Equal(st.Argv, []string{"sudo", "pacman", "-Rns", "--noconfirm", "--", "libfoo"}) ||
		!slices.Equal(st.Items, []string{"libfoo"}) || !slices.Equal(st.Cascade, []string{"libfoo-1.0-1"}) {
		t.Errorf("sudo removal %+v", st)
	}
}

// TestEnvValidatePolkit: under Polkit, PlanOrphans is required.
func TestEnvValidatePolkit(t *testing.T) {
	e := Env{Polkit: true}
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "PlanOrphans") {
		t.Fatalf("Validate = %v, want PlanOrphans named", err)
	}
	if err := (Env{}).Validate(); strings.Contains(err.Error(), "PlanOrphans") {
		t.Fatalf("Validate = %v: PlanOrphans named under sudo", err)
	}
}

// TestOnlyTopgradeAsksUnderPolkit: under polkit (the GUI) the only step of
// Full Cleanup that gets the askpass (NeedsPriv, no Verb, a command) is
// topgrade: yay -Sc's root half (pacman -Sc) is a part of the helper's one
// call, and the yay step runs `yay -Sc --aur`, which in yay runs no sudo.
// Under sudo (the TUI) the AUR cache step is as it was: yay -Sc, its sudo
// on the primed ticket, and no pacman -Sc step of its own.
func TestOnlyTopgradeAsksUnderPolkit(t *testing.T) {
	a, _ := ByID(IDFullCleanup)
	for _, polkit := range []bool{false, true} {
		env := testEnv(true)
		env.Polkit = polkit
		env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { return testPlan, nil }
		queue := a.Steps(env)
		// The orphan removal the probe inserts is a helper step.
		noPacman(t)
		for _, s := range queue {
			if s.ID == steps.IDOrphansProbe && polkit {
				queue = append(queue, *s.Run(context.Background(), steps.Env{}).InsertNext)
			}
		}
		var askpass []string
		for _, s := range queue {
			if s.NeedsPriv && s.Verb == "" && s.Run == nil {
				askpass = append(askpass, s.ID)
			}
		}
		yay := queue[slices.IndexFunc(queue, func(s steps.Step) bool { return s.ID == steps.IDYayCache })]
		hasSc := slices.ContainsFunc(queue, func(s steps.Step) bool { return s.ID == steps.IDPkgCacheUninstalled })
		if !polkit {
			if !slices.Equal(yay.Argv, []string{"yay", "-Sc", "--noconfirm"}) || yay.NeedsPriv || hasSc {
				t.Errorf("sudo: AUR cache step %+v, a pacman -Sc step: %v", yay, hasSc)
			}
			continue
		}
		if !slices.Equal(askpass, []string{steps.IDTopgrade}) {
			t.Errorf("polkit: steps with the askpass %q, want only topgrade", askpass)
		}
		if !slices.Equal(yay.Argv, []string{"yay", "-Sc", "--aur", "--noconfirm"}) || yay.NeedsPriv || yay.Verb != "" {
			t.Errorf("polkit: AUR cache step %+v, want yay -Sc --aur as the user", yay)
		}
		sc := queue[slices.IndexFunc(queue, func(s steps.Step) bool { return s.ID == steps.IDPkgCacheUninstalled })]
		if sc.Verb != steps.VerbFullCleanup || sc.Combine != steps.VerbFullCleanup || !sc.NeedsPriv || sc.Run != nil ||
			sc.Confirm != "" || !slices.Equal(sc.Argv, []string{"pacman", "-Sc", "--noconfirm"}) {
			t.Errorf("polkit: pacman -Sc step %+v", sc)
		}
	}
}

// TestFullCleanupPolkitOneCall: under polkit, Full Cleanup's root steps are
// consecutive and marked for the one full-cleanup call, right behind the
// orphan probe (whose removal joins them first), pacman -Sc among them,
// with yay -Sc --aur after them and hyprpm last. Under sudo nothing is combined and the order is the
// TUI's.
func TestFullCleanupPolkitOneCall(t *testing.T) {
	a, _ := ByID(IDFullCleanup)
	idsOf := func(q []steps.Step) []string {
		var out []string
		for _, s := range q {
			out = append(out, s.ID)
		}
		return out
	}
	env := testEnv(true)
	sudoQ := a.Steps(env)
	for _, s := range sudoQ {
		if s.Combine != "" {
			t.Errorf("sudo: %s is combined", s.ID)
		}
	}
	if want := []string{steps.IDAURAudit, steps.IDTopgrade, steps.IDPaccache, steps.IDPaccacheWipe, steps.IDOrphansProbe,
		steps.IDYayCache, steps.IDTmpfiles, steps.IDJournal, steps.IDIntegrity, steps.IDHyprpm}; !slices.Equal(idsOf(sudoQ), want) {
		t.Errorf("sudo order %q, want %q", idsOf(sudoQ), want)
	}

	env.Polkit = true
	env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { return testPlan, nil }
	q := a.Steps(env)
	want := []string{steps.IDAURAudit, steps.IDTopgrade, steps.IDOrphansProbe, steps.IDPaccache, steps.IDPaccacheWipe,
		steps.IDPkgCacheUninstalled, steps.IDTmpfiles, steps.IDJournal, steps.IDIntegrity, steps.IDYayCache, steps.IDHyprpm}
	if !slices.Equal(idsOf(q), want) {
		t.Fatalf("polkit order %q, want %q", idsOf(q), want)
	}
	for i, s := range q {
		root := s.Verb != "" && s.Run == nil
		if root != (s.Combine == steps.VerbFullCleanup) {
			t.Errorf("%s: Combine %q", s.ID, s.Combine)
		}
		// The same step as under sudo, but for Combine; the split AUR cache
		// is TestOnlyTopgradeAsksUnderPolkit's.
		if s.ID == steps.IDYayCache || s.ID == steps.IDPkgCacheUninstalled {
			continue
		}
		was := sudoQ[slices.IndexFunc(sudoQ, func(x steps.Step) bool { return x.ID == s.ID })]
		if s.Title != was.Title || !slices.Equal(s.Argv, was.Argv) || s.Confirm != was.Confirm || s.NeedsPriv != was.NeedsPriv {
			t.Errorf("%d %s changed: %+v vs %+v", i, s.ID, s, was)
		}
	}
	noPacman(t)
	res := q[2].Run(context.Background(), steps.Env{})
	if res.InsertNext == nil || res.InsertNext.Combine != steps.VerbFullCleanup {
		t.Fatalf("the inserted removal isn't combined: %+v", res.InsertNext)
	}
}
