package report

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestYayBuildDir: where yay -Sc cleans: the buildDir of yay's config.json
// (a path, ~/ and $VAR expanded), else <cache>/yay; a relative or empty one
// is ignored.
func TestYayBuildDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("BUILDROOT", "/build/root")
	if got, want := yayBuildDir(), filepath.Join(home, ".cache", "yay"); got != want {
		t.Errorf("no config: %q, want %q", got, want)
	}
	write := func(json string) {
		t.Helper()
		p := filepath.Join(home, ".config", "yay", "config.json")
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ json, want string }{
		{`{"buildDir":"/mnt/yay"}`, "/mnt/yay"},
		{`{"buildDir":"~/builds"}`, filepath.Join(home, "builds")},
		{`{"buildDir":"$BUILDROOT/yay"}`, "/build/root/yay"},
		{`{"buildDir":"relative/dir"}`, filepath.Join(home, ".cache", "yay")},
		{`{"buildDir":""}`, filepath.Join(home, ".cache", "yay")},
		{`{not json`, filepath.Join(home, ".cache", "yay")},
	} {
		write(c.json)
		if got := yayBuildDir(); got != c.want {
			t.Errorf("%s: %q, want %q", c.json, got, c.want)
		}
	}
	// Without XDG_CONFIG_HOME the config is under ~/.config all the same.
	t.Setenv("XDG_CONFIG_HOME", "")
	write(`{"buildDir":"/mnt/other"}`)
	if got := yayBuildDir(); got != "/mnt/other" {
		t.Errorf("config under HOME: %q", got)
	}
}

func TestUserCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	if got, want := userCacheDir(), filepath.Join(home, ".cache"); got != want {
		t.Errorf("default %q, want %q", got, want)
	}
	t.Setenv("XDG_CACHE_HOME", "/somewhere/cache")
	if got := userCacheDir(); got != "/somewhere/cache" {
		t.Errorf("XDG_CACHE_HOME: %q", got)
	}
	t.Setenv("XDG_CACHE_HOME", "relative")
	if got := userCacheDir(); got != filepath.Join(home, ".cache") {
		t.Errorf("a relative XDG_CACHE_HOME is ignored: %q", got)
	}
}

// TestRunningKernel reads the real release (a read of /proc, nothing run).
func TestRunningKernel(t *testing.T) {
	if _, err := os.Stat("/proc/sys/kernel/osrelease"); err != nil {
		t.Skip("no /proc")
	}
	if got := runningKernel(); got == "" || strings.ContainsAny(got, "\n ") {
		t.Errorf("runningKernel() = %q", got)
	}
}

// TestZeroConfig: a Collector made from nothing never panics, runs no
// command (its Query refuses) and says what it couldn't do.
func TestZeroConfig(t *testing.T) {
	c := New(Config{}, "topgrade", time.Now())
	ctx := context.Background()
	c.StepStarting(ctx, steps.Step{ID: steps.IDTopgrade})
	c.StepStarting(ctx, steps.Step{ID: steps.IDPaccache})
	c.StepStarting(ctx, steps.Step{ID: steps.IDYayCache, Argv: []string{"yay", "-Sc"}})
	c.StepResult(steps.Result{})
	c.StepDone(ctx, steps.IDTopgrade, steps.OK)
	c.StepDone(ctx, steps.IDPaccache, steps.OK)
	c.StepDone(ctx, steps.IDYayCache, steps.OK)
	r := c.Finish(ctx, FinishInfo{})
	if r.Packages != nil || len(r.Notes) == 0 {
		t.Errorf("a zero config read a log: %+v notes %v", r.Packages, r.Notes)
	}
	// No yay folder known, no cache folder readable by a refusing pacman-conf:
	// the AUR row is unknown, never 0.
	for _, a := range r.Freed {
		if a.ID == AreaYay && a.Known {
			t.Errorf("yay row %+v with no folder known", a)
		}
	}
}

// TestDoneAreaIsNotMeasuredTwice: a step id whose area already ended (twice
// told, or registered by two steps) keeps its first number.
func TestDoneAreaIsNotMeasuredTwice(t *testing.T) {
	w := newWorld(t)
	w.fill(w.jnl, 2, 100<<10)
	c := New(w.config(), "x", time.Now())
	ctx := context.Background()
	c.StepStarting(ctx, pkgStep(steps.IDJournal, "journalctl"))
	os.RemoveAll(w.jnl)
	c.StepDone(ctx, steps.IDJournal, steps.OK)
	first := c.areas[AreaJournal].bytes
	c.stepAreas["again"] = []string{AreaJournal, "unknown-area"}
	c.StepDone(ctx, "again", steps.OK)
	if c.areas[AreaJournal].bytes != first || first == 0 {
		t.Errorf("journal bytes %d after the second told, first %d", c.areas[AreaJournal].bytes, first)
	}
}

// TestInstalledAURMarked: a foreign package that was installed (not upgraded)
// is marked and counted too.
func TestInstalledAURMarked(t *testing.T) {
	w := newWorld(t)
	w.answers["pacman"] = func([]string) ([]byte, error) { return []byte("fresh-aur\nother\n"), nil }
	c := New(w.config(), "topgrade", time.Now())
	ctx := context.Background()
	c.StepStarting(ctx, pkgStep(steps.IDTopgrade, "topgrade"))
	w.appendLog("[t] [ALPM] installed fresh-aur (1-1)", "[t] [ALPM] installed from-repo (1-1)")
	c.StepDone(ctx, steps.IDTopgrade, steps.OK)
	p := c.Finish(ctx, FinishInfo{}).Packages
	if p.AURInstalled != 1 || !p.Installed[0].AUR || p.Installed[1].AUR {
		t.Errorf("installed %+v, AUR %d", p.Installed, p.AURInstalled)
	}
}

// TestPlural: the attention texts' plurals.
func TestPlural(t *testing.T) {
	if plural(1, "AUR warning") != "1 AUR warning" || plural(2, "high-severity finding") != "2 high-severity findings" {
		t.Error("plural")
	}
}

// TestMeasureOddRoots: a root that is a file counts as that file; a walk
// with no roots at all (a folder nobody knows) is unknown.
func TestMeasureOddRoots(t *testing.T) {
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "f"), 8192)
	if s := DirSize(context.Background(), filepath.Join(dir, "f")); !s.OK || s.Bytes < 8192 {
		t.Errorf("a file root measured %+v", s)
	}
	c := New(newWorld(t).config(), "x", time.Now())
	if s := c.measure(context.Background(), AreaYay); !s.OK {
		// the world has a yay folder: this one is known
		t.Errorf("yay folder %+v", s)
	}
	cfg := newWorld(t).config()
	cfg.YayDir = nil
	if s := New(cfg, "x", time.Now()).measure(context.Background(), AreaYay); s.OK {
		t.Errorf("no yay folder known measured %+v", s)
	}
}

// TestParseLogOddInput: a line longer than any pacman wrote is skipped, not
// an error; a huge count in paccache's line is refused.
func TestParseLogOddInput(t *testing.T) {
	long := strings.Repeat("x", 2<<20)
	ch, err := ParseLog(strings.NewReader("[t] [ALPM] upgraded a (1 -> 2)\n" + long + "\n[t] [ALPM] upgraded b (1 -> 2)\n"))
	if err != nil || len(ch.Upgraded) != 1 {
		t.Errorf("long line: %v, upgraded %+v", err, ch.Upgraded)
	}
	if _, _, ok := ParsePaccacheDry("==> finished dry run: " + strings.Repeat("9", 40) + " candidates (disk space saved: 1 B)"); ok {
		t.Error("an overflowing count parsed")
	}
}

// TestVersionLessKinds: numbers sort above letters, equal prefixes by length.
func TestVersionLessKinds(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.0a", "1.01", true}, {"1.01", "1.0a", false}, {"1.0", "1.0.1", true}, {"1.0.1", "1.0", false},
		{"1.0b", "1.0a", false}, {"1.0a", "1.0b", true},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// TestKernelStaleSkipsFiles: a stray file in the modules folder is no kernel.
func TestKernelStaleSkipsFiles(t *testing.T) {
	dir := modules(t, map[string]string{"7.2.12-1": "linux"})
	os.WriteFile(filepath.Join(dir, "modules.stray"), nil, 0o644)
	if stale, inst, _ := kernelStale("7.2.10-1", dir, up("linux")); !stale || inst != "7.2.12-1" {
		t.Errorf("= %v %q", stale, inst)
	}
}
