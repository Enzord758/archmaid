package sysstat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEntriesUsage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Empty: nothing for `rm -rf dir/*` to free (the dir's own blocks don't count).
	if s, n, err := EntriesUsage(ctx, dir); err != nil || n != 0 || s != "0" {
		t.Fatalf("empty dir: %q %d %v", s, n, err)
	}

	// 1 MiB in a nested file, plus two directories' own blocks.
	sub := filepath.Join(dir, "app", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "blob"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	s, n, err := EntriesUsage(ctx, dir)
	if err != nil || n < 1<<20 || n > 1<<20+64<<10 || s != humanCeil(n) {
		t.Fatalf("one file: %q %d %v", s, n, err)
	}
	if s != "1.1M" && s != "1.0M" {
		t.Fatalf("size text %q", s)
	}

	// A dot entry at the top is not removed by the glob, so not counted.
	if err := os.WriteFile(filepath.Join(dir, ".keep"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, n2, err := EntriesUsage(ctx, dir); err != nil || n2 != n {
		t.Fatalf("hidden entry counted: %d vs %d (%v)", n2, n, err)
	}

	// An unreadable subdirectory is skipped, like du.
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, _, err := EntriesUsage(ctx, dir); err != nil {
		t.Fatalf("unreadable subdir failed the walk: %v", err)
	}
}

func TestEntriesUsageErrors(t *testing.T) {
	if s, n, err := EntriesUsage(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil || s != "" || n != 0 {
		t.Fatalf("missing dir: %q %d %v", s, n, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, n, err := EntriesUsage(ctx, dir); err != context.Canceled || s != "" || n != 0 {
		t.Fatalf("cancelled: %q %d %v", s, n, err)
	}
	// A file is not a directory: unreadable, nothing shown.
	if _, _, err := EntriesUsage(context.Background(), filepath.Join(dir, "f")); err == nil {
		t.Fatal("a file as dir gave no error")
	}
}
