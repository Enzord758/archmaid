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
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
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
	// The snapshot is taken inside the frame clock's after-paint signal, once
	// it is not empty, for up to shotCap (saveAfterPaint says why). The
	// retries below are only for a shot that was still empty after the cap: a
	// safety net, counted, logged, and a shot that needed one fails the test
	// (shotRetried).
	var err, first error
	for try := 0; try < shotTries; try++ {
		var frames int
		frames, err = saveAfterPaint(w, path)
		if err == nil {
			noteShotFrames(scenario, what, frames)
			if try > 0 {
				shotRetried(scenario, what, try, first)
			}
			return
		}
		if try == 0 {
			first = err
		}
		fmt.Fprintf(os.Stderr, "SHOT RETRY %s-%s: try %d failed: %v\n", scenario, what, try+1, err)
		pumpMain(shotWait)
	}
	shotFailed("%s-%s: %v", scenario, what, err)
}

// shotRetries counts the shots that needed more than one try, in this
// process. It must stay 0: shotRetried fails the test.
var shotRetries atomic.Int32

func shotRetried(scenario, what string, tries int, first error) {
	shotRetries.Add(1)
	fmt.Fprintf(os.Stderr, "SHOT RETRY %s-%s: taken only on try %d\n", scenario, what, tries+1)
	shotFailed("%s-%s: needed %d retries (first error: %v); the window was still mid-update (saveAfterPaint)", scenario, what, tries, first)
}

// saveAfterPaint writes the window's PNG from inside the frame clock's
// after-paint signal, frame after frame, until the snapshot is not empty:
// at the earliest at the shotMinFrames-th frame, at the latest when shotCap
// has passed (then the error of the last try is returned, and shoot's retry
// loop, the counted safety net, takes over). It returns how many frames it
// took.
//
// Why (#308): a widget paintable draws nothing for a widget that is mid-
// update, and so nothing for any of its ancestors, up to the window: the
// window's render node comes out NULL ("step 3 failed") although it is mapped,
// realized and sized. Seen at the end shot of caches-no: the Overview nav row
// of the sidebar (the run ends, the page and the row selection switch back)
// was the deepest empty widget, its own children drawing fine. It lasts until
// the frames after the change have run, and how many that is depends on the
// machine: 1 or 2 on the host, 2 or more under load (frames ~45 ms apart,
// paced by the Broadway server's roundtrip), and more in the VM CI (llvmpipe).
// A frame count tuned on one machine fails on another (bb178e4 failed in the
// VM with 4), so the wait is for the real condition, a snapshot that is
// drawn, within a time cap. The frames each shot took are logged ("SHOT
// FRAMES") and the largest is kept in shotFramesMax.
func saveAfterPaint(w *Window, path string) (frames int, err error) {
	fc := gdk.BaseFrameClock(w.win.FrameClock())
	if fc == nil {
		return 0, fmt.Errorf("the window has no frame clock yet")
	}
	var done bool
	start := time.Now()
	id := fc.ConnectAfterPaint(func() {
		if done {
			return
		}
		if frames++; frames < shotMinFrames {
			fc.RequestPhase(gdk.FrameClockPhaseUpdate)
			return
		}
		if w.win.Width() <= 0 || w.win.Height() <= 0 {
			err = fmt.Errorf("the window has no size yet (%dx%d)", w.win.Width(), w.win.Height())
		} else {
			err = savePNG(w.win, path)
		}
		if err != nil && time.Since(start) < shotCap {
			fc.RequestPhase(gdk.FrameClockPhaseUpdate)
			return
		}
		done = true
	})
	defer fc.HandlerDisconnect(id)
	fc.RequestPhase(gdk.FrameClockPhaseUpdate)
	for end := start.Add(shotCap + time.Second); !done && time.Now().Before(end); {
		pumpMain(time.Millisecond)
	}
	if !done {
		return frames, fmt.Errorf("no frame was painted within %v", shotCap+time.Second)
	}
	return frames, err
}

// shotFramesMax is the most frames any shot of this process took.
var shotFramesMax atomic.Int32

func noteShotFrames(scenario, what string, frames int) {
	for {
		m := shotFramesMax.Load()
		if int32(frames) <= m || shotFramesMax.CompareAndSwap(m, int32(frames)) {
			break
		}
	}
	fmt.Fprintf(os.Stderr, "SHOT FRAMES %s-%s: %d (max so far %d)\n", scenario, what, frames, shotFramesMax.Load())
}

// shotTries and shotWait: how often and for how long shoot lets the main
// loop run between attempts when the window can't be drawn yet.
const (
	shotMinFrames = 2
	shotCap       = time.Second
	shotTries     = 20
	shotWait      = 50 * time.Millisecond
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
