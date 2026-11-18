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
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	glibv2 "github.com/diamondburned/gotk4/pkg/glib/v2"
)

// shotsEnv names the screenshot directory; empty means no screenshots.
const shotsEnv = "ARCHMAID_GUI_SHOTS"

// shoot writes the window as it is now to $ARCHMAID_GUI_SHOTS/<scenario>-
// <what>.png, if that is set. Through a cairo renderer of its own (what
// archmaid-gui renders with; the headless display's renderer is
// Broadway's), from what GTK draws (a WidgetPaintable of the window,
// snapshotted at its size), so on any backend. A
// dialog shown inside the window is in the shot; one that didn't fit and
// floats as a window of its own isn't.
func shoot(w *Window, scenario, what string) {
	dir := os.Getenv(shotsEnv)
	if dir == "" {
		return
	}
	// A shot that was asked for is written before the child exits or it is
	// an error in the child's report (shotFailed), never silently skipped:
	// a missing PNG used to surface only as the parent's "no such file".
	if w.closed {
		shotFailed("%s-%s: the window was already closed", scenario, what)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		shotFailed("%s-%s: %v", scenario, what, err)
		return
	}
	path := filepath.Join(dir, scenario+"-"+what+".png")
	// The window has no size until GTK has allocated it; under load the
	// first frames can come late, so wait for the allocation by running the
	// main loop (a few short slices) instead of skipping the shot.
	var err error
	for try := 0; try < shotTries; try++ {
		if w.win.Width() <= 0 || w.win.Height() <= 0 {
			err = fmt.Errorf("the window has no size yet (%dx%d)", w.win.Width(), w.win.Height())
		} else if err = savePNG(w.win, path); err == nil {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "shot %s-%s: try %d at %d: %v\n", scenario, what, try+1, time.Now().UnixMilli()%100000, err)
		}
		pumpMain(shotWait)
	}
	shotFailed("%s-%s: %v", scenario, what, err)
}

// shotTries and shotWait: how often and for how long shoot lets the main
// loop run between attempts when the window can't be drawn yet.
const (
	shotTries = 20
	shotWait  = 50 * time.Millisecond
)

// pumpMain runs the default main context for d, so allocation and frame
// clock work that is waiting can happen.
func pumpMain(d time.Duration) {
	ctx := glibv2.MainContextDefault()
	for end := time.Now().Add(d); time.Now().Before(end); {
		if !ctx.Iteration(false) {
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// shotsPending counts end-of-run shots scheduled and not yet taken. The
// harness closes the window a moment after a run ends; the end shot is taken
// a moment after it too, and under load (both timers overdue, a snapshot that
// needs a retry) the close could come first and the shot was lost.
// afterShots runs f once none is pending.
var shotsPending atomic.Int32

// afterShots runs f ms from now, or as soon after as no shot is pending.
func afterShots(ms uint, f func()) {
	var try func()
	try = func() {
		if shotsPending.Load() > 0 {
			coreglib.TimeoutAdd(10, try)
			return
		}
		f()
	}
	coreglib.TimeoutAdd(ms, try)
}

var (
	shotMu   sync.Mutex
	shotErrs []string
)

// shotFailed records a shot that was requested and not taken; the child's
// report carries it (harness_test.go) and the parent test fails on it.
func shotFailed(format string, args ...any) {
	shotMu.Lock()
	defer shotMu.Unlock()
	shotErrs = append(shotErrs, "screenshot "+fmt.Sprintf(format, args...))
}

// takeShotErrors returns and clears the recorded shot failures.
func takeShotErrors() []string {
	shotMu.Lock()
	defer shotMu.Unlock()
	e := shotErrs
	shotErrs = nil
	return e
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
	child(t, "caches-no")
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
	for _, want := range []string{"caches-no-start.png", "caches-no-prompt-1.png", "caches-no-end-1.png"} {
		if !slices.Contains(names, want) {
			t.Errorf("shots %q: no %s", names, want)
		}
	}
	// A shot that can't be taken is an error naming the step, no file.
	child(t, "cov-savepng")
}
