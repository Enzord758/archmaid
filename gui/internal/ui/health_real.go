package ui

import (
	"context"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/internal/health"
)

// realHealth is the window's Source over internal/health (#269): read-only,
// no root, no password. A run is up to health.Deadline (15 s) when the
// news, AUR and mirror caches are cold, so the window calls it off the main
// thread (health.go).
func realHealth() healthview.Source { return realHealthSource{d: health.DefaultDeps()} }

type realHealthSource struct{ d health.Deps }

func (s realHealthSource) Run(ctx context.Context) healthview.Report {
	r := health.Run(ctx, s.d)
	out := healthview.Report{At: r.At, Checks: make([]healthview.Check, 0, len(r.Checks))}
	for _, c := range r.Checks {
		out.Checks = append(out.Checks, healthview.Check{
			ID: c.ID, Title: c.Title, Group: c.Group, Detail: c.Detail, Copy: c.Copy, Link: c.Link,
			State: mapHealthState(c.State),
		})
	}
	return out
}

func mapHealthState(s health.State) healthview.State {
	switch s {
	case health.OK:
		return healthview.OK
	case health.Look:
		return healthview.Look
	}
	return healthview.Unknown
}

func (s realHealthSource) UnreadNews(ctx context.Context) (healthview.Unread, error) {
	u, err := health.UnreadNews(ctx, s.d)
	if err != nil {
		return healthview.Unread{}, err
	}
	out := healthview.Unread{Since: u.Since, FromCache: u.FromCache, CacheAge: u.CacheAge}
	for _, it := range u.Items {
		out.Items = append(out.Items, healthview.NewsItem{Title: it.Title, Link: it.Link, Date: it.Date})
	}
	return out, nil
}
