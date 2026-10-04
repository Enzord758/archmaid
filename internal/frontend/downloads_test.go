package frontend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Which entries are old and the deletion's safety are tested in
// internal/downloads, and the TUI's delete through its screens in
// internal/tui. These tests pin the step glue: the abort record and the
// in-flight count.

// oldFiles makes names in a scratch folder and returns the plan to delete
// them, with options whose clock puts them all past the cutoff.
func oldFiles(t *testing.T, names ...string) (downloads.Plan, func() downloads.Options) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(downloads.MaxAge + time.Hour)
	opts := func() downloads.Options { return downloads.Options{Now: func() time.Time { return later }} }
	plan, err := downloads.Scan(dir, later, opts())
	if err != nil || len(plan.Names) != len(names) {
		t.Fatalf("scan = %+v, %v; want %q", plan, err, names)
	}
	return plan, opts
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// cleanupLog is store's cleanup log ("" when there is none).
func cleanupLog(store *logstore.Store) string {
	log, _ := store.Read(logstore.Cleanup)
	return log
}

// TestDownloadsOptions: the environment's part of the resolver's input,
// with the front end's folder and dry-run flag.
func TestDownloadsOptions(t *testing.T) {
	t.Setenv(downloads.EnvVar, "/env/dl")
	t.Setenv("HOME", "/home/x")
	t.Setenv("XDG_CONFIG_HOME", "/home/x/.cfg")
	o := DownloadsOptions("/flag/dl", true)
	if o.Flag != "/flag/dl" || o.Env != "/env/dl" || o.Home != "/home/x" || o.XDGConfigHome != "/home/x/.cfg" || !o.DryRun {
		t.Fatalf("options = %+v", o)
	}
	if o.PasswdHome != downloads.PasswdHome() || o.Now != nil || o.EntryHook != nil {
		t.Fatalf("passwd home %q, Now set %v, hook set %v", o.PasswdHome, o.Now != nil, o.EntryHook != nil)
	}
}

// TestDeleteStepCommitted: a run whose result the engine takes and commits
// deletes, logs nothing itself (the engine logs the OK entry) and leaves
// nothing in flight.
func TestDeleteStepCommitted(t *testing.T) {
	plan, opts := oldFiles(t, "a.txt")
	store := testStore(t, false)
	var d Deletions
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := d.Step(plan, store, opts)(ctx, steps.Env{})
	if r.Err != nil || !strings.Contains(r.Output, "deleted: a.txt") || exists(filepath.Join(plan.Dir, "a.txt")) {
		t.Fatalf("result %+v; a.txt there: %v", r, exists(filepath.Join(plan.Dir, "a.txt")))
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	cancel(steps.ErrResultTaken)
	d.Wait()
	if log := cleanupLog(store); log != "" {
		t.Fatalf("a committed run logged itself:\n%s", log)
	}
}

// TestDeleteStepAbortMidDeletion: an abort partway through stops before
// the next entry, and the record names exactly what was deleted.
func TestDeleteStepAbortMidDeletion(t *testing.T) {
	plan, base := oldFiles(t, "a.txt", "b.txt", "c.txt", "d.txt")
	ctx, abort := context.WithCancelCause(context.Background())
	defer abort(nil)
	opts := func() downloads.Options {
		o := base()
		o.EntryHook = func(name string) {
			if name == "c.txt" {
				abort(nil)
			}
		}
		return o
	}
	store := testStore(t, false)
	var d Deletions
	d.Step(plan, store, opts)(ctx, steps.Env{})
	d.Wait()
	log := cleanupLog(store)
	for _, want := range []string{"ABORT: Old downloads deleted (aborted)", "deleted: a.txt", "deleted: b.txt", "stopped: aborted before c.txt"} {
		if !strings.Contains(log, want) {
			t.Errorf("cleanup log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "deleted: c.txt") || strings.Count(log, ": Old downloads deleted") != 1 {
		t.Fatalf("want one entry naming only a.txt and b.txt:\n%s", log)
	}
	if !exists(filepath.Join(plan.Dir, "c.txt")) || !exists(filepath.Join(plan.Dir, "d.txt")) {
		t.Fatal("c.txt and d.txt should be kept")
	}
}

// TestDeleteStepAbortAfterTheRun: an abort that lands after the run but
// before the engine took the result still leaves the record, once.
func TestDeleteStepAbortAfterTheRun(t *testing.T) {
	plan, opts := oldFiles(t, "a.txt", "b.txt")
	store := testStore(t, false)
	var d Deletions
	ctx, abort := context.WithCancelCause(context.Background())
	d.Step(plan, store, opts)(ctx, steps.Env{})
	abort(nil)
	d.Wait()
	if log := cleanupLog(store); !strings.Contains(log, "ABORT: Old downloads deleted (aborted)") ||
		!strings.Contains(log, "deleted: b.txt") || strings.Count(log, ": Old downloads deleted") != 1 {
		t.Fatalf("want one abort entry naming every deletion:\n%s", log)
	}
}

// TestDeleteStepResultTaken: a result taken without its commit (refused,
// or gated) is released with steps.ErrResultTaken, which is no abort.
func TestDeleteStepResultTaken(t *testing.T) {
	plan, opts := oldFiles(t, "a.txt")
	store := testStore(t, false)
	var d Deletions
	ctx, release := context.WithCancelCause(context.Background())
	d.Step(plan, store, opts)(ctx, steps.Env{})
	release(steps.ErrResultTaken)
	d.Wait()
	if log := cleanupLog(store); strings.Contains(log, "ABORT") {
		t.Fatalf("a taken result logged an abort:\n%s", log)
	}
}

// TestDeleteStepDryRunAndBackground: a dry run deletes nothing and arms
// no record; a ctx that can't end (context.Background) arms none either.
func TestDeleteStepDryRunAndBackground(t *testing.T) {
	plan, base := oldFiles(t, "a.txt")
	store := testStore(t, false)
	var d Deletions
	dry := func() downloads.Options { o := base(); o.DryRun = true; return o }
	ctx, abort := context.WithCancelCause(context.Background())
	if r := d.Step(plan, store, dry)(ctx, steps.Env{}); r.Commit != nil || !exists(filepath.Join(plan.Dir, "a.txt")) {
		t.Fatalf("dry run: commit set %v, a.txt kept %v", r.Commit != nil, exists(filepath.Join(plan.Dir, "a.txt")))
	}
	abort(nil)
	if r := d.Step(plan, store, base)(context.Background(), steps.Env{}); r.Commit != nil || exists(filepath.Join(plan.Dir, "a.txt")) {
		t.Fatalf("background: commit set %v, a.txt kept %v", r.Commit != nil, exists(filepath.Join(plan.Dir, "a.txt")))
	}
	d.Wait()
	if log := cleanupLog(store); log != "" {
		t.Fatalf("logged:\n%s", log)
	}
}

// TestStepAborted: only a cancel that isn't ErrResultTaken is an abort.
func TestStepAborted(t *testing.T) {
	live, stop := context.WithCancelCause(context.Background())
	if StepAborted(live) {
		t.Fatal("a live ctx is not aborted")
	}
	stop(nil)
	if !StepAborted(live) {
		t.Fatal("a plain cancel is an abort")
	}
	taken, release := context.WithCancelCause(context.Background())
	release(steps.ErrResultTaken)
	if StepAborted(taken) {
		t.Fatal("ErrResultTaken is not an abort")
	}
	wrapped, release2 := context.WithCancelCause(context.Background())
	release2(errors.Join(steps.ErrResultTaken))
	if StepAborted(wrapped) {
		t.Fatal("a wrapped ErrResultTaken is not an abort")
	}
}
