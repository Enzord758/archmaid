package frontend

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── AUR audit: the front-end glue ────────────────────────────────────────────
//
// The audit itself — the RPC lookup, classification, the maintainer snapshot
// and its atomic, locked save, the plain-text report — lives in
// internal/audit. This file builds its Config from a front end's settings,
// turns it into the menu action's discovery and the in-process step that
// gates topgrade, and prefetches the step's lookup for the Driver. Drawing
// the report is the front end's (Render).

// AuditOptions are a front end's audit settings. The zero value is the real
// thing; tests point RPCURL at an httptest server and swap Foreign.
type AuditOptions struct {
	// RPCURL is the AUR RPC endpoint; "" is audit.DefaultRPCURL.
	RPCURL string
	// Deadline bounds a whole audit (audit.Config.Deadline); 0 is
	// audit.DefaultDeadline.
	Deadline time.Duration
	// DryRun leaves the snapshot and state files alone.
	DryRun bool
	// Foreign lists the foreign packages to check; nil is ForeignPackages.
	Foreign func(ctx context.Context) ([]string, error)
}

// AuditConfig is every setting an audit reads: o, with the snapshot and
// state files in store's root and the audit's log entries going to store.
// A front end calls it before the audit goes off on its own (when the
// queue is built, the prefetch starts or the menu audit's Env is made), so
// the audit never reads the front end's settings while they change.
func AuditConfig(store *logstore.Store, o AuditOptions) audit.Config {
	cfg := audit.Config{
		RPCURL:       o.RPCURL,
		SnapshotPath: filepath.Join(store.Root(), audit.SnapshotFile),
		StatePath:    filepath.Join(store.Root(), audit.StateFile),
		Deadline:     o.Deadline,
		DryRun:       o.DryRun,
		Foreign:      o.Foreign,
		// A Store never changes once made, so this is safe off any
		// goroutine. A failed write is dropped: logging must never break a
		// step.
		Log: func(kind, label, body string) { _ = store.Append(logstore.Audit, kind, label, body) },
	}
	if cfg.RPCURL == "" {
		cfg.RPCURL = audit.DefaultRPCURL
	}
	if cfg.Deadline == 0 {
		cfg.Deadline = audit.DefaultDeadline
	}
	if cfg.Foreign == nil {
		cfg.Foreign = ForeignPackages
	}
	return cfg
}

// ForeignPackages lists installed packages that aren't in any sync repo
// (pacman -Qqm). Empty when there are none (pacman exits 1); any other
// failure reads as none too, and audit.Run reports that as ErrNoForeign.
// When ctx ends first, Run sees that itself.
func ForeignPackages(ctx context.Context) ([]string, error) {
	out, _ := proc.Query(ctx, "pacman", "-Qqm")
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

// MenuAudit is the menu action's audit (actions.Env.AuditReport): a Notice
// for "nothing to audit", "couldn't reach the AUR" or a stopped audit, or
// the Report, its Text drawn by render (nil: the plain-text res.Text()),
// its Audit the result for a front end that draws its own, and Accept
// acknowledging the high findings (nil when there are none).
//
// ctx is the discovery's: an abort or the Driver's Close cancels it, which
// ends the lookup at once, and an audit cancelled after its lookup still
// writes no snapshot (no one will see its report).
func MenuAudit(ctx context.Context, cfg audit.Config, now time.Time, render func(audit.Result) string) actions.Discovery {
	res, err := audit.Run(ctx, cfg, now)
	if ctx.Err() != nil {
		return actions.Notice{Text: "The AUR audit was stopped."}
	}
	switch {
	case errors.Is(err, audit.ErrNoForeign):
		return actions.Notice{Text: "No foreign (AUR or locally built) packages installed."}
	case err != nil:
		return actions.Notice{Text: "Couldn't reach the AUR: " + audit.ShortErr(err, cfg.Deadline)}
	}
	res.LogSummary()
	if render == nil {
		render = audit.Result.Text
	}
	r := actions.Report{Text: render(res), Audit: &res}
	// Advance the snapshot for everything but the high findings (with none,
	// that's all of it); those stay flagged until the report's Accept
	// acknowledges them.
	if save := res.Commit(true, false); save != nil {
		save()
	}
	if res.High() > 0 {
		r.Accept = res.Commit(false, true)
	}
	return r
}

// AuditStep is the first step of Full Cleanup and of Run topgrade: the
// audit, run in-process. It sits directly in front of topgrade so that a
// high-severity finding (an adoption, a maintainer change or an added
// co-maintainer) can put a confirm in front of the update — the one step
// that would build a taken-over AUR package. Declining skips only the
// update; in Full Cleanup the rest of the cleanup still runs. The audit
// can't *hold back* a single package; that's the yay cooldown hook's job
// (see HANDOVER.md).
//
// AlwaysLog: the step's own log entry is the audit record, so unlike the
// menu action it doesn't call LogSummary. It never needs sudo. An abort
// cancels the step's ctx, which ends the lookup at once; the engine then
// drops the late result like any other.
//
// Its Prefetch has the engine start the lookup (Prefetcher's AuditFetch)
// the moment the action is chosen, so it runs while the password prompt is
// open; the step then awaits that instead of calling Run. Without a
// prefetch (a Driver with no Deps.Prefetch), Run audits cfg, captured when
// the queue is built (the moment a prefetch would capture it).
func AuditStep(cfg audit.Config) steps.Step {
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

// AuditFetch is an action's audit, prefetched (audit.Prefetch) with the
// config it captured: a driver.Prefetch. The Driver owns it (Deps.Prefetch)
// and cancels it when the run is abandoned before or during the step: esc
// on the password prompt, a declined gate, an abort, Close.
type AuditFetch struct {
	*audit.Fetch
	cfg audit.Config
}

// StartAuditFetch starts the audit's lookup with cfg.
func StartAuditFetch(cfg audit.Config) *AuditFetch {
	return &AuditFetch{Fetch: audit.Prefetch(cfg), cfg: cfg}
}

// Run is the audit step's run when the lookup was prefetched: it waits for
// the fetch rather than starting another. An abort cancels both ctx and the
// fetch, so it returns at once either way.
func (f *AuditFetch) Run(ctx context.Context) steps.Result {
	res, err := f.Wait(ctx)
	return auditStepResult(f.cfg, res, err)
}

// Prefetcher is driver.Deps.Prefetch for a front end: the AUR audit's
// lookup, so it runs behind the password prompt or the gate, and nil for
// any other name. cfg is called on the Driver's loop, once per prefetch
// started, and only for the audit.
func Prefetcher(cfg func() audit.Config) func(name string) driver.Prefetch {
	return func(name string) driver.Prefetch {
		if name != steps.PrefetchAURAudit {
			return nil
		}
		return StartAuditFetch(cfg())
	}
}

// auditStepResult turns an audit into the step's result: the plain-text
// report, a gate on topgrade for a high finding, and the snapshot commit.
func auditStepResult(cfg audit.Config, res audit.Result, err error) steps.Result {
	switch {
	case errors.Is(err, audit.ErrNoForeign):
		return steps.Result{Output: "No foreign (AUR or locally built) packages installed — nothing to audit."}
	case err != nil:
		return steps.Result{Output: "Couldn't reach the AUR: " + audit.ShortErr(err, cfg.Deadline), Err: err}
	}
	r := steps.Result{Output: res.Text()}
	if why := res.GateReason(); why != "" {
		// Name the packages: the popup covers the report that explains it.
		r.ConfirmNext = why + " Update anyway?"
	}
	// Run by the engine as it takes the result when nothing gates, otherwise
	// only if the gated update is accepted: declining leaves the snapshot as
	// it was, so the same change gates the next run too.
	r.Commit = res.Commit(false, r.ConfirmNext != "")
	return r
}
