package testenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTouchedFlagsNewEntries(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
	before := snapshot(dir)
	start := time.Now()
	if got := touched(dir, before, start); len(got) != 0 {
		t.Fatalf("untouched dir flagged: %v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "20260101-0000"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := touched(dir, before, start); len(got) == 0 {
		t.Fatal("new run dir not flagged")
	}
}

func TestMissingDirIsClean(t *testing.T) {
	d := filepath.Join(t.TempDir(), "nope")
	if got := touched(d, snapshot(d), time.Now()); len(got) != 0 {
		t.Fatalf("flagged %v", got)
	}
}
