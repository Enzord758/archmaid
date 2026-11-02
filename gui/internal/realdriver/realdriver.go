// Package realdriver wires the GUI onto the real engine Driver
// (internal/driver) on the polkit path, in plain Go (no gotk4), so the
// wiring is tested with plain `go test`.
//
// What it builds, per Driver (Wiring.New; the window makes a fresh one for
// every Start, so this is per run):
//
//   - the Session with engine.AuthPolkit: a root step with a Verb runs
//     the root helper through pkexec (helperclient.Run, the Driver's
//     default); Full Cleanup's orphan probe plans with
//     helperclient.PlanOrphans (as the user, never through pkexec);
//   - every user step's child (topgrade, yay -Sc, hyprpm …) writes to a
//     pty that is never its controlling terminal (Deps.Pty), as wide as
//     the Run view (Config.TermCols): colour and progress bars as in a
//     terminal, while sudo still finds no terminal;
//   - topgrade (NeedsPriv, no Verb) starts as the user, its inner sudo
//     pointed at archmaid-gui's SUDO_ASKPASS wrapper (Deps.Priv is
//     privilege.Sudo{Wrapper: srv.Wrapper()}): sudo's prompt reaches the
//     Driver from the askpass Server, which verifies it against the running
//     step (Request.Verify) and asks the GUI with NeedsPassword{Kind:
//     Askpass};
//   - the actions' front-end glue, shared with the TUI (internal/frontend:
//     the AUR audit step and menu report, drawn as plain text; the
//     downloads delete step with its abort record);
//   - the AUR audit's prefetch (Deps.Prefetch = frontend.Prefetcher): the
//     lookup starts when the queue is planned, so it runs while the gate
//     is up; the audit step awaits it instead of looking up again, and a
//     declined gate, an abort or Close cancels and reaps it (the engine's
//     CancelPrefetch, the Driver's Close);
//   - the log store: a fresh one per Driver, so every run gets its own
//     <stamp> directory, stamped when the run starts (Config.Now); a dry
//     run writes to the reused dryrun slot. A real run's first write
//     rotates the history to logstore.KeepRuns; last-run.json and the
//     audit's files stay in the state dir's root.
//
// The askpass relay (Relay) mirrors the TUI's: sudo runs the wrapper, which
// execs archmaid-gui --askpass <sock> [prompt]; main hands that to
// privilege.RunClient before anything else, GTK included.
package realdriver

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/frontend"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Relay is the SUDO_ASKPASS helper mode, the same dispatch as the TUI's
// startup: args (os.Args[1:]) of --askpass <sock> [prompt] relay sudo's
// prompt to the running GUI's Server and return the exit code with true;
// anything else returns false and the GUI starts.
func Relay(args []string) (code int, relayed bool) {
	if len(args) >= 2 && args[0] == "--askpass" {
		prompt := ""
		if len(args) >= 3 {
			prompt = args[2]
		}
		return privilege.RunClient(args[1], prompt), true
	}
	return 0, false
}

// Config is what a Wiring takes from outside. The zero value is the real
// thing; tests swap in fakes for everything that reads the system.
type Config struct {
	// StateDir is the log store's state directory; "" is logstore's
	// default, ~/.local/state/archmaid.
	StateDir string
	// Exe is the binary the askpass wrapper execs in --askpass mode; ""
	// is os.Executable. RuntimeDir holds the askpass socket; "" is
	// $XDG_RUNTIME_DIR. NoAskpass runs without the Server (sudo -n).
	Exe, RuntimeDir string
	NoAskpass       bool

	// Faillock answers ReadFaillock; nil is the Driver's default (the
	// current user's pam_faillock count).
	Faillock func() engine.FaillockRead
	// PlanOrphans is Env.PlanOrphans; nil is helperclient.PlanOrphans.
	PlanOrphans func(ctx context.Context) (steps.OrphanPlan, error)
	// PlanPackages is Env.PlanPackages (the Packages page's plan); nil is
	// helperclient.PlanPackages.
	PlanPackages func(ctx context.Context, req steps.PkgRequest) (steps.PkgPlan, error)
	// PlanAUR is Env.PlanAUR (the Packages page's AUR install, see
	// frontend.AURPlanner). Nil leaves it unset: an AUR run then fails
	// closed with a notice, until the page's wiring (it needs the
	// pkgdb.DB) provides it.
	PlanAUR func(ctx context.Context, names []string) (actions.AURPrepared, error)
	// Foreign lists the foreign packages the audit checks; nil runs
	// `pacman -Qqm`. RPCURL is the AUR endpoint; "" is audit's default.
	Foreign func(ctx context.Context) ([]string, error)
	RPCURL  string
	// LookPath is Env.LookPath (Full Cleanup asks it for hyprpm); nil is
	// exec.LookPath.
	LookPath func(string) (string, error)

	// KillGrace and WaitDelay are the Driver's (zero: its defaults, the
	// TUI's 5 s and 2 s).
	KillGrace, WaitDelay time.Duration
	// Report is told what no Event carries (driver.Deps.Report).
	Report func(error)
	// RefreshUpdates is driver.Deps.RefreshUpdates: the widget's update
	// check after a topgrade run. Nil is driver.RefreshUpdates (systemctl
	// --user, else the packaged script); tests must set a fake, because
	// that reaches systemctl and a script outside the test.
	RefreshUpdates func(context.Context) error

	// RunReport is driver.Deps.RunReport: the run report (package changes,
	// space freed, what needs the user; internal/report) the Run page's
	// finished state draws. Nil, the default, means none, and a nil one is
	// what every test gets: the real config (report.DefaultConfig, which
	// archmaid-gui's main sets, ui.realConfig) reads the machine's
	// pacman.log and runs paccache and pacman, which testenv traps.
	RunReport *report.Config

	// Now is read once per Driver (New) to stamp its run directory; nil
	// is time.Now. Log entries are stamped with the real time.
	Now func() time.Time

	// TTYAgent is pkttyagent's path for the helper steps' polkit prompts
	// (agent.go); "" is /usr/bin/pkttyagent (sysbin.Pkttyagent, never
	// looked up on PATH), "-" runs without one (polkit
	// asks through the desktop's agent).
	TTYAgent string

	// TermCols is the Run view's width in monospace columns, read as each
	// step's child starts: its pty's width (driver.Deps.TermCols). Nil or
	// < 1 is 80.
	TermCols func() int
}

// Wiring makes the GUI's real Drivers. It owns the askpass Server; Close
// it after the last Driver is closed.
type Wiring struct {
	cfg Config
	srv *privilege.Server

	// deleting counts downloads deletions (and their pending abort
	// records) still in flight; Close waits for them.
	deleting frontend.Deletions

	mu       sync.Mutex
	onNotice func(string) // SetNotice
}

// Start makes a Wiring and starts its askpass Server. Best effort, as the
// TUI's: without one (no runtime dir, say) topgrade's sudo runs `sudo -n`
// and fails cleanly instead of prompting on a terminal there isn't.
func Start(cfg Config) *Wiring {
	w := &Wiring{cfg: cfg}
	if cfg.NoAskpass {
		return w
	}
	exe := cfg.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return w
		}
	}
	dir := cfg.RuntimeDir
	if dir == "" {
		dir = os.Getenv("XDG_RUNTIME_DIR")
	}
	if srv, err := privilege.Listen(dir, exe, privilege.ServerOptions{}); err == nil {
		w.srv = srv
	}
	return w
}

// Askpass reports whether the askpass Server runs.
func (w *Wiring) Askpass() bool { return w.srv != nil }

// Close waits for downloads deletions still finishing (an aborted one
// stops at the end of its current entry and logs what it deleted), then
// closes the askpass Server. Call it after the last Driver's Close.
func (w *Wiring) Close() {
	w.deleting.Wait()
	w.srv.Close()
}

// CloseAskpass closes the askpass Server at once (its socket and wrapper
// go with its private directory), without waiting for anything: for a
// forced exit, which skips Close. Close may still follow; it is a no-op
// for the Server then.
func (w *Wiring) CloseAskpass() { w.srv.Close() }

// Options is the base of every run's engine.Options: the polkit flow and a
// complete actions.Env (Validate passes). The Settings page applies dry
// run and the downloads folder on top (settings.Apply); New fills in what
// depends on the dry-run flag.
func (w *Wiring) Options() engine.Options {
	o := engine.Options{Auth: engine.AuthPolkit}
	o.Env = w.env(o.Env, false, w.store(false))
	return o
}

// New is the window's NewDriver: a Session for o, always on the polkit
// path, inside a real Driver, with its own log store (one run directory,
// stamped now) and the audit's prefetch. o.Env's Downloads (the Settings
// page's) is kept; everything else in it is the Wiring's.
func (w *Wiring) New(o engine.Options) engine.Driver {
	o.Auth = engine.AuthPolkit
	store := w.store(o.DryRun)
	o.Env = w.env(o.Env, o.DryRun, store)
	deps := driver.Deps{
		Priv: privilege.Sudo{},
		Log:  store,
		// The run's own store: the prefetched audit logs where the rest
		// of the run does.
		Prefetch:  frontend.Prefetcher(func() audit.Config { return w.auditConfig(store, o.DryRun) }),
		Polkit:    true,
		Faillock:  w.cfg.Faillock,
		KillGrace: w.cfg.KillGrace,
		WaitDelay: w.cfg.WaitDelay,
		// No child of the GUI may reach a terminal: one started from a
		// shell still has the shell's, and sudo would prompt there.
		Detach: true,
		// ... but its output goes to a pty of its own, never its
		// controlling terminal (internal/proc/pty.go), so tools colour
		// and draw progress as in a terminal, as wide as the Run view.
		Pty:      true,
		TermCols: w.cfg.TermCols,
		Report:   w.cfg.Report,
		// The run report, for the Run page's summary and last-run.json.
		RunReport: w.cfg.RunReport,
		// The widget's update count, refreshed after a topgrade run.
		RefreshUpdates: w.cfg.RefreshUpdates,
		// One password per run: the first the user gives answers the
		// run's later sudo and polkit prompts (driver/keep.go).
		KeepPassword: true,
	}
	if deps.RefreshUpdates == nil {
		deps.RefreshUpdates = driver.RefreshUpdates
	}
	agents := make(chan driver.AgentPrompt)
	deps.Agent, deps.Helper = agents, w.helper(agents)
	if w.srv != nil {
		deps.Priv = privilege.Sudo{Wrapper: w.srv.Wrapper()}
		deps.Askpass = w.srv.Requests()
	}
	return driver.New(engine.New(o), deps)
}

// env completes base for a run that logs to store: the Session's own
// fields stay as given (New sets DryRun and Polkit from Options),
// Downloads too when set.
func (w *Wiring) env(base actions.Env, dry bool, store *logstore.Store) actions.Env {
	e := base
	e.DryRun, e.Polkit = dry, true
	e.PlanOrphans = w.cfg.PlanOrphans
	if e.PlanOrphans == nil {
		e.PlanOrphans = helperclient.PlanOrphans
	}
	e.PlanPackages = w.cfg.PlanPackages
	if e.PlanPackages == nil {
		e.PlanPackages = helperclient.PlanPackages
	}
	e.PlanAUR = w.cfg.PlanAUR
	e.LookPath = w.cfg.LookPath
	return frontend.Glue{
		AuditConfig: func() audit.Config { return w.auditConfig(store, dry) },
		Render:      nil, // plain text: the GUI draws the findings from Report.Audit
		Downloads:   func() downloads.Options { return frontend.DownloadsOptions("", dry) },
		Store:       store,
		Deletions:   &w.deleting,
	}.Env(e)
}

// auditConfig is the audit's config for a run logging to store: its
// snapshot and state files in the state dir's root, its entries in the
// run's audit log.
func (w *Wiring) auditConfig(store *logstore.Store, dry bool) audit.Config {
	return frontend.AuditConfig(store, frontend.AuditOptions{RPCURL: w.cfg.RPCURL, DryRun: dry, Foreign: w.cfg.Foreign})
}

// store is a run's log store, a fresh one: its run directory is stamped
// now (Config.Now) and created by the run's first write, which rotates
// the history to logstore.KeepRuns. A dry run's writes to the dryrun slot
// and never rotates. It creates nothing on disk itself.
func (w *Wiring) store(dry bool) *logstore.Store {
	now := w.cfg.Now
	if now == nil {
		now = time.Now
	}
	return logstore.New(logstore.Options{Dir: w.cfg.StateDir, DryRun: dry, Fresh: true, Now: runClock(now()), KeepRuns: logstore.KeepRuns})
}

// runClock is time.Now, except for its first call, which names the run
// directory (logstore.Options.Now): that one is start.
func runClock(start time.Time) func() time.Time {
	var named atomic.Bool
	return func() time.Time {
		if named.CompareAndSwap(false, true) {
			return start
		}
		return time.Now()
	}
}

// KeepPreview is the gate sheet's side-effect-free preview of the kept
// password's PATH guard: "" when the password would be kept, else why not
// (driver.PreviewKeepOff). The step-start check stays authoritative.
func KeepPreview() string { return driver.PreviewKeepOff() }
