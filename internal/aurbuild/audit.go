package aurbuild

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// RunAudit runs the maintainer audit (audit.Run) with the AUR packages of
// this install added to the installed foreign ones, so the snapshot the audit
// compares with and later saves is not shrunk to the new names (the new ones
// are a baseline, "first seen", not a high finding). It returns the audit's
// Result (the caller wires Result.Commit on the sheet's accept, as the menu
// audit's gate does) and the findings about names, for the sheet.
//
// cfg.Foreign may be nil (then the names alone are audited). The audit runs
// while the repos are fetched; it makes the one RPC call audit always makes.
func RunAudit(ctx context.Context, cfg audit.Config, names []string, now time.Time) (audit.Result, []steps.AURFinding, error) {
	listed := cfg.Foreign
	cfg.Foreign = func(ctx context.Context) ([]string, error) {
		var out []string
		if listed != nil {
			l, err := listed(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, l...)
		}
		return dedupe(append(out, names...)), nil
	}
	res, err := audit.Run(ctx, cfg, now)
	if err != nil {
		return res, nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []steps.AURFinding
	for _, f := range res.Findings {
		if !want[f.Name] {
			continue
		}
		reason := f.Reason
		if reason == "" {
			reason = strings.Join(f.Flags, ", ")
		}
		out = append(out, steps.AURFinding{
			Name: sanitize.Field(f.Name, audit.FieldMax), High: f.High,
			Reason: sanitize.Clip(sanitize.OneLine(reason), 200),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].High != out[j].High {
			return out[i].High
		}
		return out[i].Name < out[j].Name
	})
	return res, out, nil
}
