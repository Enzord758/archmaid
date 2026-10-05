package ui

// Screenshots from the headless harness (P8 row 1): with
// ARCHMAID_GUI_SHOTS=<dir>, every scenario writes PNGs of the window to
// dir, named <scenario>-<moment>.png: "start" once the first frames have
// settled, "prompt-<id>" when a dialog is up (before it is answered), and
// "end-<n>" after the n-th run finished. Off by default. For comparing the
// GUI with the mockup (Dev 5), and for a before/after of every P8 row:
//
//	ARCHMAID_GUI_SHOTS=$(mktemp -d) gui/scripts/headless.sh
//
// The capture (shot.go) is built only with -tags shots: headless.sh adds
// it to every group when ARCHMAID_GUI_SHOTS is set, and always to the
// group that runs TestGUIShots and TestGUIDMSColoursWin.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// shotsEnv names the screenshot directory; empty means no screenshots.
const shotsEnv = "ARCHMAID_GUI_SHOTS"

// shoot writes the window as it is now to $ARCHMAID_GUI_SHOTS/<scenario>-
// <what>.png, if that is set. Through the window's own renderer, as GTK
// draws it (a WidgetPaintable of the window, snapshotted at its size). A
// dialog shown inside the window is in the shot; one that didn't fit and
// floats as a window of its own isn't.
func shoot(w *Window, scenario, what string) {
	dir := os.Getenv(shotsEnv)
	if dir == "" || w.closed {
		return
	}
	width, height := w.win.Width(), w.win.Height()
	if width <= 0 || height <= 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = savePNG(w.win, filepath.Join(dir, scenario+"-"+what+".png"))
}

// needShots skips a test that reads screenshots in a binary built without
// -tags shots.
func needShots(t *testing.T) {
	t.Helper()
	if !shotsBuilt {
		t.Skip("needs screenshots: build with -tags shots (headless.sh's engine-stops group does)")
	}
}

// pngSize is a PNG's width and height from its IHDR, or ok false.
func pngSize(b []byte) (w, h int, ok bool) {
	if len(b) < 24 || !bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")) || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}

// TestGUIShots: with ARCHMAID_GUI_SHOTS set, a scenario with a confirm
// writes its start, prompt and end shots as PNGs of the window's size.
func TestGUIShots(t *testing.T) {
	needDisplay(t)
	needShots(t)
	dir := t.TempDir()
	t.Setenv(shotsEnv, dir)
	child(t, "usercache-no")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if w, h, ok := pngSize(b); !ok || w < 300 || h < 200 {
			t.Errorf("%s: not a window-sized PNG (%dx%d, png %v)", e.Name(), w, h, ok)
		}
	}
	for _, want := range []string{"usercache-no-start.png", "usercache-no-prompt-1.png", "usercache-no-end-1.png"} {
		if !slices.Contains(names, want) {
			t.Errorf("shots %q: no %s", names, want)
		}
	}
}
