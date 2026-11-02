package actions

import (
	"context"
	"errors"
	"slices"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's run (docs/design/packages-contract.md, section 3). It
// is not a menu entry: it has parameters, so a front end starts it with
// engine.StartPackages, and the engine runs PackagesDiscover under the
// action ID IDPackages. GUI and polkit only: under sudo there is no
// install or remove, and the discovery says so.

// IDPackages is the action ID of a Packages run (QueueStarted and Finished
// carry it). It is not in Menu.
const IDPackages = "packages"

// PkgPreview is a Packages discovery that planned the transaction: Plan is
// what the sheet shows, Steps the queue a "yes" runs.
type PkgPreview struct {
	Plan  steps.PkgPlan
	Steps []steps.Step
}

// PkgRefused is a Packages discovery the helper refused (nothing changed):
// the request and the reason.
type PkgRefused struct {
	Req     steps.PkgRequest
	Refusal *steps.PkgRefusal
}

func (PkgPreview) discovery() {}
func (PkgRefused) discovery() {}

// PackagesDiscover is the discovery of a Packages run for req: the helper's
// plan (Env.PlanPackages), then PkgPreview, or PkgRefused when the helper
// refused, or a Notice when it could not plan at all.
func PackagesDiscover(req steps.PkgRequest) func(ctx context.Context, env Env) Discovery {
	return func(ctx context.Context, env Env) Discovery {
		if !env.Polkit || env.PlanPackages == nil {
			return Notice{Text: "Couldn't plan the package change.",
				Err: "installing and removing packages needs the root helper (polkit); it is not available here"}
		}
		plan, err := env.PlanPackages(ctx, req)
		var pr *steps.PkgRefusal
		switch {
		case errors.As(err, &pr):
			return PkgRefused{Req: req, Refusal: pr}
		case err != nil:
			return Notice{Text: "Couldn't plan the package change.", Err: sanitize.OneLine(err.Error())}
		case len(plan.Rows) == 0:
			return Notice{Text: "The package change would do nothing."}
		}
		return PkgPreview{Plan: plan, Steps: Packages(plan)}
	}
}

// Packages is the queue of a confirmed plan: one root step, the helper's
// pkg-install or pkg-remove, which gets only the plan's hash and names.
func Packages(p steps.PkgPlan) []steps.Step {
	st := steps.Step{
		ID: steps.IDPkgInstall, Verb: steps.PkgVerbInstall,
		Title: "Installing packages…", LogLabel: "Packages installed",
		Items: slices.Clone(p.Targets), PkgPlan: &p,
		NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup,
	}
	if p.Mode == steps.PkgRemove {
		st.ID, st.Verb = steps.IDPkgRemove, steps.PkgVerbRemove
		st.Title, st.LogLabel = "Removing packages…", "Packages removed"
	}
	return []steps.Step{st}
}
