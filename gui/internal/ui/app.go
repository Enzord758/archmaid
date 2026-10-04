// Package ui is the GTK layer: the only package in the GUI that imports
// gotk4. It stays thin — the state lives in internal/runview, which is plain
// Go — and every widget call happens on the GTK main thread: the Driver's
// events reach it through runview.Pump, which posts with glib's idle-add.
//
// Theming is libadwaita's own: DMS writes ~/.config/gtk-4.0/gtk.css
// (matugen colours), which GTK loads for every app, so there is no colour
// CSS here.
package ui

import (
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
)

// AppID is the application id (D-Bus name, .desktop file, icon).
const AppID = "io.github.enzord758.archmaid"

// Options configure a run of the application.
type Options struct {
	Flags gio.ApplicationFlags
	// NewDriver makes the Driver the window talks to; it is closed when the
	// window closes. Action is what the Run button starts.
	NewDriver func() engine.Driver
	Action    string
	Pump      runview.PumpOptions
	// hook, when set, sees the window once it is built (the headless
	// harness drives it from there).
	hook func(*Window)
}

// Main runs archmaid-gui with the stub Driver and returns its exit status.
// ARCHMAID_GUI_SCRIPT picks the stub's script: "fullcleanup" (default) or
// "flood" (50k lines, for eyeballing the output view).
//
// TODO(E6): the real Driver replaces the stub here.
func Main(args []string) int {
	opts := Options{Action: "fullcleanup"}
	switch os.Getenv("ARCHMAID_GUI_SCRIPT") {
	case "flood":
		opts.Action = "flood"
		opts.NewDriver = func() engine.Driver { return stub.New(runview.Flood(time.Now(), 50000, 1, 0), stub.Options{}) }
	default:
		opts.NewDriver = func() engine.Driver { return stub.New(stub.FullCleanup(time.Now()), stub.Options{}) }
	}
	return Run(args, opts)
}

// Run runs the application until its window is closed. The Driver is
// closed, and the pump's goroutine gone, by the time it returns.
func Run(args []string, opts Options) int {
	app := adw.NewApplication(AppID, opts.Flags)
	var w *Window
	app.ConnectActivate(func() {
		if w != nil {
			w.win.Present()
			return
		}
		w = newWindow(app, opts)
		if opts.hook != nil {
			opts.hook(w)
		}
		w.win.Present()
	})
	code := app.Run(args)
	if w != nil {
		// app.Quit (or a signal) can end the loop with the window still up.
		w.closeDriver()
	}
	return code
}

// DefaultRenderer makes GTK render with cairo unless GSK_RENDERER is
// already set, so the owner can still pick one (GSK_RENDERER=vulkan, ngl,
// gl). Call it before GTK initializes: the renderer is read once.
//
// Why cairo: with the GPU renderers NVIDIA's userspace driver is mapped
// into the process, which puts the idle RSS at ~280–390 MB against a
// 120 MB budget and the first frame at ~375–450 ms; with cairo it is
// ~110–120 MB and ~85 ms, and the 50k-line flood stays within its budgets
// (docs/TESTING.md → GUI measurements). The window is lists and text,
// which cairo draws as fast.
func DefaultRenderer() {
	if _, set := os.LookupEnv("GSK_RENDERER"); !set {
		os.Setenv("GSK_RENDERER", "cairo")
	}
}

// post runs f on the GTK main thread; it never blocks.
func post(f func()) { glib.IdleAdd(f) }
