package frontend

import (
	"context"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// AURPreparer is what AURPlanner needs of an aurbuild.Planner.
type AURPreparer interface {
	Prepare(ctx context.Context, names []string) (aurbuild.Prepared, error)
}

// AURPlanner is Env.PlanAUR over an aurbuild planner: it prepares the
// install and turns the answer into what the actions and the engine use. A
// refusal of aurbuild (nothing fetched, nothing built) becomes a
// *steps.PkgRefusal; the queue of a "yes" is the fetcher's.
func AURPlanner(p AURPreparer, f aurbuild.Fetcher, reviewed *aurbuild.Reviewed) func(ctx context.Context, names []string) (actions.AURPrepared, error) {
	return func(ctx context.Context, names []string) (actions.AURPrepared, error) {
		prep, err := p.Prepare(ctx, names)
		if r, ok := aurbuild.IsRefusal(err); ok {
			reason := steps.RefuseAUR
			if r.Reason == aurbuild.ReasonNotFound {
				reason = steps.RefuseNotFound
			}
			var pkgs []string
			if r.Package != "" {
				pkgs = []string{r.Package}
			}
			return actions.AURPrepared{}, &steps.PkgRefusal{Reason: reason, Names: pkgs, Detail: r.Detail}
		}
		if err != nil {
			return actions.AURPrepared{}, err
		}
		out := actions.AURPrepared{
			Plans: prep.Plans, RepoDeps: prep.RepoDeps,
			Queue: func(deps *steps.Step) []steps.Step { return f.Queue(prep.Plans, deps, reviewed) },
		}
		// High findings: the sheet's yes accepts them, which commits the
		// audit snapshot as topgrade's accept does (Result.Commit(false,
		// true): the highs advance too, ACCEPT is logged). Without any, the
		// snapshot stays as it was. A dry run has no commit (nil).
		if prep.HasAudit && prep.HighFindings() > 0 {
			out.Accept = prep.Audit.Commit(false, true)
		}
		if prep.AuditNote != "" {
			out.Notes = append(out.Notes, sanitize.OneLine(prep.AuditNote))
		}
		return out, nil
	}
}
