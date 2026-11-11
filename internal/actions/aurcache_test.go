package actions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/aurhelper"
	"github.com/Enzord758/archmaid/internal/steps"
)

// aurStep is Full Cleanup's AUR cache step for env.
func aurStep(t *testing.T, env Env) steps.Step {
	t.Helper()
	a, _ := ByID(IDFullCleanup)
	noPacman(t)
	for _, s := range a.Steps(env) {
		if s.ID == steps.IDYayCache {
			return s
		}
	}
	t.Fatal("Full Cleanup has no AUR cache step")
	return steps.Step{}
}

func helperEnv(h aurhelper.Helper, polkit bool) Env {
	env := testEnv(true)
	env.Polkit = polkit
	env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { return testPlan, nil }
	env.AURHelper = func() aurhelper.Helper { return h }
	return env
}

// TestAURCacheStepPerHelper: the AUR cache step runs the detected helper, with
// the same flags for both: under sudo `-Sc --noconfirm` (the helper runs sudo
// pacman -Sc itself, so NeedsPriv), under polkit `-Sc --aur --noconfirm` as a
// plain user step. The step ID stays IDYayCache.
func TestAURCacheStepPerHelper(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	for _, name := range []string{"yay", "paru"} {
		h := aurhelper.Helper{Name: name, Path: "/usr/bin/" + name}
		sudo := aurStep(t, helperEnv(h, false))
		if want := []string{name, "-Sc", "--noconfirm", "--color", "always"}; !slices.Equal(sudo.Argv, want) || !sudo.NeedsPriv || sudo.Run != nil || sudo.Title != "Cleaning AUR cache…" {
			t.Errorf("%s under sudo: %+v, want argv %q with NeedsPriv", name, sudo, want)
		}
		pk := aurStep(t, helperEnv(h, true))
		if want := []string{name, "-Sc", "--aur", "--noconfirm", "--color", "always"}; !slices.Equal(pk.Argv, want) || pk.NeedsPriv || pk.Verb != "" || pk.Run != nil {
			t.Errorf("%s under polkit: %+v, want argv %q as a user step", name, pk, want)
		}
	}
	// NO_COLOR drops the colour switch for either.
	t.Setenv("NO_COLOR", "1")
	if got := aurStep(t, helperEnv(aurhelper.Helper{Name: "paru"}, true)).Argv; !slices.Equal(got, []string{"paru", "-Sc", "--aur", "--noconfirm"}) {
		t.Errorf("NO_COLOR: %q", got)
	}
}

// TestAURCacheStepNoHelper: with neither installed the step stays in the
// checklist as an in-process one that says why it does nothing; it starts no
// program and needs no root, under sudo and polkit alike.
func TestAURCacheStepNoHelper(t *testing.T) {
	for _, polkit := range []bool{false, true} {
		st := aurStep(t, helperEnv(aurhelper.Helper{}, polkit))
		if st.Run == nil || st.Argv != nil || st.NeedsPriv || st.Verb != "" || st.Confirm != "" {
			t.Fatalf("polkit=%v: %+v, want a plain in-process step", polkit, st)
		}
		r := st.Run(context.Background(), steps.Env{})
		if r.Err != nil || !strings.Contains(r.Output, "No AUR helper (paru or yay) is installed") {
			t.Errorf("polkit=%v: result %+v", polkit, r)
		}
	}
	// The step keeps its place: after the orphan probe, before the temp files.
	a, _ := ByID(IDFullCleanup)
	q := a.Steps(helperEnv(aurhelper.Helper{}, false))
	if i, j := slices.IndexFunc(q, func(s steps.Step) bool { return s.ID == steps.IDYayCache }), slices.IndexFunc(q, func(s steps.Step) bool { return s.ID == steps.IDTmpfiles }); i < 0 || j != i+1 {
		t.Errorf("queue order %q", ids(q))
	}
}

// The default detection runs through Env.LookPath: a machine with only paru
// gets paru, one with both gets paru (topgrade's autodetect order), only yay
// gets yay, neither gets the no-helper step.
func TestAURCacheStepDetectsThroughLookPath(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv(aurhelper.EnvOverride, "") // the test environment forces yay; this is about detection
	have := func(names ...string) func(string) (string, error) {
		return func(f string) (string, error) {
			if slices.Contains(names, f) || f == "hyprpm" {
				return "/usr/bin/" + f, nil
			}
			return "", errors.New("not found")
		}
	}
	for _, c := range []struct {
		have []string
		want string // "" = the no-helper step
	}{
		{[]string{"paru"}, "paru"}, {[]string{"yay"}, "yay"}, {[]string{"paru", "yay"}, "paru"}, {nil, ""},
	} {
		env := testEnv(true)
		env.LookPath = have(c.have...)
		st := aurStep(t, env)
		if c.want == "" {
			if st.Run == nil {
				t.Errorf("%v: %+v, want the no-helper step", c.have, st)
			}
			continue
		}
		if len(st.Argv) == 0 || st.Argv[0] != c.want {
			t.Errorf("%v: argv %q, want %s", c.have, st.Argv, c.want)
		}
	}
}
