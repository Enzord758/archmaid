package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGlassRoundTrip: the glass switch and the blur row are kept in
// gui.json and read back; off and auto write nothing (the default file
// stays as it was), and an unknown blur value reads as auto.
func TestGlassRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archmaid", "gui.json")
	want := Settings{Glass: true, Blur: BlurOff, DryRun: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if err := Save(path, Settings{Blur: BlurAuto}); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.Glass || s.BlurMode() != BlurAuto {
		t.Errorf("default glass: %+v, %v", s, err)
	}
	if got := (Settings{Blur: "sparkly"}).BlurMode(); got != BlurAuto {
		t.Errorf("BlurMode(unknown) = %q, want auto", got)
	}
	// A file from before the keys existed is the default look.
	old := Settings{DryRun: true}
	if old.Glass || old.BlurMode() != BlurAuto {
		t.Errorf("zero value is glass %v blur %q", old.Glass, old.BlurMode())
	}
	if err := Save(path, Settings{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "glass") || strings.Contains(string(b), "blur") {
		t.Errorf("the default file holds glass keys: %s", b)
	}
}

// TestBlurBehind: Auto means translucency only where Hyprland is (its
// session variable, faked here); On and Off force either way.
func TestBlurBehind(t *testing.T) {
	hypr := func(k string) string {
		if k == HyprlandEnv {
			return "abc123"
		}
		return ""
	}
	none := func(string) string { return "" }
	for _, c := range []struct {
		blur string
		env  func(string) string
		want bool
	}{
		{"", hypr, true}, {BlurAuto, hypr, true},
		{"", none, false}, {BlurAuto, none, false},
		{BlurOn, none, true}, {BlurOn, hypr, true},
		{BlurOff, hypr, false}, {BlurOff, none, false},
		{"sparkly", hypr, true}, {"sparkly", none, false},
	} {
		if got := (Settings{Glass: true, Blur: c.blur}).BlurBehind(c.env); got != c.want {
			t.Errorf("blur %q, hyprland %v: %v, want %v", c.blur, c.env(HyprlandEnv) != "", got, c.want)
		}
	}
}
