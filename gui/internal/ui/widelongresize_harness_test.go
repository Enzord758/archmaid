package ui

// A wide dialog left open while its window is resized across the boundary
// where its lists (capList) fit or don't (batch 7, Dev 2's #102): tall ->
// low -> tall -> low -> tall. In a tall window every list is capped again
// (its max content height back, scrolling by itself past it, the middle
// not scrolling); in a low one every list takes its whole height and only
// the middle scrolls. Never an uncapped list in a tall window, never a
// capped list scrolling inside a scrolling middle in a low one.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/engine"
)

func init() { covScenarios["wide-resize"] = wideResize }

// TestGUIWideDialogsLongResize: see the file's comment.
func TestGUIWideDialogsLongResize(t *testing.T) {
	needDisplay(t)
	var buf bytes.Buffer
	rep, _ := childCmd(t, "wide-resize", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) })
	for _, l := range strings.Split(buf.String(), "\n") {
		if glibWarned(l) {
			t.Errorf("GTK logged: %s", l)
		}
	}
	if rep != nil {
		for _, m := range rep.Narrow {
			t.Log(m)
		}
	}
}

// vpolicy is sc's vertical scrollbar policy.
func vpolicy(sc *gtk.ScrolledWindow) gtk.PolicyType {
	_, v := sc.Policy()
	return v
}

func wideResize(c *covEnv) {
	w := c.w
	c.quiet()
	tall := [2]int{1460, 930}
	low := [2]int{1460, 500}
	stages := [][2]int{tall, low, tall, low, tall, low, tall}
	var all []struct {
		name string
		ev   engine.Event
		id   engine.PromptID
	}
	for _, p := range longPrompts() {
		switch ev := p.ev.(type) {
		case engine.NeedsConfirm:
			if p.name == "orphans" || p.name == "downloads" {
				all = append(all, struct {
					name string
					ev   engine.Event
					id   engine.PromptID
				}{p.name, ev, ev.ID})
			}
		case engine.OutputReport:
			all = append(all, struct {
				name string
				ev   engine.Event
				id   engine.PromptID
			}{p.name, ev, ev.ID})
		}
	}
	check := func(name string, d *amDialog, size [2]int, stage int) {
		isTall := size == tall
		where := fmt.Sprintf("%s, stage %d (%dx%d)", name, stage, size[0], size[1])
		if len(d.lists) == 0 {
			c.fail("%s: no capped lists", where)
			return
		}
		c.ok(d.win.Width() == size[0] && d.win.Height() == size[1], "%s: the window is %dx%d (the headless output too small?)", where, d.win.Width(), d.win.Height())
		c.ok(d.uncapped == !isTall, "%s: uncapped %v, want %v", where, d.uncapped, !isTall)
		scrolling := 0
		for i, l := range d.lists {
			capped := l.sc.MaxContentHeight() == l.cap && vpolicy(l.sc) == gtk.PolicyAutomatic
			free := l.sc.MaxContentHeight() == -1 && vpolicy(l.sc) == gtk.PolicyNever
			if isTall {
				c.ok(capped, "%s: list %d is not capped again (max %d, policy %v, cap %d)", where, i, l.sc.MaxContentHeight(), vpolicy(l.sc), l.cap)
				if overflows(l.sc) {
					scrolling++
				}
			} else {
				c.ok(free, "%s: list %d is still capped in a low window (max %d, policy %v)", where, i, l.sc.MaxContentHeight(), vpolicy(l.sc))
				c.ok(!overflows(l.sc), "%s: list %d scrolls inside the scrolling middle", where, i)
			}
		}
		if mid, ok := d.parts[1].(*gtk.ScrolledWindow); ok {
			if isTall {
				// Everything fits: the middle has nothing to scroll, and
				// the long lists scroll by themselves.
				c.ok(!overflows(mid), "%s: the middle scrolls in a tall window", where)
				if name != "report" {
					c.ok(scrolling > 0, "%s: no list scrolls by itself in a tall window", where)
				}
			} else {
				c.ok(overflows(mid), "%s: the middle doesn't scroll in a low window", where)
			}
		}
		c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("%s: uncapped %v, %d lists, %d scrolling by themselves", where, d.uncapped, len(d.lists), scrolling))
	}
	n := 0
	var run func()
	run = func() {
		if n == len(all) {
			c.finish()
			return
		}
		p := all[n]
		c.feed(p.ev)
		stage := 0
		var next func()
		next = func() {
			d := w.dialogs[p.id]
			if d == nil {
				c.fail("%s: no dialog at stage %d", p.name, stage)
				n++
				coreglib.TimeoutAdd(100, run)
				return
			}
			check(p.name, d, stages[stage], stage)
			stage++
			if stage == len(stages) {
				ans := "no"
				if p.name == "report" {
					ans = "close"
				}
				c.press(d, ans)
				n++
				coreglib.TimeoutAdd(200, run)
				return
			}
			w.win.SetDefaultSize(stages[stage][0], stages[stage][1])
			coreglib.TimeoutAdd(700, next)
		}
		w.win.SetDefaultSize(tall[0], tall[1])
		coreglib.TimeoutAdd(800, next)
	}
	w.win.SetDefaultSize(tall[0], tall[1])
	coreglib.TimeoutAdd(400, run)
}
