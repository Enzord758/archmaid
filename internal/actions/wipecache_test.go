package actions

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// shimPaccache puts a paccache first on PATH that records its argv, one
// call per line, in the returned file, then runs body.
func shimPaccache(t *testing.T, body string) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" + body
	if err := os.WriteFile(filepath.Join(dir, "paccache"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func readCalls(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestFullCleanupHasNoWipe: Full Cleanup (the sudo queue and the polkit
// split, with and without hyprpm) neither deletes the entire package cache
// nor asks about it; the root steps it combines don't include it. Owner
// decision 2026-10-18: step 04's "keep 3" is the cache cleaning, and the
// wipe is its own menu action (IDWipeCache).
func TestFullCleanupHasNoWipe(t *testing.T) {
	a, _ := ByID(IDFullCleanup)
	for _, polkit := range []bool{false, true} {
		for _, hyprpm := range []bool{false, true} {
			env := testEnv(hyprpm)
			env.Polkit = polkit
			env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { return testPlan, nil }
			for _, s := range a.Steps(env) {
				if s.ID == steps.IDPaccacheWipe || s.Verb == steps.VerbPaccacheWipe || slices.Contains(s.Argv, "-rk0") {
					t.Errorf("polkit=%v hyprpm=%v: step %q wipes the package cache", polkit, hyprpm, s.ID)
				}
				if s.Confirm != "" {
					t.Errorf("polkit=%v hyprpm=%v: step %q has a confirm %q, Full Cleanup asks nothing up front", polkit, hyprpm, s.ID, s.Confirm)
				}
			}
		}
	}
	if slices.Contains(fullCleanupRoot, steps.IDPaccacheWipe) {
		t.Error("the wipe is in Full Cleanup's root steps")
	}
}

// TestWipeCacheMenuEntry: the action is on the menu and discovers its step.
func TestWipeCacheMenuEntry(t *testing.T) {
	a, ok := ByID(IDWipeCache)
	if !ok || a.Label != "Delete entire package cache" || a.Discover == nil || a.Steps != nil {
		t.Fatalf("menu entry: %+v, %v", a, ok)
	}
	if !a.Record.Discovery() {
		t.Errorf("record %q is not a discovery category", a.Record)
	}
}

// TestDiscoverWipeCacheConfirmShowsTheDryRun: the confirm shows what the
// dry run says it frees, and the warning, before anything runs; the only
// paccache call made is the read-only dry run.
func TestDiscoverWipeCacheConfirmShowsTheDryRun(t *testing.T) {
	calls := shimPaccache(t, `echo "/var/cache/pacman/pkg/a-1-1-x86_64.pkg.tar.zst"
echo "==> finished dry run: 12 candidates (disk space saved: 1.37 GiB)"
`)
	d := discoverWipeCache(context.Background(), Env{})
	p, ok := d.(Pending)
	if !ok || len(p.Steps) != 1 {
		t.Fatalf("got %#v, want one pending step", d)
	}
	s := p.Steps[0]
	for _, want := range []string{"1.37 GiB", "12 package files", "can't reinstall or downgrade", "without internet"} {
		if !strings.Contains(s.Confirm, want) {
			t.Errorf("confirm %q lacks %q", s.Confirm, want)
		}
	}
	if !strings.HasPrefix(s.Confirm, "Delete the ENTIRE package cache?\n") {
		t.Errorf("confirm title: %q", s.Confirm)
	}
	if s.ID != steps.IDPaccacheWipe || s.Verb != steps.VerbPaccacheWipe || !s.NeedsPriv || !s.Optional ||
		!slices.Equal(s.Argv, []string{"sudo", "paccache", "-rk0"}) || len(s.Items) != 0 {
		t.Errorf("step %+v", s)
	}
	if got := readCalls(t, calls); !slices.Equal(got, []string{"--nocolor -dk0"}) {
		t.Errorf("paccache was called as %q, want only the dry run", got)
	}
	if !strings.Contains(p.Checked.Summary, "1.37 GiB") {
		t.Errorf("summary %q", p.Checked.Summary)
	}
}

func TestWipeConfirmOne(t *testing.T) {
	if got := WipeConfirm(1, "3.00 MiB"); !strings.Contains(got, "(1 package file)") {
		t.Errorf("%q", got)
	}
}

// TestDiscoverWipeCacheNothingOrFailing: an empty cache is a notice, and a
// dry run that fails, prints something unreadable or can't start never
// yields a confirm without its numbers.
func TestDiscoverWipeCacheNothingOrFailing(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
		isErr            bool
	}{
		{"empty", "echo '==> no candidate packages found for pruning'\n", "The package cache is empty", false},
		{"fails", "echo 'permission denied' >&2\nexit 1\n", "Couldn't measure the package cache: permission denied", true},
		{"unreadable", "echo 'something else'\n", "Couldn't read paccache's dry-run summary", true},
		{"hostile size", "echo 'finished dry run: 1 candidates (disk space saved: 9 B; rm -rf)'\n", "Couldn't read paccache's dry-run summary", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			shimPaccache(t, c.body)
			n, ok := discoverWipeCache(context.Background(), Env{}).(Notice)
			if !ok || !strings.Contains(n.Text, c.want) || (n.Err != "") != c.isErr {
				t.Fatalf("got %#v, want a notice with %q (err=%v)", n, c.want, c.isErr)
			}
		})
	}
}

func TestDiscoverWipeCacheNoPaccache(t *testing.T) {
	env := Env{LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	n, ok := discoverWipeCache(context.Background(), env).(Notice)
	if !ok || !strings.Contains(n.Text, "paccache is not installed") || n.Err == "" {
		t.Fatalf("got %#v", n)
	}
}
