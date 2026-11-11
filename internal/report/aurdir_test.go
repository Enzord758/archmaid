package report

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The folder the run summary measures for the AUR cache step is the one the
// AUR helper in use cleans: yay's build folder, paru's clone folder, none
// without a helper (aurhelper has the per-helper rules, tested there).
func TestAURBuildDirFollowsTheHelper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("AURDEST", "")
	t.Setenv("PARU_CONF", "")
	for helper, want := range map[string]string{
		"yay":  filepath.Join(home, ".cache", "yay"),
		"paru": filepath.Join(home, ".cache", "paru", "clone"),
		"none": "",
	} {
		t.Setenv("ARCHMAID_AUR_HELPER", helper)
		if got := aurBuildDir(); got != want {
			t.Errorf("helper %s: %q, want %q", helper, got, want)
		}
	}
	t.Setenv("ARCHMAID_AUR_HELPER", "paru")
	if got := DefaultConfig().YayDir(); got != filepath.Join(home, ".cache", "paru", "clone") {
		t.Errorf("DefaultConfig's AUR dir: %q", got)
	}
	t.Setenv("ARCHMAID_AUR_HELPER", "none")
	if got := DefaultConfig().YayDir(); got != "" {
		t.Errorf("DefaultConfig's AUR dir: %q", got)
	}
}

// The AUR cache step measures the build cache under sudo and polkit alike for
// either helper (--aur: only that area), and nothing when it is the in-process
// "no AUR helper" note.
func TestAreasOfAURCacheStep(t *testing.T) {
	for _, c := range []struct {
		name string
		st   steps.Step
		want []string
	}{
		{"yay under sudo", steps.Step{ID: steps.IDYayCache, Argv: []string{"yay", "-Sc", "--noconfirm"}}, []string{AreaPkgUninstalled, AreaYay}},
		{"paru under sudo", steps.Step{ID: steps.IDYayCache, Argv: []string{"paru", "-Sc", "--noconfirm"}}, []string{AreaPkgUninstalled, AreaYay}},
		{"paru under polkit", steps.Step{ID: steps.IDYayCache, Argv: []string{"paru", "-Sc", "--aur", "--noconfirm"}}, []string{AreaYay}},
		{"no helper", steps.Step{ID: steps.IDYayCache, Run: func(context.Context, steps.Env) steps.Result { return steps.Result{} }}, nil},
	} {
		got := areasOf(c.st)
		if len(got) != len(c.want) || (len(got) > 0 && (got[0] != c.want[0] || got[len(got)-1] != c.want[len(c.want)-1])) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
