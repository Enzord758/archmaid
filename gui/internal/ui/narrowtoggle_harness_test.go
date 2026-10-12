package ui

// The narrow breakpoint toggled back and forth with a run going on (QA,
// batch 6): the window starts wide, a run adds step headers, the window is
// made narrow (every header's tags move under its title), the run adds
// headers while narrow (they must be created under the title), the window
// is made wide again (all of them beside), headers are added once more, and
// the window is narrow once more. Each state is checked with checkTags, and
// the hero's plan, the cells and the hero's buttons with it.

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

func init() { covScenarios["narrow-toggle"] = narrowToggle }

const (
	toggleWide   = 960
	toggleNarrow = 500
)

func narrowToggle(c *covEnv) {
	w := c.w
	c.quiet()
	now := time.Now()
	pfx := "t" // the step ids of the run going on
	plan := func(n int) []engine.PlanStep {
		var p []engine.PlanStep
		for i := range n {
			p = append(p, engine.PlanStep{ID: fmt.Sprintf("%s%d", pfx, i+1), Title: fmt.Sprintf("Step %d of the toggled run, with a title long enough to wrap in a narrow window", i+1),
				NeedsPriv: i%2 == 0, Category: logstore.Cleanup})
		}
		return p
	}
	// resize sets the window's width and waits for the breakpoint to follow.
	resize := func(wd int) func() {
		return func() { w.win.SetDefaultSize(wd, narrowH) }
	}
	// at checks that the window is wd wide, w.narrow follows, and every
	// drawn header and plan row has its tags where the width says.
	at := func(wd int, moment string) func() {
		return func() {
			c.ok(w.win.Width() == wd, "%s: the window is %d px wide, want %d (the headless compositor didn't resize it)", moment, w.win.Width(), wd)
			c.ok(w.narrow == (wd <= narrowPx), "%s: Window.narrow = %v at %d px", moment, w.narrow, wd)
			c.ok(checkTags(c, moment) > 0, "%s: no step header with tags drawn", moment)
			n := len(w.tl.headers)
			c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("%s width=%d content=0 headers=%d", moment, w.win.Width(), n))
		}
	}
	// grow adds headers to the running run: from step from+1 to step to.
	grow := func(from, to int) func() {
		return func() {
			var evs []engine.Event
			for i := from; i < to; i++ {
				id := pfx + strconv.Itoa(i+1)
				evs = append(evs, engine.StepStarted{ID: id, Index: i, At: now},
					engine.OutputChunk{ID: id, Lines: []string{"line one of " + id, "line two of " + id, "line three of " + id}})
				if i%3 == 2 {
					evs = append(evs, engine.StepDone{ID: id, Index: i, Status: steps.Failed, Elapsed: time.Second, Err: "failed with a state line that is long enough to wrap in a narrow window"})
				}
			}
			c.feed(evs...)
		}
	}
	start := func(p string, n int) func() {
		return func() {
			pfx = p
			w.current = actions.IDFullCleanup
			w.pickPage("run")
			c.feed(
				engine.PhaseChanged{Phase: engine.PhaseRunning},
				engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: plan(n)},
			)
		}
	}
	finish := func() {
		c.feed(
			engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted, Elapsed: time.Second},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
		)
	}
	// snap remembers the headers that exist; more checks that a new run
	// made new ones (else "added while narrow" tested nothing).
	known := map[*stepHeader]bool{}
	snap := func() {
		for _, h := range w.tl.headers {
			known[h] = true
		}
	}
	more := func(moment string) func() {
		return func() {
			fresh := 0
			for _, h := range w.tl.headers {
				if !known[h] {
					fresh++
				}
			}
			c.ok(fresh > 0, "%s: the new run made no new step header (%d known)", moment, len(known))
		}
	}
	// Each state gets its frames before it is measured.
	var seq []func()
	add := func(f ...func()) { seq = append(seq, f...) }
	// A: started wide, then narrow. B: started (new headers) while narrow,
	// then wide. C: started while wide, then narrow again.
	add(start("a", 6), grow(0, 6), at(toggleWide, "wide-first"))
	add(resize(toggleNarrow), at(toggleNarrow, "narrow-first"))
	add(finish, snap, start("b", 12), grow(0, 12), more("narrow-new-run"), at(toggleNarrow, "narrow-new-headers"))
	add(resize(toggleWide), at(toggleWide, "wide-again"))
	add(finish, snap, start("c", 18), grow(0, 18), more("wide-new-run"), at(toggleWide, "wide-new-headers"))
	add(resize(toggleNarrow), at(toggleNarrow, "narrow-again"))
	add(finish, at(toggleNarrow, "narrow-finished"))
	// The Overview over the same toggles: the hero's kicker and buttons and
	// the cells' roots follow the width (checkTags, with the plan's rows when
	// the Driver has a plan).
	overview := func(wd int, moment string) func() {
		return func() {
			if w.heroKicker != nil {
				want := w.kickerText[0]
				if wd <= narrowPx {
					want = w.kickerText[1]
				}
				c.ok(w.heroKicker.Text() == want, "%s: the hero's kicker is %q, want %q", moment, w.heroKicker.Text(), want)
			}
			checkTags(c, moment)
			c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("%s width=%d content=0 headers=0", moment, w.win.Width()))
		}
	}
	add(func() { w.pickPage("overview") }, overview(toggleNarrow, "overview-narrow"))
	add(resize(toggleWide), overview(toggleWide, "overview-wide"))
	add(resize(toggleNarrow), overview(toggleNarrow, "overview-narrow-again"))
	var run func(i int)
	run = func(i int) {
		if i == len(seq) {
			c.finish()
			return
		}
		coreglib.TimeoutAdd(400, func() {
			seq[i]()
			run(i + 1)
		})
	}
	run(0)
}

// TestGUIMinWidthToggle: the breakpoint crossed in both directions with a
// run adding rows: no row's tags are on the wrong side of its title.
func TestGUIMinWidthToggle(t *testing.T) {
	needDisplay(t)
	_, env := installShims(t, shimOpts{})
	rep, _ := childCmd(t, "narrow-toggle", nil, env...)
	seen := map[string]bool{}
	for _, m := range rep.Narrow {
		var name string
		var width, content, n int
		if _, err := fmt.Sscanf(m, "%s width=%d content=%d headers=%d", &name, &width, &content, &n); err != nil {
			t.Errorf("measure %q: %v", m, err)
			continue
		}
		seen[name] = true
		t.Log(m)
	}
	for _, want := range []string{"wide-first", "narrow-first", "narrow-new-headers", "wide-again", "wide-new-headers", "narrow-again", "narrow-finished", "overview-narrow", "overview-wide", "overview-narrow-again"} {
		if !seen[want] {
			t.Errorf("never measured %s (%q)", want, rep.Narrow)
		}
	}
}
