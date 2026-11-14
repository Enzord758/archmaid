package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A partly ignored check keeps its detail, cut to DetailMax with the count.
func TestApplyIgnoresClipsTheDetail(t *testing.T) {
	long := strings.Repeat("x", DetailMax)
	c := applyIgnores(Check{ID: "c", State: Look, Detail: long, Items: []Item{{"a", "a"}, {"b", "b"}}}, map[string]time.Time{"a": t0})
	if r := []rune(c.Detail); len(r) != DetailMax || r[len(r)-1] != '…' || c.State != Look || len(c.Items) != 1 {
		t.Errorf("%d runes, state %v, items %v", len(r), c.State, c.Items)
	}
}

func TestAgoLong(t *testing.T) {
	h := time.Hour
	for d, want := range map[time.Duration]string{
		10 * time.Minute: "under an hour", 90 * time.Minute: "1 hour", 5 * h: "5 hours", 30 * time.Hour: "1 day", 59 * 24 * h: "59 days",
		61 * 24 * h: "2 months", 245 * 24 * h: "8 months", 729 * 24 * h: "24 months", 800 * 24 * h: "2 years",
	} {
		if got := agoLong(d); got != want {
			t.Errorf("agoLong(%v) = %q, want %q", d, got, want)
		}
	}
}

// What can't be read or written is an empty list or an error, never a panic.
func TestIgnoreStoreFailures(t *testing.T) {
	// A file we can't open reads as empty.
	dir := t.TempDir()
	p := filepath.Join(dir, IgnoreFile)
	os.WriteFile(p, []byte(`{"v":1,"ignored":{"a":"2026-11-09T12:00:00Z"}}`), 0o600)
	os.Chmod(p, 0o200)
	if os.Getuid() != 0 {
		if got := (&IgnoreStore{Dir: dir}).Load(); len(got) != 0 {
			t.Errorf("an unreadable file was read: %v", got)
		}
	}
	// A directory we can't write to: Ignore fails, nothing is left behind.
	ro := t.TempDir()
	os.Chmod(ro, 0o500)
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	sub := filepath.Join(ro, "state")
	if os.Getuid() != 0 {
		if err := (&IgnoreStore{Dir: sub}).Ignore("a"); err == nil {
			t.Error("Ignore under a read-only folder succeeded")
		}
	}
	// A temp-file name can't be made inside a directory that vanished.
	gone := filepath.Join(t.TempDir(), "state")
	s := &IgnoreStore{Dir: gone}
	if err := s.Ignore("a"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(gone)
	if got := s.Load(); len(got) != 0 {
		t.Errorf("a removed file was read: %v", got)
	}
}
