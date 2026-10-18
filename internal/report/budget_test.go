package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestCollectorLongWaitBeforeCacheSteps: topgrade, a password and a confirm
// take over ten minutes before the cache steps; the walks are still
// measured (the cap counts walking time, not the time since New).
func TestCollectorLongWaitBeforeCacheSteps(t *testing.T) {
	w := newWorld(t)
	w.answers["paccache"] = func([]string) ([]byte, error) { return []byte(paccacheDryFixture), nil }
	w.fill(w.cache, 6, 200<<10)
	w.fill(w.yay, 2, 100<<10)
	now := time.Unix(10000, 0)
	cfg := w.config()
	cfg.Now = func() time.Time { return now }
	ctx := context.Background()
	c := New(cfg, "full-cleanup", now)
	now = now.Add(12 * time.Minute) // topgrade, password wait, orphan confirm

	c.StepStarting(ctx, pkgStep(steps.IDPaccache, "sudo", "paccache", "-r"))
	for _, n := range []string{"fa", "fb", "fc"} {
		os.Remove(filepath.Join(w.cache, n+".pkg.tar.zst"))
	}
	c.StepDone(ctx, steps.IDPaccache, steps.OK)
	now = now.Add(5 * time.Minute) // another wait
	c.StepStarting(ctx, pkgStep(steps.IDYayCache, "yay", "-Sc", "--aur"))
	w.empty(w.yay)
	c.StepDone(ctx, steps.IDYayCache, steps.OK)

	r := c.Finish(ctx, FinishInfo{Elapsed: 17 * time.Minute})
	a := areas(r)
	if !a[AreaPaccache].Known || a[AreaPaccache].Bytes < 3*200<<10 {
		t.Errorf("paccache row %+v, want about 600 KB freed", a[AreaPaccache])
	}
	if !a[AreaYay].Known || a[AreaYay].Bytes < 2*100<<10 {
		t.Errorf("yay row %+v, want about 200 KB freed", a[AreaYay])
	}
	if r.FreedPartial || r.FreedBytes != a[AreaPaccache].Bytes+a[AreaYay].Bytes {
		t.Errorf("freed %d partial %v, want the sum of the rows", r.FreedBytes, r.FreedPartial)
	}
}

// TestCollectorWalkCapHit: when the time spent walking reaches the cap, the
// rest are unknown; the total is the sum of the known rows, and partial.
func TestCollectorWalkCapHit(t *testing.T) {
	w := newWorld(t)
	w.fill(w.yay, 2, 100<<10)
	w.fill(w.tmp, 2, 100<<10)
	now := time.Unix(10000, 0)
	cfg := w.config()
	// Every read of the clock is 3 s later, so each walk "takes" 3 s.
	cfg.Now = func() time.Time { now = now.Add(3 * time.Second); return now }
	cfg.MeasureEach, cfg.MeasureTotal = 5*time.Second, 6*time.Second
	ctx := context.Background()
	c := New(cfg, "x", now)
	c.StepStarting(ctx, pkgStep(steps.IDYayCache, "yay", "-Sc", "--aur")) // walk 1: 3 s
	w.empty(w.yay)
	c.StepDone(ctx, steps.IDYayCache, steps.OK) // walk 2: 6 s spent, the cap
	c.StepStarting(ctx, pkgStep(steps.IDTmpfiles, "systemd-tmpfiles", "--clean"))
	os.Remove(filepath.Join(w.tmp, "fa.pkg.tar.zst"))
	c.StepDone(ctx, steps.IDTmpfiles, steps.OK)
	r := c.Finish(ctx, FinishInfo{})
	a := areas(r)
	if !a[AreaYay].Known || a[AreaYay].Bytes == 0 {
		t.Errorf("yay row %+v, want measured", a[AreaYay])
	}
	if a[AreaTmpfiles].Known {
		t.Errorf("tmp row %+v, want unknown past the cap", a[AreaTmpfiles])
	}
	if !r.FreedPartial || r.FreedBytes != a[AreaYay].Bytes {
		t.Errorf("freed %d partial %v, want the known row only", r.FreedBytes, r.FreedPartial)
	}
}

// TestCollectorYayThenUserCacheNoDoubleCount: yay's folder lives inside
// ~/.cache; the user-cache step is measured after yay's step ended, so what
// yay freed is not counted again, and the total is the rows' sum.
func TestCollectorYayThenUserCacheNoDoubleCount(t *testing.T) {
	w := newWorld(t)
	home := filepath.Join(t.TempDir(), ".cache")
	w.yay = filepath.Join(home, "yay")
	os.MkdirAll(w.yay, 0o755)
	w.fill(w.yay, 3, 100<<10)
	w.fill(home, 2, 50<<10)
	cfg := w.config()
	cfg.UserCacheDir = home
	cfg.YayDir = func() string { return w.yay }
	ctx := context.Background()
	c := New(cfg, "x", time.Now())
	c.StepStarting(ctx, pkgStep(steps.IDYayCache, "yay", "-Sc", "--aur"))
	w.empty(w.yay)
	c.StepDone(ctx, steps.IDYayCache, steps.OK)
	c.StepStarting(ctx, pkgStep(steps.IDUserCache, "bash", "-c", "rm"))
	os.Remove(filepath.Join(home, "fa.pkg.tar.zst"))
	os.Remove(filepath.Join(home, "fb.pkg.tar.zst"))
	c.StepDone(ctx, steps.IDUserCache, steps.OK)
	r := c.Finish(ctx, FinishInfo{})
	a := areas(r)
	if got := a[AreaUserCache].Bytes; got < 100<<10 || got >= 3*100<<10 {
		t.Errorf("user cache row %d: must hold only its own 100 KB, not yay's 300 KB", got)
	}
	if r.FreedBytes != a[AreaYay].Bytes+a[AreaUserCache].Bytes {
		t.Errorf("total %d is not the sum of the rows", r.FreedBytes)
	}
}
