package aurbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	r := &Reviewed{Path: filepath.Join(dir, "aur-reviewed.json")}
	if r.Get("app") != "" {
		t.Fatal("nothing is reviewed yet")
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 64)
	if err := r.Mark(map[string]string{"app": a}); err != nil {
		t.Fatal(err)
	}
	if err := r.Mark(map[string]string{"lib": b, "app": b}); err != nil {
		t.Fatal(err)
	}
	if r.Get("app") != b || r.Get("lib") != b {
		t.Fatalf("app %q lib %q", r.Get("app"), r.Get("lib"))
	}
	fi, err := os.Stat(r.Path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v, %v; want 0600", fi, err)
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v; want 0700", di.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".aur-reviewed-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	// a second handle on the same file sees it
	if (&Reviewed{Path: r.Path}).Get("lib") != b {
		t.Fatal("not persisted")
	}
}

// TestReviewedBadState: a corrupt or hostile file reads as nothing reviewed
// (the next review is a first one, the safe direction); bad entries are
// dropped and never reach a git command.
func TestReviewedBadState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	r := &Reviewed{Path: p}
	good := strings.Repeat("a", 40)
	for name, text := range map[string]string{
		"junk":          "{not json",
		"wrong version": `{"version":2,"bases":{"app":"` + good + `"}}`,
		"no bases":      `{"version":1}`,
	} {
		os.WriteFile(p, []byte(text), 0o600)
		if got := r.Get("app"); got != "" {
			t.Errorf("%s: Get = %q", name, got)
		}
	}
	os.WriteFile(p, []byte(`{"version":1,"bases":{"app":"`+good+`","../x":"`+good+`","evil":"--output=/x","short":"abc"}}`), 0o600)
	if r.Get("app") != good || r.Get("../x") != "" || r.Get("evil") != "" || r.Get("short") != "" {
		t.Fatal("bad entries were not dropped")
	}
	if err := r.Mark(map[string]string{"app": "HEAD"}); err == nil {
		t.Fatal("a ref name was recorded as a commit")
	}
	if err := r.Mark(map[string]string{"a/b": good}); err == nil {
		t.Fatal("a bad base was recorded")
	}
	// a state path in an unwritable place fails, not panics
	bad := &Reviewed{Path: filepath.Join(p, "sub", "r.json")} // p is a file
	if err := bad.Mark(map[string]string{"app": good}); err == nil {
		t.Fatal("Mark under a file succeeded")
	}
}
