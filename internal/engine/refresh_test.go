package engine_test

import (
	"errors"
	"testing"

	. "github.com/Enzord758/archmaid/internal/engine"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
)

// RefreshUpdates (the widget's update count, re-read at once) goes out
// exactly once, with the Finished of a real run whose topgrade step
// started: completed, failed, or aborted while it ran. Never for a dry
// run, a gate or confirm that was declined, an abort before topgrade, or
// an action without a topgrade step.
func TestSessionRefreshUpdates(t *testing.T) {
	tg := child(steps.IDTopgrade, "topgrade", "-y")
	boom := errors.New("exit status 1")
	declined := tg
	declined.Confirm = "Run topgrade?"

	for _, c := range []struct {
		name  string
		opts  Options
		menu  actions.Action
		drive func(h *harness) Output
		want  int
	}{
		{"completed", Options{}, queue("q", child("a", "true"), tg),
			func(h *harness) Output { h.ok(); return h.ok() }, 1},
		{"failed", Options{}, queue("q", tg, child("b", "true")),
			func(h *harness) Output { h.exit(boom); return h.ok() }, 1},
		{"aborted while topgrade runs", Options{}, queue("q", tg, child("b", "true")),
			func(h *harness) Output { return h.abort() }, 1},
		{"topgrade only, then a failing step", Options{}, queue("q", tg, child("b", "x")),
			func(h *harness) Output { h.ok(); return h.exit(boom) }, 1},
		{"dry run", Options{DryRun: true}, queue("q", tg),
			func(h *harness) Output { return h.ok() }, 0},
		{"abort before topgrade", Options{}, queue("q", child("a", "sleep"), tg),
			func(h *harness) Output { return h.abort() }, 0},
		{"topgrade declined", Options{}, queue("q", child("a", "true"), declined),
			func(h *harness) Output { h.ok(); return h.confirm(false) }, 0},
		{"gate declined", Options{}, func() actions.Action {
			a := queue("q", tg)
			a.Gate = "Run a system update?"
			return a
		}(), func(h *harness) Output { return h.confirm(false) }, 0},
		{"no topgrade step", Options{}, queue("q", child("a", "true")),
			func(h *harness) Output { return h.ok() }, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.opts, c.menu)
			h.start("q")
			o := c.drive(h)
			event[Finished](t, o)
			if got := len(all[RefreshUpdates](o.Effects)); got != c.want {
				t.Errorf("RefreshUpdates effects = %d, want %d", got, c.want)
			}
		})
	}
}
