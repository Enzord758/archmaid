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
//   - topgrade (NeedsPriv, no Verb) starts as the user, its inner sudo
//     pointed at archmaid-gui's SUDO_ASKPASS wrapper (Deps.Priv is
//     privilege.Sudo{Wrapper: srv.Wrapper()}): sudo's prompt reaches the
//     Driver from the askpass Server, which verifies it against the running
//     step (Request.Verify) and asks the GUI with NeedsPassword{Kind:
//     Askpass};
//   - the actions' front-end glue the TUI keeps in cmd/archmaid (the AUR
//     audit step and menu report, the downloads delete step with its abort
//     record), mirrored here for a front end without lipgloss;
//   - the AUR audit's prefetch (Deps.Prefetch, the TUI's startPrefetch):
//     the lookup starts when the queue is planned, so it runs while the
//     gate is up; the audit step awaits it instead of looking up again,
//     and a declined gate, an abort or Close cancels and reaps it (the
//     engine's CancelPrefetch, the Driver's Close);
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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
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

	// Now is read once per Driver (New) to stamp its run directory; nil
	// is time.Now. Log entries are stamped with the real time.
	Now func() time.Time
}

// Wiring makes the GUI's real Drivers. It owns the askpass Server; Close
// it after the last Driver is closed.
type Wiring struct {
	cfg Config
	srv *privilege.Server

	// deleting counts downloads deletions (and their pending abort
	// records) still in flight; Close waits for them.
	deleting sync.WaitGroup
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
		Priv:      privilege.Sudo{},
		Log:       store,
		Prefetch:  w.prefetch(store, o.DryRun),
		Polkit:    true,
		Faillock:  w.cfg.Faillock,
		KillGrace: w.cfg.KillGrace,
		WaitDelay: w.cfg.WaitDelay,
		Report:    w.cfg.Report,
	}
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
	e.LookPath = w.cfg.LookPath
	e.AuditStep = func() steps.Step { return auditStep(w.auditConfig(store, dry)) }
	e.AuditReport = func(ctx context.Context) actions.Discovery {
		return menuAudit(ctx, w.auditConfig(store, dry))
	}
	if e.Downloads == nil {
		e.Downloads = func() downloads.Options { return resolverOptions(dry) }
	}
	e.DeleteDownloads = func(plan downloads.Plan) func(context.Context, steps.Env) steps.Result {
		return w.deleteOldDownloads(plan, store, e.Downloads)
	}
	return e
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
	return logstore.New(logstore.Options{Dir: w.cfg.StateDir, DryRun: dry, Now: runClock(now()), KeepRuns: logstore.KeepRuns})
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

// resolverOptions is the downloads resolver's input from the environment,
// as the TUI gathers it, without a folder (the Settings page sets one).
func resolverOptions(dry bool) downloads.Options {
	return downloads.Options{
		Env:           os.Getenv(downloads.EnvVar),
		Home:          os.Getenv("HOME"),
		PasswdHome:    downloads.PasswdHome(),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		DryRun:        dry,
	}
}

// ── the AUR audit (cmd/archmaid/audit.go's glue, without the styling) ──

// auditConfig is every setting an audit reads, captured when the queue is
// built or the menu audit starts.
func (w *Wiring) auditConfig(store *logstore.Store, dry bool) audit.Config {
	rpc := w.cfg.RPCURL
	if rpc == "" {
		rpc = audit.DefaultRPCURL
	}
	foreign := w.cfg.Foreign
	if foreign == nil {
		foreign = foreignPackages
	}
	return audit.Config{
		RPCURL:       rpc,
		SnapshotPath: filepath.Join(store.Root(), audit.SnapshotFile),
		StatePath:    filepath.Join(store.Root(), audit.StateFile),
		Deadline:     audit.DefaultDeadline,
		DryRun:       dry,
		Foreign:      foreign,
		Log:          func(kind, label, body string) { _ = store.Append(logstore.Audit, kind, label, body) },
	}
}

// foreignPackages is `pacman -Qqm`: the installed packages in no sync
// repo. A failure reads as none (audit.Run then reports ErrNoForeign).
func foreignPackages(ctx context.Context) ([]string, error) {
	out, _ := proc.Query(ctx, "pacman", "-Qqm")
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

// menuAudit is the menu's audit (Env.AuditReport): a notice, or the report
// with its findings (the GUI draws them from Audit) and Accept for the
// high ones.
func menuAudit(ctx context.Context, cfg audit.Config) actions.Discovery {
	res, err := audit.Run(ctx, cfg, time.Now())
	switch {
	case errors.Is(err, audit.ErrNoForeign):
		return actions.Notice{Text: "No foreign (AUR or locally built) packages installed."}
	case err != nil:
		return actions.Notice{Text: "Couldn't reach the AUR: " + audit.ShortErr(err, cfg.Deadline)}
	}
	res.LogSummary()
	r := actions.Report{Text: res.Text(), Audit: &res}
	// The snapshot advances for everything but the high findings; those
	// stay flagged until the report's Accept.
	if save := res.Commit(true, false); save != nil {
		save()
	}
	if res.High() > 0 {
		r.Accept = res.Commit(false, true)
	}
	return r
}

// auditStep is the audit in front of topgrade (Full Cleanup, Run
// topgrade): an in-process step whose high finding gates the update. Its
// Prefetch has the Driver start the lookup (prefetch) when the queue is
// planned, so it runs while the gate is up; the step then awaits that
// instead of calling Run. Without one, Run audits cfg, captured here when
// the queue is built (the moment a prefetch would capture it).
func auditStep(cfg audit.Config) steps.Step {
	return steps.Step{
		ID:    steps.IDAURAudit,
		Title: "Auditing AUR packages…", LogLabel: "AUR audit",
		Category: logstore.Audit, AlwaysLog: true,
		Run: func(ctx context.Context, _ steps.Env) steps.Result {
			res, err := audit.Run(ctx, cfg, time.Now())
			return auditStepResult(cfg, res, err)
		},
		Prefetch: steps.PrefetchAURAudit,
	}
}

// prefetch is Deps.Prefetch for a run logging to store (the TUI's
// startPrefetch): the AUR audit's lookup, started on the Driver's loop
// with the config read there; nil for a name it doesn't know. The Driver
// hands the audit step's ctx to its Run, and cancels and reaps it when the
// engine drops it (a declined gate, an abort, the run's end) or on Close.
func (w *Wiring) prefetch(store *logstore.Store, dry bool) func(string) driver.Prefetch {
	return func(name string) driver.Prefetch {
		if name != steps.PrefetchAURAudit {
			return nil
		}
		cfg := w.auditConfig(store, dry)
		return &auditFetch{Fetch: audit.Prefetch(cfg), cfg: cfg}
	}
}

// auditFetch is a prefetched audit with the config it captured.
type auditFetch struct {
	*audit.Fetch
	cfg audit.Config
}

// Run is the audit step's run when the lookup was prefetched: it waits for
// the fetch rather than starting another. An abort cancels ctx and the
// fetch, so it returns at once either way.
func (f *auditFetch) Run(ctx context.Context) steps.Result {
	res, err := f.Wait(ctx)
	return auditStepResult(f.cfg, res, err)
}

// auditStepResult is the audit as the step's result: the report, a gate on
// topgrade for a high finding, and the snapshot commit.
func auditStepResult(cfg audit.Config, res audit.Result, err error) steps.Result {
	switch {
	case errors.Is(err, audit.ErrNoForeign):
		return steps.Result{Output: "No foreign (AUR or locally built) packages installed — nothing to audit."}
	case err != nil:
		return steps.Result{Output: "Couldn't reach the AUR: " + audit.ShortErr(err, cfg.Deadline), Err: err}
	}
	r := steps.Result{Output: res.Text()}
	if why := res.GateReason(); why != "" {
		r.ConfirmNext = why + " Update anyway?"
	}
	r.Commit = res.Commit(false, r.ConfirmNext != "")
	return r
}

// ── "Delete old downloads" (cmd/archmaid/downloads.go's glue) ──

// deleteOldDownloads is the delete step's Run. An aborted real run's
// result is dropped by the engine, so the step records what it deleted
// itself: when it sees the abort, or from a context.AfterFunc should the
// abort land after it returned but before the result was taken (the
// Driver then cancels with steps.ErrResultTaken, which isn't an abort).
func (w *Wiring) deleteOldDownloads(plan downloads.Plan, store *logstore.Store, opts func() downloads.Options) func(context.Context, steps.Env) steps.Result {
	return func(ctx context.Context, _ steps.Env) steps.Result {
		w.deleting.Add(1)
		defer w.deleting.Done()
		opt := opts()
		r, err := downloads.Delete(ctx, plan, opt)
		res := steps.Result{Output: r.Output, Err: err}
		if opt.DryRun {
			return res
		}
		if ctx.Err() != nil {
			logAbortedDownloads(store, res.Output)
			return res
		}
		if ctx.Done() == nil {
			return res
		}
		w.deleting.Add(1) // until the AfterFunc has run or been disarmed
		stop := context.AfterFunc(ctx, func() {
			defer w.deleting.Done()
			if !errors.Is(context.Cause(ctx), steps.ErrResultTaken) {
				logAbortedDownloads(store, res.Output)
			}
		})
		res.Commit = func() error {
			if stop() {
				w.deleting.Done()
			}
			return nil
		}
		return res
	}
}

func logAbortedDownloads(store *logstore.Store, output string) {
	_ = store.Append(logstore.Cleanup, "ABORT", "Old downloads deleted (aborted)", output)
}
