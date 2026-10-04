package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── AUR audit: the tea glue ──────────────────────────────────────────────────
//
// The audit itself — the RPC lookup, classification, the maintainer snapshot
// and its atomic, locked save, the plain-text report — lives in
// internal/audit. This file builds its Config from the TUI's settings, turns
// it into the menu action's discovery Cmd and the in-process step that gates
// topgrade, keeps the prefetch on the model, and renders the styled report.

// aurRPCURL is the AUR RPC endpoint. A var so tests can point it at an
// httptest server.
var aurRPCURL = audit.DefaultRPCURL

// auditDeadline bounds a whole audit (see audit.Config.Deadline). A var so
// tests can shrink it.
var auditDeadline = audit.DefaultDeadline

// foreignPackages lists installed packages that aren't in any sync repo
// (pacman -Qqm). Empty when there are none (pacman exits 1); any other
// failure reads as none too, and audit.Run reports that as ErrNoForeign. When
// ctx ends first, Run sees that itself.
func foreignPackages(ctx context.Context) ([]string, error) {
	out, _ := proc.Query(ctx, "pacman", "-Qqm")
	return nonEmptyLines(string(out)), nil
}

// currentAuditConfig is every setting an audit reads, taken from the package
// vars: the AUR endpoint, the snapshot file (read, and later written by the
// result's Commit), the deadline, whether this is a dry run, and the log
// store its entries go to. Call it on the model's goroutine, before the audit
// goes off on its own (the menu action's Cmd, the step's run, a prefetch), so
// the audit never reads those vars while something else (a test, mostly)
// changes them.
func currentAuditConfig() audit.Config {
	store := logs()
	return audit.Config{
		RPCURL:       aurRPCURL,
		SnapshotPath: filepath.Join(store.Root(), audit.SnapshotFile),
		StatePath:    filepath.Join(store.Root(), audit.StateFile),
		Deadline:     auditDeadline,
		DryRun:       dryRun,
		Foreign:      foreignPackages,
		// A Store never changes once made, so this is safe off the model's
		// goroutine. Like appendLog, a failed write is dropped.
		Log: func(kind, label, body string) { _ = store.Append(logCategoryAudit, kind, label, body) },
	}
}

// auditOutcome is the menu audit's result: a notice for "nothing to audit" /
// "couldn't reach the AUR", or the rendered report for the audit screen with
// commit accepting its high findings (`a`; nil when there are none), and
// the result itself for a front end that draws its own report.
type auditOutcome struct {
	notice, report string
	commit         func() error
	res            *audit.Result
}

// discovery is o as the AUR-audit action's actions.Discovery.
func (o auditOutcome) discovery() actions.Discovery {
	if o.notice != "" {
		return actions.Notice{Text: o.notice}
	}
	return actions.Report{Text: o.report, Accept: o.commit, Audit: o.res}
}

// menuAudit is the menu action's audit (actions.Env.AuditReport, with the
// config captured on the model's goroutine; see actionEnv).
func menuAudit(now time.Time, cfg audit.Config) auditOutcome {
	res, err := audit.Run(context.Background(), cfg, now)
	switch {
	case errors.Is(err, audit.ErrNoForeign):
		return auditOutcome{notice: "No foreign (AUR or locally built) packages installed."}
	case err != nil:
		return auditOutcome{notice: "Couldn't reach the AUR: " + audit.ShortErr(err, cfg.Deadline)}
	}
	res.LogSummary()
	msg := auditOutcome{report: renderAudit(res), res: &res}
	// Advance the snapshot for everything but the high findings (with none,
	// that's all of it); those stay flagged until `a` on the audit screen
	// runs msg.commit to acknowledge them.
	if save := res.Commit(true, false); save != nil {
		save()
	}
	if res.High() > 0 {
		msg.commit = res.Commit(false, true)
	}
	return msg
}

// auditStep is the first step of Full Cleanup and of Run topgrade: the audit,
// run in-process. It sits directly in front of topgrade so that a
// high-severity finding (an adoption, a maintainer change or an added
// co-maintainer) can put a confirm in front of the update — the one step that would build a taken-over AUR
// package. Declining skips only the update; in Full Cleanup the rest of the
// cleanup still runs. The audit can't *hold back* a single package; that's the
// yay cooldown hook's job (see HANDOVER.md).
//
// AlwaysLog: the step's own log entry is the audit record, so unlike the menu
// action it doesn't call LogSummary. It never needs sudo. An abort cancels the
// step's ctx, which ends the lookup at once; the late result is then dropped by
// stepSeq like any other.
//
// Its Prefetch has activate start the lookup as an auditFetch
// the moment the action is chosen, so it runs while the password prompt is
// open; launch then awaits that instead of calling run.
//
// Without a prefetch, run audits the config captured here, when the queue is
// built on the model's goroutine (the moment a prefetch would capture it),
// not when run executes in its Cmd.
func auditStep() steps.Step {
	cfg := currentAuditConfig()
	return steps.Step{
		ID:    steps.IDAURAudit,
		Title: "Auditing AUR packages…", LogLabel: "AUR audit",
		Category: logCategoryAudit, AlwaysLog: true,
		Run: func(ctx context.Context, _ steps.Env) steps.Result {
			res, err := audit.Run(ctx, cfg, time.Now())
			return auditStepResult(cfg, res, err)
		},
		Prefetch: steps.PrefetchAURAudit,
	}
}

// auditFetch is the current action's audit, prefetched (audit.Prefetch) with
// the config it captured. The model owns it (model.auditFetch) and cancels it
// when the run is abandoned before or during the step: esc on the password
// prompt, a declined gate, an abort.
type auditFetch struct {
	*audit.Fetch
	cfg audit.Config
}

// startAuditFetch starts the audit with the config read here, on the model's
// goroutine.
func startAuditFetch() *auditFetch {
	cfg := currentAuditConfig()
	return &auditFetch{Fetch: audit.Prefetch(cfg), cfg: cfg}
}

// Run is the audit step's run when the lookup was prefetched: it waits for
// the fetch rather than starting another. An abort cancels both ctx and the
// fetch, so it returns at once either way.
func (f *auditFetch) Run(ctx context.Context) steps.Result {
	res, err := f.Wait(ctx)
	return auditStepResult(f.cfg, res, err)
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
	// Run by handleStepResult right away when nothing gates, otherwise only if
	// the gated update is accepted: declining leaves the snapshot as it was,
	// so the same change gates the next run too.
	r.Commit = res.Commit(false, r.ConfirmNext != "")
	return r
}

// shortErr trims an error to something that fits a notice popup (see
// audit.ShortErr); a deadline it names is auditDeadline.
func shortErr(err error) string { return audit.ShortErr(err, auditDeadline) }

// renderAudit builds the styled report shown on the audit screen.
func renderAudit(res audit.Result) string {
	var b strings.Builder
	b.WriteString(stepRunStyle.Render(fmt.Sprintf("%d foreign package(s) checked against the AUR", res.Total)) + "\n\n")

	high := res.High()
	b.WriteString(fmt.Sprintf("%s  %s  %s\n\n",
		stepFailStyle.Render(fmt.Sprintf("✖ %d look closely", high)),
		stepSkipStyle.Render(fmt.Sprintf("⚠ %d glance", len(res.Findings)-high)),
		stepOKStyle.Render(fmt.Sprintf("✔ %d clean", res.Clean())),
	))

	width := audit.NameWidth(res.Findings)
	for _, f := range res.Findings {
		glyph, style := "⚠", stepSkipStyle
		if f.High {
			glyph, style = "✖", stepFailStyle
		}
		b.WriteString(style.Render(glyph) + " " + itemStyle.Render(f.Line(width)) + "\n")
	}
	if len(res.Findings) == 0 {
		b.WriteString(stepOKStyle.Render("Nothing flagged.") + "\n")
	}

	b.WriteString("\n")
	if note := res.Note(); note != "" {
		b.WriteString(statusStyle.Render(note) + "\n")
	}
	if high > 0 && !res.DryRun() {
		b.WriteString(statusStyle.Render("Flagged maintainers stay flagged on every audit until you've checked them and press a to accept.") + "\n")
	}
	b.WriteString(statusStyle.Render("Recent = pushed within 72h, the same window as the yay cooldown hook."))
	return b.String()
}
