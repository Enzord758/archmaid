package held

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCooldownAtNeedsAFreshCache(t *testing.T) {
	checked := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if got := parseChecked([]byte(heldCache)); got.IsZero() {
		t.Fatal("the cache's checked time was not read")
	}
	if !parseChecked([]byte("nope")).IsZero() || !parseChecked([]byte(`{"checked":"yesterday"}`)).IsZero() {
		t.Error("a bad checked time was taken")
	}
	s := Set{Checked: checked, Cooldown: []Pkg{{Name: "a"}}}
	for _, c := range []struct {
		at   time.Time
		want int
	}{
		{checked.Add(time.Hour), 1},
		{checked.Add(CacheFresh), 1},
		{checked.Add(CacheFresh + time.Second), 0},
		{checked.Add(-30 * time.Second), 1}, // a little clock skew
		{checked.Add(-time.Hour), 0},        // dated in the future
	} {
		if got := len(s.CooldownAt(c.at)); got != c.want {
			t.Errorf("at %v: %d rows, want %d", c.at.Sub(checked), got, c.want)
		}
	}
	if len((Set{Cooldown: []Pkg{{Name: "a"}}}).CooldownAt(checked)) != 0 {
		t.Error("a cache with no checked time counts")
	}
}

func writeCache(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadSetsChecked(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, heldCache)
	if Read(dir).Checked.IsZero() {
		t.Error("Read left Checked empty")
	}
}
