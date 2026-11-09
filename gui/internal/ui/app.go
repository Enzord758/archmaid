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
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/gui/internal/realdriver"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
	runreport "github.com/Enzord758/archmaid/internal/report"
)

// AppID is the application id (D-Bus name, .desktop file, icon).
const AppID = "io.github.enzord758.archmaid"

// Options configure a run of the application.
type Options struct {
	Flags gio.ApplicationFlags
	// Stub: NewDriver is the stub's. The application then runs as
	// StubAppID, and the window always shows StubBanner, "(stub)" in its
	// title and STUB in the status line.
	Stub bool
	// NewDriver makes the Driver the window talks to from the Options for
	// its runs: Engine with the Settings page's settings applied. The
	// window makes one at start and a fresh one for every Start (a Session
	// is cheap: one Session and one log store per run); each is closed
	// when it is replaced or the window closes. The stub ignores the
	// Options.
	NewDriver func(engine.Options) engine.Driver
	// Engine is the base of every run's engine.Options.
	Engine engine.Options
	// SettingsPath is the settings file; "" is settings.DefaultPath(),
	// $XDG_CONFIG_HOME/archmaid/gui.json.
	SettingsPath string
	Pump         runview.PumpOptions
	// StateDir is archmaid's state directory, read only (last-run.json,
	// the run logs); "" is logstore's default, ~/.local/state/archmaid.
	StateDir string
	// KeepsPassword: the Driver keeps the run's first password (the real
	// one does); its password dialogs say so (KeptNote).
	KeepsPassword bool
	// KeepPreview, when set with KeepsPassword, is asked when the gate
	// sheet is built: "" when the PATH guard would leave the password
	// kept, else why it would turn it off. The sheet words itself from
	// that; the guard at step start stays the authority (PATH or files can
	// change between the sheet and the step).
	KeepPreview func() string
	// Notices, when set, is handed the window's notice setter: what to
	// show when polkit's own dialog has to ask (realdriver's SetNotice).
	Notices func(set func(string))
	// Confirm, when set, is an action the window starts as soon as it is
	// up (--confirm fullcleanup), which opens straight into its confirm:
	// nothing runs before Proceed (the gate is its first question).
	Confirm string
	// Launcher opens files and folders and asks for a folder; nil is the
	// desktop's (gtk.FileLauncher, gtk.FileDialog). The harness records.
	Launcher Launcher
	// AtForcedExit, when set, runs on a second quit signal's forced exit
	// (signals.go), where no deferred cleanup runs: it must undo what
	// would otherwise outlive the process (realdriver's CloseAskpass: the
	// askpass Server's socket and wrapper). It gets at most a second.
	AtForcedExit func()
	// Packages wires the Packages page (packages.go); nil leaves it saying
	// it is not available in this build.
	Packages *PackagesWiring
	// RefreshUpdates starts the updates check the timer runs
	// (driver.RefreshUpdates: a user unit, no root); it returns once the
	// check is queued, not when it is done. The Overview's Updates card
	// reads updates-cache.json for the result. nil leaves its button off.
	RefreshUpdates func(context.Context) error
	// Health runs the System health checks and fetches the unread Arch
	// news (internal/health through cmd/archmaid-gui's adapter); nil leaves
	// the cell calm and the confirms without news. The stub has a demo one.
	Health healthview.Source
	// hook, when set, sees the window once it is built (the headless
	// harness drives it from there).
	hook func(*Window)
	// appID, when set, stands in for AppID (the instance test's own name,
	// so it never meets a real archmaid-gui on the bus).
	appID string
}

// ScriptEnv picks the stub's script; it is read only with --stub.
const ScriptEnv = "ARCHMAID_GUI_SCRIPT"

// StubAppID is the stub's application id: never AppID, so a demo window
// and a real one are separate instances on the session bus, and a --stub
// launch can't hand its activation to an open real window (or the other
// way round).
const StubAppID = AppID + stubSuffix

const stubSuffix = ".Stub"

// StubBanner is the banner a stub window always shows.
const StubBanner = "Demo — the stub, changes nothing"

// stubScripts are the ARCHMAID_GUI_SCRIPT values the stub takes; "" is
// its Catalog.
var stubScripts = []string{"", "flood", "flood-colour", "uncached", "askpass"}

// Main runs archmaid-gui and returns its exit status. By default it drives
// the real Driver on the polkit path (internal/realdriver), with the
// askpass Server for topgrade's sudo; main has already handled --askpass.
//
// Only --stub runs it on the stub Driver instead, which touches nothing:
// for demos. No environment variable does: a stray one must never put
// the real GUI on the stub. A stub window says so (StubBanner, the title,
// STUB in the status line) and has its own app id (StubAppID). The stub
// plays its Catalog: a script for every menu action that runs, with the
// declined paths. With --stub, ARCHMAID_GUI_SCRIPT changes that: "flood"
// or "flood-colour" play 50k lines whatever action is started (for
// eyeballing the output view); "uncached" makes Full Cleanup ask for the
// password up front (the stub's "wrong" is wrong), "askpass" makes its
// topgrade ask mid-step. Any other value is refused.
func Main(args []string) int {
	l, err := parseLaunch(args, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "archmaid-gui:", err)
		return 2
	}
	if !l.stub {
		return mainReal(l.args, l.confirm)
	}
	return Run(l.args, Options{Stub: true, NewDriver: stubDriver(l.script), Confirm: l.confirm, Packages: demoWiring(), Health: healthview.Demo()})
}

// launch is what the command line asks for.
type launch struct {
	args   []string // for GApplication: without --stub and --confirm
	stub   bool
	script string // ARCHMAID_GUI_SCRIPT, with stub only
	// confirm is --confirm's action: the window opens into its confirm.
	confirm string
}

// confirmable are the actions --confirm takes: only Full Cleanup for now
// (the DMS widget's Run Full Cleanup).
var confirmable = []string{actions.IDFullCleanup}

// parseLaunch takes --stub out of args (GApplication would refuse it).
// The stub runs only for --stub; ARCHMAID_GUI_SCRIPT is read only then,
// and an unknown value is an error rather than the Catalog.
func parseLaunch(args []string, getenv func(string) string) (launch, error) {
	var l launch
	l.args = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case i > 0 && a == "--stub":
			l.stub = true
			continue
		case i > 0 && (a == "--confirm" || strings.HasPrefix(a, "--confirm=")):
			v, given := strings.CutPrefix(a, "--confirm=")
			if !given {
				if i+1 >= len(args) {
					return launch{}, fmt.Errorf("--confirm needs an action (%s)", strings.Join(confirmable, ", "))
				}
				i++
				v = args[i]
			}
			if !slices.Contains(confirmable, v) || l.confirm != "" {
				return launch{}, fmt.Errorf("--confirm %q: want one of %s, once", v, strings.Join(confirmable, ", "))
			}
			l.confirm = v
			continue
		}
		l.args = append(l.args, a)
	}
	if !l.stub {
		return l, nil
	}
	l.script = getenv(ScriptEnv)
	if !slices.Contains(stubScripts, l.script) {
		return launch{}, fmt.Errorf("unknown %s %q (want one of flood, flood-colour, uncached, askpass, or unset)", ScriptEnv, l.script)
	}
	return l, nil
}

// mainReal runs the window on the real Driver. The Wiring outlives every
// Driver: Run closes the last one before it returns.
func mainReal(args []string, confirm string) int {
	// The Packages page's data: pacman's databases and the AUR planner
	// over them, which the Driver needs for an AUR install.
	pk := openRealPackages(pkgSource{})
	cfg := realConfig()
	cfg.PlanAUR = pk.PlanAUR
	wr := realdriver.Start(cfg)
	defer wr.Close()
	base := wr.Options()
	// A missing piece of the actions' wiring would otherwise fail
	// mid-action (the TUI's main checks the same).
	if err := base.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid-gui:", err)
		return 1
	}
	return Run(args, realOptions(base, wr.New, wr.SetNotice, confirm, wr.CloseAskpass, pk))
}

// realConfig is the binary's realdriver.Config: errors to stderr, each
// step's pty as wide as the Run view (termcols.go), and the real run report
// (report.DefaultConfig: pacman's log, paccache, pacman), which no test
// config sets.
func realConfig() realdriver.Config {
	return realdriver.Config{
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "archmaid-gui:", err) },
		TermCols:  TermCols,
		RunReport: runreport.DefaultConfig(),
	}
}

// realOptions are the window's Options on the real Driver: it keeps the
// run's first password (KeepsPassword), whatever base's auth mode. pk is the
// Packages page's data (openRealPackages); nil leaves the page saying it is
// not available.
func realOptions(base engine.Options, newDriver func(engine.Options) engine.Driver, notices func(func(string)), confirm string, atForcedExit func(), pk *realPackages) Options {
	return Options{NewDriver: newDriver, Engine: base, KeepsPassword: true, KeepPreview: realdriver.KeepPreview, Notices: notices, Confirm: confirm,
		AtForcedExit: atForcedExit, Packages: pk.wiring(), RefreshUpdates: driver.RefreshUpdates, Health: realHealth()}
}

// stubDriver is the stub's NewDriver for an ARCHMAID_GUI_SCRIPT value
// (one of stubScripts).
func stubDriver(script string) func(engine.Options) engine.Driver {
	single := func(s stub.Script) func(engine.Options) engine.Driver {
		return func(engine.Options) engine.Driver { return stub.New(s, stub.Options{}) }
	}
	catalog := func(f func(time.Time) map[string]stub.Script) func(engine.Options) engine.Driver {
		return func(engine.Options) engine.Driver {
			return stub.New(nil, stub.Options{Scripts: withPackages(f, time.Now())})
		}
	}
	switch script {
	case "flood":
		return single(runview.Flood(time.Now(), 50000, 1, 0))
	case "flood-colour":
		return single(runview.FloodColour(time.Now(), 50000, 1, 0))
	case "uncached":
		return catalog(stub.CatalogUncached)
	case "askpass":
		return catalog(func(start time.Time) map[string]stub.Script {
			c := stub.Catalog(start)
			c[actions.IDFullCleanup] = c[stub.IDFullCleanupAskpass]
			return c
		})
	}
	return catalog(stub.Catalog)
}

// withPackages is f's scripts with the Packages page's (stub.PackagesCatalog)
// next to them: the stub plays a Packages run as it plays a menu action.
func withPackages(f func(time.Time) map[string]stub.Script, start time.Time) map[string]stub.Script {
	m := f(start)
	maps.Copy(m, stub.PackagesCatalog(start))
	return m
}

// Run runs the application until its window is closed. The Driver is
// closed, and the pump's goroutine gone, by the time it returns.
func Run(args []string, opts Options) int {
	id := AppID
	if opts.appID != "" {
		id = opts.appID
	}
	if opts.Stub {
		id += stubSuffix
	}
	app := adw.NewApplication(id, opts.Flags)
	var w *Window
	app.ConnectActivate(func() {
		if w != nil {
			w.win.Present()
			return
		}
		loadStyle()
		w = newWindow(app, opts)
		if opts.hook != nil {
			opts.hook(w)
		}
		w.win.Present()
		if id := opts.Confirm; id != "" {
			post(func() { w.startAction(id) })
		}
	})
	// SIGINT, SIGTERM and SIGHUP are Stop and Quit (signals.go): the run is
	// aborted and recorded, its children stopped, then Run returns 128 +
	// the signal's number.
	sigs := catchSignals(app, &w, opts.AtForcedExit)
	code := app.Run(args)
	if w != nil {
		// app.Quit can end the loop with the window still up.
		w.closeDriver()
	}
	if sig := sigs.stop(); sig != 0 {
		return sig
	}
	return code
}

// DefaultRenderer makes GTK render with cairo unless GSK_RENDERER is
// already set, so the owner can still pick one (GSK_RENDERER=vulkan, ngl,
// gl). Call it before GTK initializes: the renderer is read once.
//
// Why cairo: with the GPU renderers NVIDIA's userspace driver is mapped
// into the process, which puts the idle RSS at ~280–390 MB against a
// 125 MB budget and the first frame at ~375–450 ms; with cairo it is
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
