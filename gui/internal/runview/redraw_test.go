package runview

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
)

func out(id string, replace, dropped int, lines ...string) engine.OutputChunk {
	return engine.OutputChunk{ID: id, Index: 0, Lines: lines, Replace: replace, Dropped: dropped}
}

// TestApplyRedraw (#129): a chunk that redraws the step's last lines
// replaces them in the model, and the Change says so exactly: the lines
// leave the view's tail (OutTrim, StepOut.Trim) and the new ones follow,
// in one StepOut for the step. Fails on ecb0903: every frame was a new
// line.
func TestApplyRedraw(t *testing.T) {
	m, v := NewModel(), &view{}
	g := groups{}
	step := func(evs ...engine.Event) Change {
		t.Helper()
		c := m.Apply(evs)
		v.apply(m, c)
		g.apply(t, c.Steps)
		check(t, m, v)
		if want := m.want(); !reflect.DeepEqual(g, want) {
			t.Fatalf("grouped view %q, model %q", g, want)
		}
		return c
	}
	step(plan("s"), engine.StepStarted{ID: "s", Index: 0})
	step(out("s", 0, 0, "a", "b", "pulling ⠋"))
	c := step(out("s", 1, 0, "pulling ⠙"))
	if c.OutTrim != 1 || c.OutRemove != 0 || !slices.Equal(c.OutAppend, []string{"pulling ⠙"}) {
		t.Fatalf("redraw of 1: %+v", c)
	}
	if want := []StepOut{{ID: "s", Trim: 1, Append: []string{"pulling ⠙"}}}; !reflect.DeepEqual(c.Steps, want) {
		t.Fatalf("grouped %+v, want %+v", c.Steps, want)
	}
	step(out("s", 2, 0, "\x1b[1mB\x1b[0m", "pulling done", "c"))
	if got := m.StepText("s"); !slices.Equal(got, []string{"a", "B", "pulling done", "c"}) {
		t.Fatalf("step text %q", got)
	}
	if l, _ := m.StepLine("s", 1); l.Text != "B" || len(l.Spans) != 1 || !l.Spans[0].Style.Bold {
		t.Fatalf("redrawn line keeps its colour: %+v", l)
	}
	if m.Rows[0].Last != "c" {
		t.Fatalf("row's last line %q", m.Rows[0].Last)
	}
	// A redraw never takes back a dropped-lines note or the step's header:
	// what it can't reach is gone already, its lines all go in.
	step(out("s", 0, 2, "x"))
	step(out("s", 3, 0, "y", "z"))
	note := "… 2 lines not shown (they are in the log)"
	if got := m.StepText("s"); !slices.Equal(got, []string{"a", "B", "pulling done", "c", note, "y", "z"}) {
		t.Fatalf("step text after a gap %q", got)
	}
	step(engine.StepStarted{ID: "t", Index: 1})
	step(out("t", 5, 0, "t1"))
	if got := m.StepText("t"); !slices.Equal(got, []string{"t1"}) || len(m.StepText("s")) != 7 {
		t.Fatalf("a redraw reached another step: s %q, t %q", m.StepText("s"), got)
	}
	if m.Lines != 7+2 { // seven lines shown and two dropped
		t.Fatalf("Lines %d", m.Lines)
	}
}

// TestRedrawAcrossTheCap: a redraw right after a batch that overflowed
// the ring takes back only lines the ring still holds, and the views stay
// exact; lines the ring overwrote never come back.
func TestRedrawAcrossTheCap(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	for round := range 30 {
		m, v := NewModel(), &view{}
		g := groups{}
		apply := func(evs []engine.Event) {
			c := m.Apply(evs)
			v.apply(m, c)
			g.apply(t, c.Steps)
			check(t, m, v)
			if want := m.want(); !reflect.DeepEqual(g, want) {
				t.Fatalf("round %d: grouped view drifted from the model", round)
			}
		}
		apply([]engine.Event{plan("a", "b")})
		n := 0
		var evs []engine.Event
		for i, id := range []string{"a", "b"} {
			evs = append(evs, engine.StepStarted{ID: id, Index: i})
			own := 0
			for range 40 {
				size := r.IntN(8)
				if r.IntN(6) == 0 {
					size = r.IntN(OutputCap + OutputCap/2)
				}
				lines := make([]string, size)
				for k := range lines {
					n++
					lines[k] = fmt.Sprintf("%s %d", id, n)
				}
				replace := 0
				if own > 0 && r.IntN(2) == 0 {
					replace = 1 + r.IntN(min(own, 70))
				}
				if len(lines) < replace {
					continue
				}
				dropped := 0
				if r.IntN(10) == 0 {
					dropped = 1 + r.IntN(5)
				}
				evs = append(evs, out(id, replace, dropped, lines...))
				own += len(lines) - replace
				if r.IntN(3) == 0 {
					apply(evs)
					evs = nil
				}
			}
		}
		apply(evs)
		if got := m.Output(); len(got) > OutputCap {
			t.Fatalf("round %d: %d lines in view", round, len(got))
		}
	}
}

// TestPumpMergesRedraws: a redraw queued behind a chunk of the same step
// merges into it (engine.Rewrite); a flush that splits a chunk keeps the
// redraw on its first piece; a drop keeps it too.
func TestPumpMergesRedraws(t *testing.T) {
	p := &Pump{opts: PumpOptions{MaxPending: -1, FlushLines: 3}}
	p.add(out("s", 0, 0, "a", "b"))
	p.add(out("s", 1, 0, "B", "c"))
	p.add(out("s", 4, 0, "w", "x", "y", "z", "q"))
	if len(p.queue) != 1 || p.lines != 5 {
		t.Fatalf("queue %+v (%d lines)", p.queue, p.lines)
	}
	b1, n1 := p.take()
	b2, n2 := p.take()
	want1 := []engine.Event{out("s", 1, 0, "w", "x", "y")}
	want2 := []engine.Event{out("s", 0, 0, "z", "q")}
	if !reflect.DeepEqual(b1, want1) || !reflect.DeepEqual(b2, want2) || n1 != 3 || n2 != 2 || p.lines != 0 {
		t.Fatalf("took %+v (%d), %+v (%d)", b1, n1, b2, n2)
	}

	p = &Pump{opts: PumpOptions{MaxPending: 2, FlushLines: 100}}
	p.add(out("s", 0, 3, "a"))
	p.add(out("s", 2, 0, "b", "c", "d")) // reaches past a chunk that dropped lines: queued apart
	if len(p.queue) != 2 {
		t.Fatalf("a redraw past a drop merged: %+v", p.queue)
	}
	got, _ := p.take()
	want := []engine.Event{out("s", 0, 4, []string{}...), out("s", 2, 1, "c", "d")}
	if !reflect.DeepEqual(got, want) || p.stats.Dropped != 2 {
		t.Fatalf("after trimming to 2: %+v, want %+v", got, want)
	}
}

// TestRedrawLinesStayCapped: a step that redraws one line forever holds
// one line in the view.
func TestRedrawLinesStayCapped(t *testing.T) {
	m := NewModel()
	m.Apply([]engine.Event{plan("s"), engine.StepStarted{ID: "s", Index: 0}, out("s", 0, 0, "0%")})
	for i := range 3 * OutputCap {
		m.Apply([]engine.Event{out("s", 1, 0, fmt.Sprintf("%d%%", i%101))})
	}
	if got := m.StepText("s"); len(got) != 1 || !strings.HasSuffix(got[0], "%") {
		t.Fatalf("step text %q", got)
	}
}
