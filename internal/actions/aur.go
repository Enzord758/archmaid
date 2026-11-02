package actions

import (
	"context"
	"errors"
	"slices"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The AUR install (docs/design/packages-contract.md, section 5) rides the
// Packages run: engine.StartPackages with PkgRequest.AUR set. The discovery
// resolves, fetches and reviews as the user (Env.PlanAUR), asks the helper for
// the plan of the repository dependencies (Env.PlanPackages, --asdeps), and
// hands the sheet both. The queue a "yes" runs is built by AURPrepared.Queue.

// AURPrepared is a planned AUR install.
type AURPrepared struct {
	// Plans are the review payloads in build order, with the audit's
	// findings; the sheet shows them.
	Plans []steps.AURPlan
	// RepoDeps are the sync packages to install first, --asdeps ("" = none).
	RepoDeps []string
	// Notes are short sanitized lines for the sheet ("The maintainer audit
	// could not run: ...").
	Notes []string
	// Queue builds the steps of a "yes": deps is the root step that installs
	// RepoDeps (nil if there are none), the rest is the user-side build and
	// the final root install (aurbuild.Fetcher.Queue).
	Queue func(deps *steps.Step) []steps.Step
}

// AURDiscover is PackagesDiscover for an AUR request. The helper's plan of
// the repository dependencies, when there are some, is the preview's Plan
// (the root step for them is built from it); the AUR plans ride in Plan.AUR.
func AURDiscover(req steps.PkgRequest) func(ctx context.Context, env Env) Discovery {
	return func(ctx context.Context, env Env) Discovery {
		if !env.Polkit || env.PlanAUR == nil || env.PlanPackages == nil {
			return Notice{Text: "Couldn't plan the AUR install.",
				Err: "installing AUR packages needs the root helper (polkit); it is not available here"}
		}
		prep, err := env.PlanAUR(ctx, req.Names)
		var pr *steps.PkgRefusal
		switch {
		case errors.As(err, &pr):
			return PkgRefused{Req: req, Refusal: pr}
		case err != nil:
			return Notice{Text: "Couldn't plan the AUR install.", Err: sanitize.OneLine(err.Error())}
		case len(prep.Plans) == 0 || prep.Queue == nil:
			return Notice{Text: "The AUR install would do nothing."}
		}
		plan := steps.PkgPlan{Mode: steps.PkgInstall, AsDeps: len(prep.RepoDeps) > 0, Targets: slices.Clone(req.Names)}
		var deps *steps.Step
		if len(prep.RepoDeps) > 0 {
			dreq := steps.PkgRequest{Mode: steps.PkgInstall, Names: prep.RepoDeps, AsDeps: true}
			dplan, err := env.PlanPackages(ctx, dreq)
			switch {
			case errors.As(err, &pr):
				return PkgRefused{Req: req, Refusal: pr}
			case err != nil:
				return Notice{Text: "Couldn't plan the AUR install.", Err: sanitize.OneLine(err.Error())}
			}
			plan = dplan
			plan.Warnings = slices.Clone(dplan.Warnings)
			dstep := Packages(dplan)[0]
			deps = &dstep
			plan.Targets = slices.Clone(req.Names) // the sheet lists what was asked for
		}
		plan.AUR = prep.Plans
		for _, n := range prep.Notes {
			plan.Warnings = append(plan.Warnings, sanitize.OneLine(n))
		}
		return PkgPreview{Plan: plan, Steps: prep.Queue(deps)}
	}
}
