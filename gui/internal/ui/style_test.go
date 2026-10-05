package ui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/runview"
)

// TestStateClass: one am-state-* class per row state, spaces as dashes.
func TestStateClass(t *testing.T) {
	for s, want := range map[runview.RowState]string{
		runview.RowOK: "am-state-ok", runview.RowAuthRefused: "am-state-auth-refused", runview.RowNotRun: "am-state-not-run",
	} {
		if got := stateClass(s); got != want {
			t.Errorf("%v: %q, want %q", s, got, want)
		}
	}
	if len(stateClasses) != int(runview.RowNotRun)+2 {
		t.Errorf("stateClasses %q", stateClasses)
	}
}

// TestGUIDMSColoursWin: style.css sits under the user's stylesheet. A
// scratch XDG_CONFIG_HOME/gtk-4.0/gtk.css that defines libadwaita's
// surface colours, as DMS's dank-colors.css does, colours the window: most
// of the idle Overview is that colour, so nothing of ours overrides it.
func TestGUIDMSColoursWin(t *testing.T) {
	needDisplay(t)
	cfg, shots := t.TempDir(), t.TempDir()
	css := ""
	for _, name := range []string{"window_bg_color", "view_bg_color", "headerbar_bg_color", "card_bg_color", "sidebar_bg_color", "dialog_bg_color"} {
		css += "@define-color " + name + " #ff00ff;\n"
	}
	if err := os.MkdirAll(filepath.Join(cfg, "gtk-4.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "gtk-4.0", "gtk.css"), []byte(css), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(shotsEnv, shots)
	child(t, "idle", "XDG_CONFIG_HOME="+cfg)
	b, err := os.ReadFile(filepath.Join(shots, "idle-start.png"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if f := magentaShare(img); f < 0.5 {
		t.Errorf("%.0f%% of the window is the user's colour, want most of it: archmaid's CSS overrides the user's named colours", f*100)
	}
}

// magentaShare is the share of img's pixels that are near #ff00ff.
func magentaShare(img image.Image) float64 {
	r := img.Bounds()
	n, hit := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y += 4 {
		for x := r.Min.X; x < r.Max.X; x += 4 {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			n++
			if cr > 0xe000 && cg < 0x2000 && cb > 0xe000 {
				hit++
			}
		}
	}
	return float64(hit) / float64(n)
}
