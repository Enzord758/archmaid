package ui

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/Enzord758/archmaid/internal/engine"
)

// TestMeasureUserCacheAsksAgain: a measurement requested while one goes is
// coalesced into one more, run when the first ends, and no third.
func TestMeasureUserCacheAsksAgain(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// No ~/.cache: the walk fails, the line stays hidden ("") and nothing
	// on the (widgetless) window needs redrawing.
	t.Setenv("HOME", t.TempDir())
	w := &Window{}
	before := cacheMeasures.Load()
	w.measureUserCache()
	w.measureUserCache() // while the first goes
	if !w.cache.busy || !w.cache.again {
		t.Fatalf("busy %v again %v after two requests, want both", w.cache.busy, w.cache.again)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (cacheMeasures.Load() < before+2 || w.cache.busy) {
		runMainLoop(10 * time.Millisecond)
	}
	runMainLoop(100 * time.Millisecond) // a third one would show here
	if got := cacheMeasures.Load() - before; got != 2 {
		t.Errorf("%d measurements, want the first and the one asked for meanwhile", got)
	}
	if w.cache.busy || w.cache.again || w.cache.line != "" {
		t.Errorf("meter after: busy %v again %v line %q", w.cache.busy, w.cache.again, w.cache.line)
	}
}

// TestSignalBeforeTheWindowExists: a quit signal that comes before the
// application was activated has no window and no run to stop: the signal
// loop tells the closing channel "no Driver", quits the application, and
// the exit status is 128 plus the signal.
func TestSignalBeforeTheWindowExists(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := adw.NewApplication("org.archmaid.test.signals", gio.ApplicationNonUnique)
	var win *Window
	s := &signals{ch: make(chan os.Signal, 2), closing: make(chan engine.Driver, 1), done: make(chan struct{})}
	go s.run(app, &win)
	s.ch <- syscall.SIGTERM
	var d engine.Driver = abortedStub{}
	got := false
	deadline := time.Now().Add(3 * time.Second)
	for !got && time.Now().Before(deadline) {
		runMainLoop(10 * time.Millisecond)
		select {
		case d = <-s.closing:
			got = true
		default:
		}
	}
	if !got {
		t.Fatal("the signal was never handled")
	}
	if d != nil {
		t.Errorf("closing carried %v, want no Driver", d)
	}
	if code := s.stop(); code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit status %d, want %d", code, 128+int(syscall.SIGTERM))
	}
}

// abortedStub is a non-nil Driver value to prove the closing channel's nil.
type abortedStub struct{ engine.Driver }
