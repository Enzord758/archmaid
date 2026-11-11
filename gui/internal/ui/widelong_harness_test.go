package ui

// The wide dialogs with long content (task #96, QA of task #95): a
// 20-step gate, 300 orphans with their cascade, 200 long file names, a
// step confirm naming a long path, 80 services to pick from, a 40-finding
// audit report and an askpass named by a long path, each at 1460x930 (the
// owner's window) and at 1460x500 (a short one). In each: the dialog fits
// the window's height, its heading and its buttons are drawn inside it and
// sit in no scrolled window (a wide dialog's; an AdwAlertDialog's heading
// scrolls with its message by libadwaita's design), and at 1460x500 the
// middle scrolls. Nothing may be wider than the window, and GTK may log
// nothing. With ARCHMAID_GUI_SHOTS: wide-long-<width>x<height>-<dialog>.png.

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

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
)

func init() { covScenarios["wide-long"] = wideLong }

// wideLongSizes are the window sizes wide-long measures at: the owner's,
// then a short one (the output is 1600x1000: the wide group's).
var wideLongSizes = [][2]int{{1460, 930}, {1460, 500}, {620, 500}, {380, 500}}

// TestGUIWideDialogsLong: every dialog with long content fits the window's
// height with its heading and buttons in view; in a short window its
// middle scrolls.
func TestGUIWideDialogsLong(t *testing.T) {
	needDisplay(t)
	var buf bytes.Buffer
	rep, _ := childCmd(t, "wide-long", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) })
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

// longPrompts are wide-long's dialogs, each as the engine would send it,
// the button checked to be in view, and the one that answers it.
func longPrompts() []struct {
	name     string
	ev       engine.Event
	yes, ans string
} {
	var plan []engine.PlanStep
	for i := range 20 {
		s := engine.PlanStep{ID: fmt.Sprintf("s%02d", i), Title: fmt.Sprintf("Step %d: clean what step %d cleans", i+1, i+1),
			NeedsPriv: i%3 == 0, Confirm: i%4 == 1, Optional: i%5 == 2, Argv: []string{"true"}}
		if s.NeedsPriv && i > 0 {
			s.Verb = "full-cleanup" // root through the helper; the first gets it through its own sudo
		}
		if i == 7 {
			s.Title = "Remove " + strings.Repeat("very-long-package-name-", 6) + "git and what depends on it"
		}
		plan = append(plan, s)
	}
	var direct, cascade []string
	for i := range 300 {
		n := fmt.Sprintf("lib32-orphaned-package-%03d", i)
		direct = append(direct, n)
		cascade = append(cascade, n+" 1.2.3-1")
	}
	for i := range 200 {
		cascade = append(cascade, fmt.Sprintf("python-dependency-of-orphan-%03d 0.%d.0-2", i, i))
	}
	var files []string
	for i := range 200 {
		files = append(files, fmt.Sprintf("'%03d %s.tar.zst'", i, strings.Repeat("a rather long download name with spaces ", 4)))
	}
	var units []string
	for i := range 80 {
		units = append(units, fmt.Sprintf("systemd-something-quite-long-instance@%s%02d.service", strings.Repeat("x", 30), i))
	}
	var findings []engine.AuditFinding
	for i := range 40 {
		findings = append(findings, engine.AuditFinding{Name: fmt.Sprintf("aur-package-%02d-git", i), High: i%7 == 0,
			Flags: []string{"new maintainer", "out of date"}, Reason: "maintainer someone → " + strings.Repeat("someone-else", 3)})
	}
	longPath := "~/" + strings.Repeat("a-deep-folder/", 12) + "and-a-file-name-without-any-breaks-" + strings.Repeat("z", 60)
	type p = struct {
		name     string
		ev       engine.Event
		yes, ans string
	}
	return []p{
		{"gate", engine.NeedsConfirm{ID: 601, Kind: engine.ConfirmGate, Title: "Proceed with full cleanup?", Msg: "Proceed with full cleanup?",
			Plan: plan, Default: true, YesLabel: "Proceed"}, "Proceed", "no"},
		{"orphans", engine.NeedsConfirm{ID: 602, Kind: engine.ConfirmOrphans, Step: "orphans-remove", Title: "Remove 300 orphaned packages?",
			Msg: "Remove 300 orphaned packages?", Direct: direct, Cascade: cascade, Destructive: true, YesLabel: "Remove"}, "Remove", "no"},
		{"downloads", engine.NeedsConfirm{ID: 603, Kind: engine.ConfirmDownloads, Step: "downloads", Title: "Delete 200 old files in ~/Downloads?",
			Items: files, Destructive: true, YesLabel: "Delete"}, "Delete", "no"},
		{"step", engine.NeedsConfirm{ID: 604, Kind: engine.ConfirmStep, Step: "x", Title: "Clear everything under " + longPath + "?",
			Destructive: true, YesLabel: "Clear everything under that folder"}, "Clear everything under that folder", "no"},
		{"selection", engine.NeedsSelection{ID: 605, Title: "Enabled but not running — pick the services to disable", Items: units}, "Continue", "cancel"},
		{"report", engine.OutputReport{ID: 606, Text: "x", Audit: &engine.AuditReport{Total: 140, CanAccept: true, Findings: findings}}, "Accept", "close"},
		{"askpass", engine.NeedsPassword{ID: 607, Kind: engine.PasswordAskpass, Requester: longPath + " (pid 1597816)",
			Prompt: "[sudo] password for enzord: "}, "Authenticate", "cancel"},
	}
}

// insideScroller says whether wd sits in a scrolled window under d.
func insideScroller(wd gtk.Widgetter, d *amDialog) bool {
	stop := gtk.BaseWidget(d)
	for p := gtk.BaseWidget(wd).Parent(); p != nil; p = gtk.BaseWidget(p).Parent() {
		if gtk.BaseWidget(p) == stop || coreglib.InternObject(p).Native() == coreglib.InternObject(d).Native() {
			return false
		}
		if _, ok := p.(*gtk.ScrolledWindow); ok {
			return true
		}
	}
	return false
}

// wrappedLists are d's drawn item lists (one name, or "name version", per
// line) that break a line: each as "first line: N lines drawn for M".
func wrappedLists(d *amDialog) []string {
	var out []string
	eachWidget(d, func(x gtk.Widgetter) {
		l, ok := x.(*gtk.Label)
		if !ok || !l.IsDrawable() || !l.HasCSSClass("monospace") {
			return
		}
		want := strings.Count(l.Text(), "\n") + 1
		if got := l.Layout().LineCount(); got != want {
			first, _, _ := strings.Cut(l.Text(), "\n")
			out = append(out, fmt.Sprintf("%s…: %d lines drawn for %d", first, got, want))
		}
	})
	return out
}

// overflows says whether sw has something to scroll.
func overflows(sw *gtk.ScrolledWindow) bool {
	adj := sw.VAdjustment()
	return adj.Upper()-adj.PageSize() > 1
}

func wideLong(c *covEnv) {
	w := c.w
	c.quiet()
	prompts := longPrompts()
	size, next := 0, 0
	// within: b lies inside the box out (half a pixel's rounding).
	within := func(b, out box) bool {
		return b.x >= out.x-0.5 && b.y >= out.y-0.5 && b.x+b.w <= out.x+out.w+0.5 && b.y+b.h <= out.y+out.h+0.5
	}
	measure := func(name string, d *amDialog, yes string) {
		wd, ht := w.win.Width(), w.win.Height()
		win := box{0, 0, float64(wd), float64(ht)}
		var sheet gtk.Widgetter
		eachWidget(d, func(x gtk.Widgetter) {
			if sheet == nil && gtk.BaseWidget(x).CSSName() == "sheet" {
				sheet = x
			}
		})
		sb, ok := box{}, false
		if sheet != nil {
			sb, ok = boundsIn(sheet, w.win)
		}
		if !ok {
			c.fail("%s at %dx%d: no sheet placed", name, wd, ht)
			return
		}
		c.ok(within(sb, win), "%s at %dx%d: its sheet %+v is not inside the window", name, wd, ht, sb)
		// The heading: a wide dialog's top bar label, an AdwAlertDialog's
		// heading label.
		var head gtk.Widgetter
		eachWidget(d, func(x gtk.Widgetter) {
			if l, ok := x.(*gtk.Label); ok && head == nil && l.IsDrawable() && (l.HasCSSClass("am-wide-heading") || (d.alert != nil && l.Text() == d.Heading())) {
				head = x
			}
		})
		headIn, btnIn := false, false
		if head != nil {
			if hb, ok := boundsIn(head, w.win); ok {
				headIn = within(hb, sb) && within(hb, win)
			}
		}
		btn := findButton(gtk.BaseWidget(d), yes)
		if btn != nil {
			if bb, ok := boundsIn(btn, w.win); ok {
				btnIn = btn.IsDrawable() && within(bb, sb) && within(bb, win)
			}
		}
		c.ok(headIn, "%s at %dx%d: its heading isn't drawn inside the dialog", name, wd, ht)
		c.ok(btnIn, "%s at %dx%d: its %q button isn't drawn inside the dialog", name, wd, ht, yes)
		middle, inner := false, 0
		var innerH []int
		if d.alert == nil {
			c.ok(head != nil && !insideScroller(head, d), "%s at %dx%d: its heading scrolls", name, wd, ht)
			c.ok(btn != nil && !insideScroller(btn, d), "%s at %dx%d: its buttons scroll", name, wd, ht)
			if sc, ok := d.parts[1].(*gtk.ScrolledWindow); ok {
				middle = overflows(sc)
				eachWidget(sc.Child(), func(x gtk.Widgetter) {
					if s, ok := x.(*gtk.ScrolledWindow); ok && s.IsDrawable() && overflows(s) {
						inner++
						innerH = append(innerH, s.Height())
					}
				})
				innerH = append(innerH, -sc.Height())
			}
		} else {
			// The AdwAlertDialog's message area is its scrolled window.
			eachWidget(d, func(x gtk.Widgetter) {
				if s, ok := x.(*gtk.ScrolledWindow); ok && s.IsDrawable() && overflows(s) {
					middle = true
				}
			})
		}
		if ht < 600 && name != "step" && name != "askpass" {
			c.ok(middle || inner > 0, "%s at %dx%d: nothing in the middle scrolls (sheet %.0fx%.0f)", name, wd, ht, sb.w, sb.h)
		}
		if ht < 600 && d.alert == nil {
			// One scroller in a low window: the middle, the lists in it at
			// their whole height. A capped list inside a scrolling middle
			// shrank to a few rows instead (Dev 2's #96: the gate's rail
			// to ~3 steps, the orphans' lists to 110 px each).
			c.ok(inner == 0, "%s at %dx%d: %d lists scroll inside the middle (heights %v), want only the middle to scroll", name, wd, ht, inner, innerH)
		}
		if name == "gate" {
			// How many of the rail's steps are shown whole: inside every
			// scrolled window they sit in (the middle, and the rail's own
			// scroller where it had one).
			shown, total := 0, 0
			eachWidget(d, func(x gtk.Widgetter) {
				if !gtk.BaseWidget(x).HasCSSClass("am-rail-step") {
					return
				}
				total++
				rb, ok := boundsIn(x, w.win)
				if !ok || !gtk.BaseWidget(x).IsDrawable() {
					return
				}
				stop := coreglib.InternObject(d).Native()
				for p := gtk.BaseWidget(x).Parent(); p != nil && coreglib.InternObject(p).Native() != stop; p = gtk.BaseWidget(p).Parent() {
					if _, isSc := p.(*gtk.ScrolledWindow); isSc {
						if pb, ok := boundsIn(p, w.win); !ok || !within(rb, pb) {
							return
						}
					}
				}
				shown++
			})
			switch {
			case wd == 1460 && ht == 930:
				// The window has room for all 20: no cap of its own (it
				// stopped at 364 px, 14 steps, with 400 px to spare).
				c.ok(shown == total && total == 20 && !middle, "gate at 1460x930: %d of %d steps shown, the middle scrolls %v; want all 20, nothing scrolling", shown, total, middle)
			case ht < 600:
				// A low window shows a useful part of the plan, not ~3
				// steps in a 70 px rail (Dev 2's #96 at 620x500, 380x500).
				c.ok(shown >= 6, "gate at %dx%d: %d of %d steps shown, want at least 6", wd, ht, shown, total)
			}
			c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("gate %dx%d: %d of %d rail steps shown", wd, ht, shown, total))
		}
		if name == "orphans" && wd == 1460 && ht == 930 {
			// Two columns side by side, one "name version" per line:
			// neither wraps (the 333 px "Removed with them" wrapped
			// "python-dependency-of-orphan-123 0.123.0-2").
			c.ok(len(wrappedLists(d)) == 0, "orphans at 1460x930: lists wrap their lines: %q", wrappedLists(d))
		}
		tw := tooWide(d, wd)
		c.ok(len(tw) == 0, "%s at %dx%d: too wide: %q", name, wd, ht, tw)
		shoot(w, fmt.Sprintf("wide-long-%dx%d", wd, ht), name)
		c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf("%s %dx%d: sheet %.0fx%.0f at y=%.0f, middle scrolls %v, lists scrolling inside it %d %v",
			name, wd, ht, sb.w, sb.h, sb.y, middle, inner, innerH))
	}
	var step func()
	step = func() {
		if next == len(prompts) {
			next = 0
			size++
			if size == len(wideLongSizes) {
				c.finish()
				return
			}
			w.win.SetDefaultSize(wideLongSizes[size][0], wideLongSizes[size][1])
			coreglib.TimeoutAdd(400, step)
			return
		}
		p := prompts[next]
		c.feed(p.ev)
		coreglib.TimeoutAdd(600, func() {
			want := wideLongSizes[size]
			c.ok(w.win.Width() == want[0] && w.win.Height() == want[1], "the window is %dx%d, want %dx%d (the headless output too small?)",
				w.win.Width(), w.win.Height(), want[0], want[1])
			var id engine.PromptID
			switch ev := p.ev.(type) {
			case engine.NeedsConfirm:
				id = ev.ID
			case engine.NeedsSelection:
				id = ev.ID
			case engine.OutputReport:
				id = ev.ID
			case engine.NeedsPassword:
				id = ev.ID
			}
			if d := w.dialogs[id]; d == nil {
				c.fail("%s: no dialog", p.name)
			} else {
				measure(p.name, d, p.yes)
				c.press(d, p.ans)
			}
			next++
			coreglib.TimeoutAdd(150, step)
		})
	}
	w.win.SetDefaultSize(wideLongSizes[0][0], wideLongSizes[0][1])
	w.onApply = func(runview.Change) {}
	coreglib.TimeoutAdd(400, step)
}
