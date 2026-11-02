package aurbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reviewed file lives in the fixed ~/.local/state/archmaid, as every other
// state file; XDG_STATE_HOME is not honoured.
func TestDefaultReviewedIgnoresXDGStateHome(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", xdg)
	r := DefaultReviewed()
	want := filepath.Join(home, ".local", "state", "archmaid", "aur-reviewed.json")
	if r.Path != want {
		t.Fatalf("Path %q; want %q", r.Path, want)
	}
	c := strings.Repeat("c", 40)
	if err := r.Mark(map[string]string{"app": c}); err != nil {
		t.Fatal(err)
	}
	if DefaultReviewed().Get("app") != c {
		t.Fatal("not read back from the default path")
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v, %v; want 0600", fi, err)
	}
	if es, _ := os.ReadDir(xdg); len(es) != 0 {
		t.Fatalf("XDG_STATE_HOME was written to: %v", es)
	}
}

func TestReviewedWithoutPathRefusesToWrite(t *testing.T) {
	r := &Reviewed{}
	if err := r.Mark(map[string]string{"app": strings.Repeat("a", 40)}); err == nil {
		t.Fatal("Mark with no Path must fail, not write to the working directory")
	}
}
