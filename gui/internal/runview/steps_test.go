package runview

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// groups is a grouped view as a widget would hold it: each step's lines,
// built only from Change.Steps.
type groups map[string][]string

func (g groups) apply(t *testing.T, outs []StepOut) {
	t.Helper()
	for _, o := range outs {
		if o.Remove > len(g[o.ID]) {
			t.Fatalf("remove %d from %q, which holds %d", o.Remove, o.ID, len(g[o.ID]))
		}
		g[o.ID] = append(g[o.ID][o.Remove:], o.Append...)
		if len(g[o.ID]) == 0 {
			delete(g, o.ID)
		}
	}
}

// want is the grouped view the model holds: the view's lines without the
// step headers, by step.
func (m *Model) want() groups {
	g := groups{}
	for seq := m.viewLo; seq < m.viewHi; seq++ {
		e := m.out.buf[seq%len(m.out.buf)]
		if !e.head {
			g[e.step] = append(g[e.step], e.text)
		}
	}
	return g
}

func plan(ids ...string) engine.QueueStarted {
	q := engine.QueueStarted{Action: "a", Started: t0}
	for _, id := range ids {
		q.Steps = append(q.Steps, engine.PlanStep{ID: id, Title: "Step " + id, LogLabel: "Label " + id})
	}
	return q
}

// TestStepGroups: the grouped diff always rebuilds exactly the view's
// lines by step, also when a batch is larger than OutputCap, when the cap
// trims across steps, and with a step inserted mid-run; the flat diff is
// unchanged by it.
func TestStepGroups(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for round := range 20 {
		m := NewModel()
		g := groups{}
		flat := []string{}
		ids := []string{"a", "b", "c", "d"}
		c := m.Apply([]engine.Event{plan(ids...)})
		g.apply(t, c.Steps)
		n := 0
		for i := 0; i < len(ids); i++ {
			evs := []engine.Event{engine.StepStarted{ID: ids[i], Index: i}}
			if i == 1 && round%2 == 0 {
				ids = slices.Insert(ids, 2, "ins")
				evs = append(evs, engine.StepInserted{Index: 2, Step: engine.PlanStep{ID: "ins", Title: "Inserted"}})
			}
			for range r.IntN(5) + 1 {
				lines := make([]string, r.IntN(2*OutputCap/3))
				for k := range lines {
					n++
					lines[k] = fmt.Sprintf("\x1b[1m%s %d\x1b[0m", ids[i], n)
				}
				evs = append(evs, engine.OutputChunk{ID: ids[i], Index: i, Lines: lines, Dropped: r.IntN(2)})
				if r.IntN(3) == 0 {
					c = m.Apply(evs)
					g.apply(t, c.Steps)
					flat = append(flat[c.OutRemove:], c.OutAppend...)
					evs = nil
				}
			}
			evs = append(evs, engine.StepDone{ID: ids[i], Index: i, Status: steps.OK})
			c = m.Apply(evs)
			g.apply(t, c.Steps)
			flat = append(flat[c.OutRemove:], c.OutAppend...)
			want := m.want()
			if !reflect.DeepEqual(g, want) {
				t.Fatalf("round %d after step %s: grouped view drifted from the model", round, ids[i])
			}
			// StepText and StepLine read the same groups back (the
			// timeline's expand and colour lookups).
			for id, lines := range want {
				if got := m.StepText(id); !slices.Equal(got, lines) {
					t.Fatalf("round %d: StepText(%q) has %d lines, want %d", round, id, len(got), len(lines))
				}
				for _, k := range []int{0, len(lines) / 2, len(lines) - 1} {
					if l, ok := m.StepLine(id, k); !ok || l.Text != lines[k] {
						t.Fatalf("round %d: StepLine(%q, %d) = %q, %v; want %q", round, id, k, l.Text, ok, lines[k])
					}
				}
				if _, ok := m.StepLine(id, len(lines)); ok {
					t.Fatalf("round %d: StepLine past the end of %q", round, id)
				}
			}
			if !slices.Equal(flat, m.Output()) {
				t.Fatalf("round %d after step %s: the flat view drifted", round, ids[i])
			}
		}
	}
}

// TestStepGroupsHeaders: a step's "── title" line is in the flat view but
// not in its group; a drop notice is in its step's group.
func TestStepGroupsHeaders(t *testing.T) {
	m := NewModel()
	c := m.Apply([]engine.Event{plan("s"), engine.StepStarted{ID: "s"},
		engine.OutputChunk{ID: "s", Lines: []string{"one", "two"}, Dropped: 3}})
	if !slices.Equal(c.OutAppend, []string{"── Step s", "… 3 lines not shown (they are in the log)", "one", "two"}) {
		t.Fatalf("flat %q", c.OutAppend)
	}
	if len(c.Steps) != 1 || c.Steps[0].ID != "s" || !slices.Equal(c.Steps[0].Append, c.OutAppend[1:]) {
		t.Fatalf("grouped %+v", c.Steps)
	}
}

// TestNewRunClearsOutput: Q12. A new queue starts with an empty page, and
// so does Clear (the window's, at each start); the log keeps the lines.
func TestNewRunClearsOutput(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("s"), engine.StepStarted{ID: "s"},
		engine.OutputChunk{ID: "s", Lines: []string{"old 1", "old 2"}}, engine.StepDone{ID: "s", Status: steps.OK},
		engine.Finished{Action: "a"}})
	g := groups{"s": {"old 1", "old 2"}}
	c := m.Apply([]engine.Event{plan("t"), engine.StepStarted{ID: "t"}, engine.OutputChunk{ID: "t", Lines: []string{"new"}}})
	g.apply(t, c.Steps)
	if c.OutRemove != 3 || !slices.Equal(c.OutAppend, []string{"── Step t", "new"}) || !slices.Equal(m.Output(), c.OutAppend) {
		t.Errorf("next run: remove %d append %q output %q", c.OutRemove, c.OutAppend, m.Output())
	}
	if !reflect.DeepEqual(g, groups{"t": {"new"}}) {
		t.Errorf("grouped after the next run: %v", g)
	}

	c = m.Clear()
	g.apply(t, c.Steps)
	if !c.Reset || len(m.Rows) != 0 || c.OutRemove != 2 || len(c.OutAppend) != 0 || len(m.Output()) != 0 || len(g) != 0 {
		t.Errorf("Clear: %+v, rows %d, output %q, groups %v", c, len(m.Rows), m.Output(), g)
	}
	if _, ok := m.ViewLine(0); ok {
		t.Error("a cleared view still has a line 0")
	}
	if c := m.Clear(); c.OutRemove != 0 || len(c.Steps) != 0 {
		t.Errorf("a second Clear changed the output: %+v", c)
	}
}

func TestSummary(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("ok", "quiet", "bad", "skip", "refused", "pending"),
		engine.StepStarted{ID: "ok", Index: 0},
		engine.OutputChunk{ID: "ok", Index: 0, Lines: []string{"first", "\x1b[32mlast\x1b[0m", "   ", ""}},
		engine.StepDone{ID: "ok", Index: 0, Status: steps.OK, Elapsed: time.Second},
		engine.StepStarted{ID: "quiet", Index: 1},
		engine.StepDone{ID: "quiet", Index: 1, Status: steps.OK},
		engine.StepStarted{ID: "bad", Index: 2},
		engine.OutputChunk{ID: "bad", Index: 2, Lines: []string{"error: conflicting files"}},
		engine.StepDone{ID: "bad", Index: 2, Status: steps.Failed, Err: "exit status 1"},
		engine.StepDone{ID: "skip", Index: 3, Status: steps.Skipped},
		engine.StepDone{ID: "refused", Index: 4, Status: steps.AuthRefused},
	})
	want := []string{"last", "Label quiet", "exit status 1", "Skipped", "Not authorised", ""}
	for i, w := range want {
		if got := m.Rows[i].Summary(); got != w {
			t.Errorf("%s: summary %q, want %q", m.Rows[i].ID, got, w)
		}
	}
	m.Rows[2].Err = ""
	if got := m.Rows[2].Summary(); got != "error: conflicting files" {
		t.Errorf("failed without Err: %q", got)
	}
}

func TestHeader(t *testing.T) {
	m := NewModel()
	if h := m.Header(); h != (Header{}) || m.Elapsed(t0) != 0 {
		t.Errorf("idle: %+v", h)
	}
	m.Apply([]engine.Event{plan("a", "b", "c", "d", "e", "f", "g", "h", "i"),
		engine.StepStarted{ID: "a", Index: 0}, engine.StepDone{ID: "a", Index: 0, Status: steps.OK},
		engine.StepStarted{ID: "b", Index: 1}})
	h := m.Header()
	if h.Kicker != "Running · step 2 of 9" || h.Badge != "02/09" || h.Done != 1 || h.Step != 2 || h.Of != 9 {
		t.Errorf("running: %+v", h)
	}
	if got := m.Elapsed(t0.Add(32 * time.Second)); got != 32*time.Second {
		t.Errorf("elapsed %v", got)
	}
	m.Apply([]engine.Event{engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass}})
	if h := m.Header(); h.Kicker != "Waiting for your password" {
		t.Errorf("password: %q", h.Kicker)
	}
	m.Apply([]engine.Event{engine.PromptClosed{ID: 7}, engine.AuthWaiting{Kind: engine.WaitPolkit, Step: "b"}})
	if h := m.Header(); h.Kicker != "Waiting for authorisation" {
		t.Errorf("polkit: %q", h.Kicker)
	}
	m.Apply([]engine.Event{engine.AuthResolved{Granted: true},
		engine.StepDone{ID: "b", Index: 1, Status: steps.Failed},
		engine.StepDone{ID: "c", Index: 2, Status: steps.Skipped},
		engine.StepDone{ID: "d", Index: 3, Status: steps.AuthRefused},
		engine.Finished{Action: "a", Outcome: engine.OutcomeCompleted, Errs: 1, Elapsed: 3 * time.Minute}})
	h = m.Header()
	if h.Kicker != "Finished: failed (1 step)" || h.Badge != "done" || h.Failed != 1 || h.Skipped != 1 || h.Refused != 1 || h.NotRun != 5 {
		t.Errorf("finished: %+v", h)
	}
	if got := m.Elapsed(t0.Add(time.Hour)); got != 3*time.Minute {
		t.Errorf("finished elapsed %v", got)
	}

	d := NewModel()
	d.Apply([]engine.Event{engine.QueueStarted{Action: "a", DryRun: true, Started: t0, Steps: []engine.PlanStep{{ID: "x"}}},
		engine.StepStarted{ID: "x"}})
	if h := d.Header(); h.Kicker != "Dry run · step 1 of 1" {
		t.Errorf("dry run: %q", h.Kicker)
	}
	if d.Stop(); d.Header().Kicker != EngineStopped {
		t.Errorf("stopped: %q", d.Header().Kicker)
	}
}

// BenchmarkApplyFlushSections: a full flush (2048 lines) that ends one
// step and starts the next, so both groups change.
func BenchmarkApplyFlushSections(b *testing.B) {
	lines := make([]string, 1024)
	for i := range lines {
		lines[i] = FloodLine(i+1, 1024)
	}
	m := NewModel()
	m.Apply([]engine.Event{plan("a", "b")})
	b.ReportAllocs()
	for b.Loop() {
		m.Apply([]engine.Event{engine.OutputChunk{ID: "a", Index: 0, Lines: lines},
			engine.StepStarted{ID: "b", Index: 1}, engine.OutputChunk{ID: "b", Index: 1, Lines: lines}})
	}
}
