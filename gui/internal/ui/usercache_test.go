package ui

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Clear user cache cell's measurement, without a display: the line text,
// the failure paths (unreadable folder, cancelled or timed-out walk) and the
// stale-generation bookkeeping.

// randomFile writes n random bytes (zeros could be stored compressed or
// sparse and report fewer blocks).
func randomFile(t *testing.T, path string, n int) {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheLineOfReadable(t *testing.T) {
	dir := t.TempDir()
	randomFile(t, filepath.Join(dir, "blob"), 3<<20)
	randomFile(t, filepath.Join(dir, ".hidden"), 3<<20) // the glob skips it
	// One 3 MiB entry; dir itself isn't counted, only its entries, so the
	// result is exactly 3.0M on any filesystem (random bytes: no compression).
	if got := cacheLineOf(context.Background(), dir); got != "3.0M in ~/.cache" {
		t.Errorf("line %q, want the one 3 MiB entry", got)
	}
}

func TestCacheLineOfEmpty(t *testing.T) {
	dir := t.TempDir()
	randomFile(t, filepath.Join(dir, ".only-hidden"), 1<<20)
	if got := cacheLineOf(context.Background(), dir); got != "Empty" {
		t.Errorf("line %q, want Empty", got)
	}
}

func TestCacheLineOfMissing(t *testing.T) {
	if got := cacheLineOf(context.Background(), filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("line %q for a missing folder, want hidden", got)
	}
}

// A ~/.cache with mode 000 can't be listed: hidden, never a number.
func TestCacheLineOfPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 folder")
	}
	dir := filepath.Join(t.TempDir(), ".cache")
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	randomFile(t, filepath.Join(dir, "app", "blob"), 1<<20)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if got := cacheLineOf(context.Background(), dir); got != "" {
		t.Errorf("line %q for an unreadable folder, want hidden", got)
	}
}

// A walk whose context has ended (the 5 s limit, or a cancel) shows nothing,
// not the partial size it got to.
func TestCacheLineOfCancelledAndTimedOut(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		randomFile(t, filepath.Join(dir, n), 1<<20)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := cacheLineOf(cancelled, dir); got != "" {
		t.Errorf("cancelled: line %q, want hidden", got)
	}
	expired, stop := context.WithTimeout(context.Background(), time.Nanosecond)
	defer stop()
	<-expired.Done()
	if got := cacheLineOf(expired, dir); got != "" {
		t.Errorf("timed out: line %q, want hidden", got)
	}
}

func TestCacheLineOfEmptyFolderIsNotCancelled(t *testing.T) {
	// A control for the two above: the same call with a live context on an
	// empty folder says Empty, so "" there is the context's doing.
	if got := cacheLineOf(context.Background(), t.TempDir()); got != "Empty" {
		t.Errorf("line %q, want Empty", got)
	}
}

// A measurement that began before a run's end never shows, and it doesn't
// stop the next one from showing.
func TestCacheMeterStaleResultNeverShows(t *testing.T) {
	var m cacheMeter
	if !m.request() {
		t.Fatal("first request not started")
	}
	gen := m.gen
	m.invalidate() // a run ended while it went
	if m.request() {
		t.Fatal("a request while one goes started a second measurement")
	}
	changed, again := m.finish(gen, "9.9G in ~/.cache")
	if changed || m.line != "" {
		t.Errorf("stale result shown: changed %v line %q", changed, m.line)
	}
	if !again {
		t.Error("the coalesced request was lost")
	}
	if !m.request() {
		t.Fatal("no measurement after the stale one ended")
	}
	if changed, again = m.finish(m.gen, "1.0M in ~/.cache"); !changed || again || m.line != "1.0M in ~/.cache" {
		t.Errorf("fresh result: changed %v again %v line %q", changed, again, m.line)
	}
}

func TestCacheMeterInvalidateHidesShownLine(t *testing.T) {
	m := cacheMeter{line: "3.0M in ~/.cache"}
	if !m.invalidate() || m.line != "" {
		t.Errorf("line %q after invalidate", m.line)
	}
	if m.invalidate() {
		t.Error("invalidating a hidden line reported a change")
	}
}
