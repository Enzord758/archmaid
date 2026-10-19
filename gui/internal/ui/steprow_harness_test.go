package ui

// Task #207, finding 2: a step's title row on the finished Run page is the
// highlighted, focusable, activatable target; the line under it (the folded
// step's summary) never takes the highlight or the focus.

import (
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

func init() { covScenarios["cov-steprow"] = covStepRow }

func TestGUICovStepRow(t *testing.T) { covChild(t, "cov-steprow") }

// covStepRow: the finished Run page with folded steps that each have a line
// under their title. The title row is focusable and carries the highlight
// class; the line's list row is neither focusable nor activatable; Tab from
// a title row reaches the next title row (not a line); Enter on the focused
// row unfolds that step.
func covStepRow(c *covEnv) {
	w := c.w
	c.quiet()
	w.current = actions.IDFullCleanup
	now := time.Now()
	var plan []engine.PlanStep
	for _, id := range []string{"a", "b", "c"} {
		plan = append(plan, engine.PlanStep{ID: id, Title: "Step " + id})
	}
	evs := []engine.Event{engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: plan}}
	for i, id := range []string{"a", "b", "c"} {
		evs = append(evs, engine.StepStarted{ID: id, Index: i, At: now},
			engine.OutputChunk{ID: id, Lines: []string{"first of " + id, "[warn] valent 0 votes " + id}},
			engine.StepDone{ID: id, Index: i, Status: steps.OK, Elapsed: time.Millisecond})
	}
	evs = append(evs, engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted})
	w.pickPage("run")
	c.feed(evs...)
	sec := func(id string) *section { return w.tl.byStep[id] }
	rowOf := func(s *section) *gtk.Widget { return gtk.BaseWidget(gtk.BaseWidget(s.header.box).FirstChild()) }
	press := func(r *gtk.Widget, key uint) bool {
		for _, o := range controllersOf(r) {
			if k, ok := o.Cast().(*gtk.EventControllerKey); ok {
				k.Emit("key-pressed", key, uint(0), gdk.ModifierType(0))
				return true
			}
		}
		c.fail("the title row has no key controller")
		return false
	}
	c.seq(400,
		func() {
			for _, id := range []string{"a", "b"} {
				s := sec(id)
				c.ok(s != nil && s.header != nil, "step %s has no header drawn", id)
				c.ok(s != nil && !s.lines, "step %s is not folded to its summary", id)
				if s == nil || s.header == nil {
					return
				}
				r := rowOf(s)
				c.ok(r.Focusable() && r.HasCSSClass("am-step-row"), "step %s: title row focusable %v class %v", id, r.Focusable(), r.HasCSSClass("am-step-row"))
			}
			// The summary lines: their list rows take no focus and are
			// not activatable, so neither hover nor Enter lands on them.
			n := 0
			eachWidget(&w.outView.Widget, func(x gtk.Widgetter) {
				l, ok := x.(*gtk.Label)
				if !ok || !l.HasCSSClass("am-summary") {
					return
				}
				n++
				p := gtk.BaseWidget(gtk.BaseWidget(l).Parent())
				c.ok(!p.Focusable(), "a summary line's row is focusable")
				c.ok(!p.HasCSSClass("activatable"), "a summary line's row is activatable")
			})
			c.ok(n >= 2, "only %d summary lines drawn", n)
			// Keyboard: Tab into the list lands on a title row (the first),
			// Down moves to the next step's title row, Up back.
			a := sec("a")
			w.tl.view.GrabFocus()
			c.ok(a.header != nil, "step a lost its header")
		},
		func() {
			a := sec("a")
			f := w.win.Focus()
			c.ok(f != nil && sameObject(f, rowOf(a)), "Tab into the list landed on %T, not step a's title row", f)
			w.win.SetFocusVisible(true) // as after a key press
			press(rowOf(a), gdk.KEY_Down)
		},
		func() {
			b := sec("b")
			f := w.win.Focus()
			c.ok(f != nil && sameObject(f, rowOf(b)), "Down from step a's row landed on %T, not step b's", f)
			shoot(w, "cov-steprow", "focus-on-b")
			press(rowOf(b), gdk.KEY_Up)
		},
		func() {
			// Enter on the focused title row unfolds that step (a, where Up
			// from b left the focus), not another.
			f := w.win.Focus()
			c.ok(f != nil && sameObject(f, rowOf(sec("a"))), "Up from step b's row landed on %T, not step a's", f)
			if f != nil {
				press(gtk.BaseWidget(f), gdk.KEY_Return)
			}
			c.ok(sec("a").lines && sec("a").userOpen, "Enter on step a's title row didn't unfold it (lines %v)", sec("a").lines)
			c.ok(!sec("b").lines, "Enter unfolded step b too")
		},
	)
}
