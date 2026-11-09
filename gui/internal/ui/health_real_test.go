package ui

import (
	"context"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/internal/health"
)

// The real Source maps internal/health's report onto the GUI's types: a
// Deps with nothing wired reaches nothing (every check Unknown), and the
// groups and titles come through.
func TestRealHealthSourceMapsTheReport(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := realHealthSource{d: health.Deps{Now: func() time.Time { return now }}}
	r := s.Run(context.Background())
	if len(r.Checks) != 11 {
		t.Fatalf("%d checks, want 11", len(r.Checks))
	}
	for _, c := range r.Checks {
		if c.ID == "" || c.Title == "" || c.Group == "" {
			t.Errorf("check %+v lacks an id, a title or a group", c)
		}
		if c.State != healthview.Unknown {
			t.Errorf("%s: %v with nothing wired, want unknown", c.ID, c.State)
		}
	}
	if !r.At.Equal(now) {
		t.Errorf("At %v", r.At)
	}
	if r.Attention() != 0 {
		t.Errorf("attention %d: unknown never counts", r.Attention())
	}
	if got := mapHealthState(health.Look); got != healthview.Look {
		t.Errorf("look maps to %v", got)
	}
	if got := mapHealthState(health.OK); got != healthview.OK {
		t.Errorf("ok maps to %v", got)
	}
	if u, err := s.UnreadNews(context.Background()); err == nil && len(u.Items) != 0 {
		t.Errorf("news from nothing: %+v", u)
	}
}
