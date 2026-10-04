package actions

import (
	"context"
	"time"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// discoverDownloads resolves the folder and lists its old entries. Deleting
// them is an in-process step that re-checks each entry first (env's
// DeleteDownloads; the safety properties are internal/downloads').
func discoverDownloads(_ context.Context, env Env) Discovery {
	opt := env.Downloads()
	dir, err := downloads.Resolve(opt)
	if err != nil {
		return Notice{sanitize.String(err.Error(), sanitize.Plain)}
	}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	plan, err := downloads.Scan(dir, now(), opt)
	if err != nil {
		return Notice{err.Error()}
	}
	if len(plan.Names) == 0 {
		return Notice{"Nothing older than 30 days in " + plan.Shown + "."}
	}
	return Pending{[]steps.Step{{
		ID:    steps.IDDownloads,
		Title: "Deleting old downloads…", LogLabel: "Old downloads deleted",
		Run:      env.DeleteDownloads(plan),
		Optional: true, AlwaysLog: true,
		Confirm: plan.Confirm(),
	}}}
}
