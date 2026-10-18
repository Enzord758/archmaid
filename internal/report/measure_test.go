package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeN(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDirSize: a folder's disk usage, du-style, with package files counted
// and signatures not; a missing folder is a known zero.
func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "a-1-1-x86_64.pkg.tar.zst"), 8192)
	writeN(t, filepath.Join(dir, "a-1-1-x86_64.pkg.tar.zst.sig"), 100)
	writeN(t, filepath.Join(dir, "sub", "b-1-1-x86_64.pkg.tar.xz"), 4096)
	s := DirSize(context.Background(), dir)
	if !s.OK || s.Pkgs != 2 || s.Bytes < 8192+4096 {
		t.Errorf("size %+v, want ok, 2 packages and at least 12288 bytes", s)
	}
	if m := DirSize(context.Background(), filepath.Join(dir, "none")); !m.OK || m.Bytes != 0 {
		t.Errorf("a missing folder is %+v, want a known 0", m)
	}
}

// TestDirSizeHardlink: a file with two names is counted once.
func TestDirSizeHardlink(t *testing.T) {
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "one"), 65536)
	one := DirSize(context.Background(), dir)
	if err := os.Link(filepath.Join(dir, "one"), filepath.Join(dir, "two")); err != nil {
		t.Skip("no hard links here:", err)
	}
	if two := DirSize(context.Background(), dir); two.Bytes != one.Bytes {
		t.Errorf("a hard link counted twice: %d then %d", one.Bytes, two.Bytes)
	}
}

// TestDirSizeUnreadable: a target the user can't list is unknown, never a
// guessed zero; an unreadable subfolder is skipped.
func TestDirSizeUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "ok", "f"), 4096)
	locked := filepath.Join(dir, "locked")
	writeN(t, filepath.Join(locked, "f"), 4096)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if s := DirSize(context.Background(), locked); s.OK {
		t.Errorf("an unreadable target measured %+v, want unknown", s)
	}
	if s := DirSize(context.Background(), dir); !s.OK || s.Bytes < 4096 {
		t.Errorf("a tree with an unreadable subfolder measured %+v, want the readable part", s)
	}
}

// TestDirSizeContext: a walk that outlives its context is unknown.
func TestDirSizeContext(t *testing.T) {
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "f"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s := DirSize(ctx, dir); s.OK {
		t.Errorf("a cancelled walk measured %+v, want unknown", s)
	}
}

// TestFreed: before minus after, clamped at zero, unknown when either side
// is.
func TestFreed(t *testing.T) {
	ok := func(b uint64) Size { return Size{Bytes: b, OK: true} }
	for _, c := range []struct {
		name          string
		before, after Size
		bytes         uint64
		known         bool
	}{
		{"freed", ok(100), ok(40), 60, true},
		{"nothing", ok(100), ok(100), 0, true},
		{"grew while the step ran: clamped", ok(100), ok(130), 0, true},
		{"before unknown", Size{}, ok(1), 0, false},
		{"after unknown", ok(1), Size{}, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, k := Freed(c.before, c.after)
			if b != c.bytes || k != c.known {
				t.Errorf("Freed = %d, %v; want %d, %v", b, k, c.bytes, c.known)
			}
		})
	}
}

// TestMeasurerBudget: once the run's measuring time is spent, every walk is
// unknown at once.
func TestMeasurerBudget(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	m := &measurer{each: time.Second, deadline: now.Add(10 * time.Second), now: func() time.Time { return now }}
	if s := m.size(context.Background(), dir); !s.OK {
		t.Fatal("a walk within the budget is unknown")
	}
	now = now.Add(11 * time.Second)
	if s := m.size(context.Background(), dir); s.OK {
		t.Error("a walk past the budget was measured")
	}
}
