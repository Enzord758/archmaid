package ui

import (
	"context"
	"errors"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/internal/health"
)

// realHealth is the window's Source over internal/health (#269): read-only,
// no root, no password. A run is up to health.Deadline (15 s) when the
// news, AUR and mirror caches are cold, so the window calls it off the main
// thread (health.go). It is an Ignorer too (#288): Ignore and Undo write
// health-ignore.json through the store, which is the only thing it writes;
// stateDir "" is archmaid's real state folder, a test passes its own.
func realHealth() healthview.Source { return realHealthAt("") }

func realHealthAt(stateDir string) healthview.Source {
	st := &health.IgnoreStore{Dir: stateDir}
	d := health.DefaultDeps()
	d.IgnoreLoad = st.Load
	return realHealthSource{d: d, store: st}
}

type realHealthSource struct {
	d     health.Deps
	store *health.IgnoreStore
}

func (s realHealthSource) Run(ctx context.Context) healthview.Report {
	r := health.Run(ctx, s.d)
	// The widget reads health.json: keep it in step with what the window shows
	// (the same run, the same ignore list). Best effort; a failure is not the window's.
	if s.store != nil {
		_ = health.WriteReportFile(s.store.Dir, r)
	}
	out := healthview.Report{At: r.At, Checks: make([]healthview.Check, 0, len(r.Checks))}
	for _, c := range r.Checks {
		out.Checks = append(out.Checks, healthview.Check{
			ID: c.ID, Title: c.Title, Group: c.Group, Detail: c.Detail, Copy: c.Copy, Link: c.Link,
			State: mapHealthState(c.State), Items: mapItems(c.Items), IgnoredItems: mapItems(c.IgnoredItems),
		})
	}
	for _, it := range r.Ignored() {
		out.IgnoredList = append(out.IgnoredList, healthview.IgnoredItem{
			CheckID: it.CheckID, Title: it.Title, Item: healthview.Item{Key: it.Item.Key, Label: it.Item.Label}, Since: it.Since,
		})
	}
	return out
}

func mapItems(in []health.Item) []healthview.Item {
	var out []healthview.Item
	for _, it := range in {
		out = append(out, healthview.Item{Key: it.Key, Label: it.Label})
	}
	return out
}

func mapHealthState(s health.State) healthview.State {
	switch s {
	case health.OK:
		return healthview.OK
	case health.Look:
		return healthview.Look
	case health.Ignored:
		return healthview.Ignored
	}
	return healthview.Unknown
}

// Ignore and Unignore put a key on the ignore list or take it off.
func (s realHealthSource) Ignore(key string) error {
	if s.store == nil {
		return errors.New("no ignore list")
	}
	return s.store.Ignore(key)
}

func (s realHealthSource) Unignore(key string) error {
	if s.store == nil {
		return errors.New("no ignore list")
	}
	return s.store.Unignore(key)
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
