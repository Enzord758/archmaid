package actions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// homeWithCache gives the test a $HOME whose .cache holds n bytes (0: an
// empty folder; -1: no folder at all).
func homeWithCache(t *testing.T, n int) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if n >= 0 {
		dir := filepath.Join(home, ".cache", "app")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, n), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return home
}

const pacDry = `echo "/var/cache/pacman/pkg/a-1-1-x86_64.pkg.tar.zst"
echo "==> finished dry run: 12 candidates (disk space saved: 1.37 GiB)"
`

func discoverCaches(t *testing.T) Discovery {
	t.Helper()
	return discoverClearCaches(context.Background(), Env{})
}

// TestFullCleanupHasNoWipe: Full Cleanup (the sudo queue and the polkit
// split, with and without hyprpm) neither deletes the entire package cache
// nor asks about it; the root steps it combines don't include it. Owner
// decision 2026-10-18: step 04's "keep 3" is the cache cleaning, and the
// wipe is part of the menu's Clear caches.
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

// TestClearCachesMenuEntry: one entry, "Clear caches", replaces the two
// retired ones; they keep a label for old records but are not on the menu.
func TestClearCachesMenuEntry(t *testing.T) {
	a, ok := ByID(IDClearCaches)
	if !ok || a.Label != "Clear caches" || a.Discover == nil || a.Steps != nil {
		t.Fatalf("menu entry: %+v, %v", a, ok)
	}
	if !a.Record.Discovery() {
		t.Errorf("record %q is not a discovery category", a.Record)
	}
	for _, old := range []string{IDUserCache, IDWipeCache} {
		if _, on := ByID(old); on {
			t.Errorf("retired action %q is still on the menu", old)
		}
		if LegacyLabel(old) == "" {
			t.Errorf("retired action %q has no label for old records", old)
		}
	}
	if LegacyLabel(IDClearCaches) != "" {
		t.Error("LegacyLabel answers for a live action")
	}
}

// TestClearCachesPickRows: the two measured rows, in order, with the sizes,
// the file count and the warning on the package-cache row; the only
// paccache call is the dry run.
func TestClearCachesPickRows(t *testing.T) {
	homeWithCache(t, 3<<20)
	calls := shimPaccache(t, pacDry)
	sel, ok := discoverCaches(t).(NeedsSelection)
	if !ok || !slices.Equal(sel.Items, []string{PickUserCache, PickPkgCache}) || len(sel.Rows) != 2 {
		t.Fatalf("got %#v", sel)
	}
	if sel.Rows[0].Label != "~/.cache — 3 MiB" || sel.Rows[0].Disabled || sel.Rows[0].Note != "Hidden files are kept" {
		t.Errorf("user-cache row %+v", sel.Rows[0])
	}
	pk := sel.Rows[1]
	if pk.Label != "Entire package cache — 1.37 GiB (12 files)" || pk.Disabled {
		t.Errorf("package-cache row %+v", pk)
	}
	for _, want := range []string{"can't reinstall or downgrade", "without internet"} {
		if !strings.Contains(strings.ToLower(pk.Note), want) {
			t.Errorf("the package-cache row's note %q lacks %q", pk.Note, want)
		}
	}
	if got := readCalls(t, calls); !slices.Equal(got, []string{"--nocolor -dk0"}) {
		t.Errorf("paccache was called as %q, want only the dry run", got)
	}
	if !strings.Contains(sel.Checked.Summary, "1.37 GiB") || !strings.Contains(sel.Checked.Summary, "3 MiB") {
		t.Errorf("summary %q", sel.Checked.Summary)
	}
}

// TestClearCachesZeroByteRowIsDisabled: an empty ~/.cache, or an empty
// package cache, is a disabled row with its reason, and the other row is
// still offered.
func TestClearCachesZeroByteRowIsDisabled(t *testing.T) {
	for _, c := range []struct {
		name       string
		cache      int
		pac        string
		disabled   [2]bool
		wantReason string
	}{
		{"empty home cache", 0, pacDry, [2]bool{true, false}, "Empty"},
		{"no home cache folder", -1, pacDry, [2]bool{true, false}, "Empty"},
		{"empty package cache", 1 << 20, "echo '==> no candidate packages found for pruning'\n", [2]bool{false, true}, "Empty"},
		{"zero bytes counted", 1 << 20, "echo '==> finished dry run: 3 candidates (disk space saved: 0 B)'\n", [2]bool{false, true}, "Empty"},
		{"paccache fails", 1 << 20, "echo 'permission denied' >&2\nexit 1\n", [2]bool{false, true}, "permission denied"},
		{"unreadable summary", 1 << 20, "echo 'something else'\n", [2]bool{false, true}, "dry-run summary"},
		{"hostile size", 1 << 20, "echo 'finished dry run: 1 candidates (disk space saved: 9 B; rm -rf)'\n", [2]bool{false, true}, "dry-run summary"},
	} {
		t.Run(c.name, func(t *testing.T) {
			homeWithCache(t, c.cache)
			shimPaccache(t, c.pac)
			d := discoverCaches(t)
			sel, ok := d.(NeedsSelection)
			if !ok {
				t.Fatalf("got %#v, want a pick", d)
			}
			for i, r := range sel.Rows {
				if r.Disabled != c.disabled[i] {
					t.Errorf("row %d disabled=%v, want %v: %+v", i, r.Disabled, c.disabled[i], r)
				}
				if r.Disabled && !strings.Contains(r.Note, c.wantReason) {
					t.Errorf("row %d's reason %q lacks %q", i, r.Note, c.wantReason)
				}
			}
		})
	}
}

// TestClearCachesNothingToClear: both rows disabled is a notice, an error
// notice when a look failed and a plain one when both are just empty.
func TestClearCachesNothingToClear(t *testing.T) {
	homeWithCache(t, 0)
	shimPaccache(t, "echo '==> no candidate packages found for pruning'\n")
	n, ok := discoverCaches(t).(Notice)
	if !ok || n.Err != "" || !strings.Contains(n.Text, "Nothing to clear") {
		t.Fatalf("both empty: %#v", n)
	}
	shimPaccache(t, "echo boom >&2\nexit 1\n")
	n, ok = discoverCaches(t).(Notice)
	if !ok || n.Err == "" || strings.Contains(n.Err, "\n") || !strings.Contains(n.Text, "boom") {
		t.Fatalf("one failed: %#v", n)
	}
	env := Env{LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	n, ok = discoverClearCaches(context.Background(), env).(Notice)
	if !ok || !strings.Contains(n.Text, "paccache is not installed") || n.Err == "" {
		t.Fatalf("no paccache: %#v", n)
	}
}

// TestClearCachesSummaryIsStrict (review of #186): the package row is built
// from paccache's two captures only, through report.ParsePaccacheDry. A
// decimal comma is accepted; anything that is not plain ASCII digits and a
// known unit is a failed measurement (a disabled row), never a row with
// text from the dry run in it.
func TestClearCachesSummaryIsStrict(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       string // "" = refused
	}{
		{"comma", "finished dry run: 3 candidates (disk space saved: 1,37 GiB)", "1.37 GiB (3 files)"},
		{"one candidate", "finished dry run: 1 candidate (disk space saved: 9 B)", "9 B (1 file)"},
		{"ansi in size", "finished dry run: 3 candidates (disk space saved: \x1b[31m1 B)", ""},
		{"ansi after unit", "finished dry run: 3 candidates (disk space saved: 1 B\x1b[2J)", ""},
		{"newline in size", "finished dry run: 3 candidates (disk space saved: 1\nDelete? B)", ""},
		{"unicode digits", "finished dry run: 3 candidates (disk space saved: ٣ B)", ""},
		{"unicode unit", "finished dry run: 3 candidates (disk space saved: 1 Å)", ""},
		{"unicode count", "finished dry run: ٣ candidates (disk space saved: 1 B)", ""},
		{"no unit", "finished dry run: 3 candidates (disk space saved: 12)", ""},
		{"trailing junk in parens", "finished dry run: 3 candidates (disk space saved: 1 B, rm)", ""},
		{"negative", "finished dry run: 3 candidates (disk space saved: -1 B)", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			homeWithCache(t, 1<<20)
			shimPaccache(t, "printf '%s\\n' \"$LINE\"\n")
			t.Setenv("LINE", c.line)
			d := discoverCaches(t)
			sel, ok := d.(NeedsSelection)
			if !ok {
				t.Fatalf("got %#v, want a pick", d)
			}
			r := sel.Rows[1]
			if c.want == "" {
				if !r.Disabled {
					t.Fatalf("row %+v, want it disabled", r)
				}
			} else if r.Disabled || !strings.HasSuffix(r.Label, c.want) {
				t.Fatalf("row %+v, want %q", r, c.want)
			}
			if strings.ContainsRune(r.Label+r.Note, 0x1b) || strings.Contains(r.Label+r.Note, "\n") {
				t.Errorf("row %+v carries the dry run's text", r)
			}
		})
	}
}

// TestClearCachesPaccacheTimesOut: a dry run that hangs (a cache on a dead
// mount) is a disabled row, not an action stuck on "Measuring…".
func TestClearCachesPaccacheTimesOut(t *testing.T) {
	old := wipeTimeout
	wipeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { wipeTimeout = old })
	homeWithCache(t, 1<<20)
	shimPaccache(t, "exec sleep 20\n")
	start := time.Now()
	d := discoverCaches(t)
	sel, ok := d.(NeedsSelection)
	if !ok || !sel.Rows[1].Disabled || !strings.Contains(sel.Rows[1].Note, "timed out") {
		t.Fatalf("got %#v", d)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
}

// TestClearCachesSlowWalkStaysPickable: a ~/.cache walk that runs out of
// time still offers the row (the folder exists; only its size is unknown).
func TestClearCachesSlowWalkStaysPickable(t *testing.T) {
	old := userCacheTimeout
	userCacheTimeout = time.Nanosecond
	t.Cleanup(func() { userCacheTimeout = old })
	homeWithCache(t, 1<<20)
	shimPaccache(t, pacDry)
	d := discoverCaches(t)
	sel, ok := d.(NeedsSelection)
	if !ok || sel.Rows[0].Disabled || !strings.Contains(sel.Rows[0].Label, "size unknown") {
		t.Fatalf("got %#v", d)
	}
}

// TestClearCachesStepsFollowThePick: only what was ticked is built, the
// user-cache step before the root one; neither step has a confirm of its
// own (the pick was it); nothing ticked is no queue.
func TestClearCachesStepsFollowThePick(t *testing.T) {
	ids := func(picked ...string) []string {
		var out []string
		for _, s := range ClearCachesSteps(picked) {
			if s.Confirm != "" {
				t.Errorf("pick %q: step %q has a confirm %q", picked, s.ID, s.Confirm)
			}
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids(); got != nil {
		t.Errorf("nothing picked built %v", got)
	}
	if got := ids("zzz"); got != nil {
		t.Errorf("an unknown key built %v", got)
	}
	if got := ids(PickUserCache); !slices.Equal(got, []string{steps.IDUserCache}) {
		t.Errorf("~/.cache alone built %v", got)
	}
	if got := ids(PickPkgCache); !slices.Equal(got, []string{steps.IDPaccacheWipe}) {
		t.Errorf("package cache alone built %v", got)
	}
	if got := ids(PickPkgCache, PickUserCache); !slices.Equal(got, []string{steps.IDUserCache, steps.IDPaccacheWipe}) {
		t.Errorf("both built %v", got)
	}
	u := ClearCachesSteps([]string{PickUserCache})[0]
	if u.NeedsPriv || u.Verb != "" || !slices.Equal(u.Argv, []string{"bash", "-c", "rm -rf \"$HOME/.cache/\"*"}) {
		t.Errorf("user-cache step %+v: runs as the user", u)
	}
	w := ClearCachesSteps([]string{PickPkgCache})[0]
	if !w.NeedsPriv || w.Verb != steps.VerbPaccacheWipe || !w.Optional || !slices.Equal(w.Argv, []string{"sudo", "paccache", "-rk0"}) || len(w.Items) != 0 {
		t.Errorf("package-cache step %+v: root, takes no argument", w)
	}
}

// The user-cache step's real command, run in a scratch HOME: it empties the
// non-dot entries of ~/.cache, removes a symlink without following it (what
// it points to outside ~/.cache survives), keeps ~/.cache itself and its
// dot entries, and with an empty ~/.cache is harmless.
func TestUserCacheStepCommandStaysInsideTheCache(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	cache := filepath.Join(home, ".cache")
	for _, d := range []string{filepath.Join(cache, "app", "sub"), filepath.Join(outside, "keep")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(cache, "app", "sub", "blob"): "x",
		filepath.Join(cache, ".hidden"):            "dot",
		filepath.Join(outside, "keep", "precious"): "p",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "keep"), filepath.Join(cache, "link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	argv := ClearCachesSteps([]string{PickUserCache})[0].Argv
	run := func() {
		t.Helper()
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	run()
	if ents, _ := os.ReadDir(cache); len(ents) != 1 || ents[0].Name() != ".hidden" {
		t.Errorf("~/.cache left %v, want only .hidden", ents)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep", "precious")); err != nil {
		t.Errorf("a symlink was followed out of ~/.cache: %v", err)
	}
	run() // nothing matches the glob: still harmless
	if _, err := os.Stat(filepath.Join(cache, ".hidden")); err != nil {
		t.Error(err)
	}
}
