package aurbuild

import (
	"context"
	"fmt"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Planner is everything before the sheet: resolve, fetch and review every
// base (in build order), with the maintainer audit running meanwhile. It
// executes nothing from any repo.
type Planner struct {
	Fetcher  Fetcher
	Repo     Repo
	AUR      aursearch.Searcher
	Reviewed *Reviewed
	// Audit, if not nil, is the maintainer audit's configuration (the
	// frontend's AuditConfig); nil skips the audit.
	Audit *audit.Config
	// Now is the clock for the audit; nil means time.Now.
	Now func() time.Time
}

// Prepared is a planned AUR install, ready for the sheet.
type Prepared struct {
	Resolution Resolution
	// Plans are the review payloads, in build order.
	Plans []steps.AURPlan
	// RepoDeps are the sync packages to install first (--asdeps).
	RepoDeps []string
	// Audit is the maintainer audit's result when it ran (HasAudit); the
	// caller commits its snapshot when the install is accepted.
	Audit    audit.Result
	HasAudit bool
	// AuditNote says why the audit did not run or finish ("" if it did).
	AuditNote string
}

// HighFindings counts the high audit findings across all plans.
func (p Prepared) HighFindings() int {
	n := 0
	for _, pl := range p.Plans {
		for _, f := range pl.Audit {
			if f.High {
				n++
			}
		}
	}
	return n
}

// Prepare plans the install of names. Refusals come back as *Refusal; a
// network or git failure as a plain error.
func (p Planner) Prepare(ctx context.Context, names []string) (Prepared, error) {
	res, err := Resolve(ctx, names, p.Repo, p.AUR)
	if err != nil {
		return Prepared{}, err
	}
	out := Prepared{Resolution: res, RepoDeps: res.RepoDeps}

	var all []string
	for _, b := range res.Bases {
		all = append(all, b.Names...)
	}
	type audited struct {
		res      audit.Result
		findings []steps.AURFinding
		err      error
	}
	var auditCh chan audited
	if p.Audit != nil {
		now := time.Now
		if p.Now != nil {
			now = p.Now
		}
		actx, cancel := context.WithCancel(ctx)
		defer cancel()
		auditCh = make(chan audited, 1)
		go func() {
			r, f, err := RunAudit(actx, *p.Audit, all, now())
			auditCh <- audited{r, f, err}
		}()
	}

	for _, b := range res.Bases {
		co, err := p.Fetcher.Fetch(ctx, b.Name)
		if err != nil {
			return Prepared{}, fmt.Errorf("fetching %s: %w", b.Name, err)
		}
		prev := ""
		if p.Reviewed != nil {
			prev = p.Reviewed.Get(b.Name)
		}
		plan, err := p.Fetcher.Review(ctx, co, b, res, prev)
		if err != nil {
			return Prepared{}, err
		}
		out.Plans = append(out.Plans, plan)
	}

	if auditCh != nil {
		a := <-auditCh
		switch {
		case a.err != nil:
			out.AuditNote = "The maintainer audit could not run: " + sanitize.Clip(sanitize.OneLine(a.err.Error()), 160)
		default:
			out.Audit, out.HasAudit = a.res, true
			for i := range out.Plans {
				for _, f := range a.findings {
					if contains(out.Plans[i].Names, f.Name) {
						out.Plans[i].Audit = append(out.Plans[i].Audit, f)
					}
				}
			}
		}
	}
	return out, nil
}
