// Package frontend is the glue every archmaid front end hands the engine:
// the AUR audit as the menu action's discovery and as the step that gates
// topgrade (with its prefetch for the Driver), the downloads delete step
// with its abort record, and the actions.Env that carries them. The TUI
// (internal/tui) and the GUI (gui/) differ in how they draw and in where
// their settings come from, not in this logic, so it lives here once.
//
// It is UI-free: it may import neither Bubble Tea, lipgloss and bubbles
// nor gotk4 (scripts/check.sh, layering). What a front end draws — the
// styled audit report, say — it passes in (Glue.Render).
package frontend

import (
	"context"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Glue is what a front end hands Env: where its settings come from, and
// how it draws the menu audit's report.
type Glue struct {
	// AuditConfig is the audit's config (usually AuditConfig over the
	// front end's settings). Env calls it once, for the menu audit, and
	// the audit step calls it each time the Session builds a queue, on the
	// Driver's loop.
	AuditConfig func() audit.Config
	// Render draws the menu audit's report (actions.Report.Text); nil is
	// the plain-text res.Text().
	Render func(audit.Result) string
	// Downloads is Env.Downloads when the base Env has none: read when the
	// discovery runs and again when the delete step runs, on a Driver
	// worker.
	Downloads func() downloads.Options
	// Store gets the delete step's abort record.
	Store *logstore.Store
	// Deletions counts the delete steps in flight; the front end Waits on
	// it before it exits.
	Deletions *Deletions
}

// Env completes base with g: AuditStep, AuditReport and DeleteDownloads,
// and Downloads unless base sets one (the delete step then reads base's).
// Everything else in base (DryRun, LookPath, Polkit, PlanOrphans,
// RepairWorkers) is the front end's and stays as given.
//
// What it reads when: here, the menu audit's config (captured, so the
// discovery never looks it up); when a queue is built, the audit step's;
// when the delete step runs, its Downloads options.
func (g Glue) Env(base actions.Env) actions.Env {
	e := base
	menuCfg, render := g.AuditConfig(), g.Render
	e.AuditStep = func() steps.Step { return AuditStep(g.AuditConfig()) }
	e.AuditReport = func(ctx context.Context) actions.Discovery {
		return MenuAudit(ctx, menuCfg, time.Now(), render)
	}
	if e.Downloads == nil {
		e.Downloads = g.Downloads
	}
	opts, store, del := e.Downloads, g.Store, g.Deletions
	e.DeleteDownloads = func(plan downloads.Plan) func(context.Context, steps.Env) steps.Result {
		return del.Step(plan, store, opts)
	}
	return e
}
