package ui

// The window's minimum width with the real content (Dev 5's VM round b5):
// the real Driver's Full Cleanup plan on the Overview (10 steps, their real
// labels and root tags, the hero's kicker), its gate dialog, the Run page
// with the plan's rows, and the toasts the run shows. Libadwaita lets the
// window go down to 360 px whatever its content asks; content wider than
// that is cut off on the right, and AdwToastOverlay logs "exceeds
// AdwApplicationWindow width". So at each width the content's measured
// minimum must fit the window, and GTK must log nothing.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
)

// envNarrowW is real-narrow's window width (the parent sets it).
const envNarrowW = "ARCHMAID_GUI_NARROW_W"

// narrowH is real-narrow's window height: the VM's, a tall narrow window.
const narrowH = 720

func init() { covScenarios["real-narrow"] = realNarrow }

// minWidth is w's minimum width as GTK measures it (0 when hidden).
func minWidth(w gtk.Widgetter) int {
	if !gtk.BaseWidget(w).IsVisible() {
		return 0
	}
	min, _, _, _ := gtk.BaseWidget(w).Measure(gtk.OrientationHorizontal, -1)
	return min
}

// describe names a widget for a report: its GType, CSS classes and text.
func describe(w gtk.Widgetter) string {
	s := coreglib.InternObject(w).TypeFromInstance().Name()
	if cs := gtk.BaseWidget(w).CSSClasses(); len(cs) > 0 {
		s += "." + strings.Join(cs, ".")
	}
	text := ""
	switch x := w.(type) {
	case *gtk.Label:
		text = x.Text()
	case *gtk.Button:
		text = x.Label()
	}
	if text != "" {
		if r := []rune(text); len(r) > 48 {
			text = string(r[:47]) + "…"
		}
		s += fmt.Sprintf(" %q", text)
	}
	return s
}

// tooWide lists the widgets under root that are wider at their minimum
// than limit: the deepest ones whose minimum is over it while none of
// their children's is (a label that can't shrink, or a row whose children
// only together are too wide), each with its minimum and its widest
// child's. It follows two ways down: the widgets whose minimum makes
// root's (a stack's hidden page counts too), and every mapped widget,
// which reaches what a breakpoint bin or a dialog doesn't pass up (both
// ask less than their content does).
func tooWide(root gtk.Widgetter, limit int) []string {
	var out []string
	var walk func(p gtk.Widgetter, contributes bool)
	walk = func(p gtk.Widgetter, contributes bool) {
		over := minWidth(p) > limit && (contributes || gtk.BaseWidget(p).Mapped())
		deeper := false
		var widest gtk.Widgetter
		for c := gtk.BaseWidget(p).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if !gtk.BaseWidget(c).IsVisible() {
				continue
			}
			if widest == nil || minWidth(c) > minWidth(widest) {
				widest = c
			}
			if minWidth(c) > limit && (over || gtk.BaseWidget(c).Mapped()) {
				deeper = true
			}
			walk(c, over)
		}
		if over && !deeper {
			x := fmt.Sprintf("%s min=%d", describe(p), minWidth(p))
			if widest != nil {
				x += fmt.Sprintf(" (widest child: %s min=%d)", describe(widest), minWidth(widest))
			}
			out = append(out, x)
		}
	}
	walk(root, true)
	return out
}

// narrowMeasure is one moment's measure: the window's width, its
// content's minimum (the toast overlay, which libadwaita checks against
// the window) and what is too wide in it or in an open dialog (a dialog
// sits beside the content, in the window's dialog host).
func narrowMeasure(w *Window, moment string) string {
	width := w.win.Width()
	s := fmt.Sprintf("%s width=%d content=%d page=%s", moment, width, minWidth(w.toasts), w.stack.VisibleChildName())
	wide := tooWide(w.toasts, width)
	for _, d := range w.dialogs {
		wide = append(wide, tooWide(d, width)...)
	}
	for _, x := range wide {
		s += "\n  too wide: " + x
	}
	return s
}

// realNarrow: at envNarrowW, the Overview with the real plan, then Full
// Cleanup's gate declined (its toast), then Dry run first through to its
// end (every confirm accepted; the Run page with the plan's rows and its
// end toast), then the longest toasts the window shows, one at a time,
// then the Logs and Settings pages; each measured.
func realNarrow(c *covEnv) {
	w := c.w
	measure := func(m string) {
		c.rep.Narrow = append(c.rep.Narrow, narrowMeasure(w, m))
		shoot(w, fmt.Sprintf("real-narrow-%d", w.win.Width()), m)
	}
	var phase string
	finished := false
	w.onApply = func(ch runview.Change) {
		if ch.Opened != nil {
			id := ch.Opened.ID
			coreglib.TimeoutAdd(250, func() {
				d, ok := w.dialogs[id]
				if !ok {
					c.fail("prompt %d has no dialog", id)
					return
				}
				measure(fmt.Sprintf("%s-prompt-%d", phase, id))
				resp := "yes"
				if phase == "real" {
					resp = "no" // the gate, declined
				}
				c.press(d, resp)
			})
		}
		if ch.Finished {
			finished = true
		}
	}
	// until polls cond every 25 ms, then runs next (a stuck run fails).
	until := func(what string, cond func() bool, next func()) {
		polls := 0
		var poll func() bool
		poll = func() bool {
			if cond() {
				next()
				return false
			}
			if polls++; polls > 400 {
				c.fail("%s: gave up waiting", what)
				c.finish()
				return false
			}
			return true
		}
		coreglib.TimeoutAdd(25, poll)
	}
	settled := func() bool { return finished && !w.busy }
	measure("overview")
	c.ok(w.heroSteps.Visible(), "the hero shows no plan: not the real Driver's Env")
	phase, finished = "real", false
	w.menu[actions.IDFullCleanup].start()
	until("the declined gate", settled, func() {
		coreglib.TimeoutAdd(250, func() {
			measure("cancelled-toast")
			phase, finished = "dry", false
			w.dryFirst.Emit("clicked")
			until("the dry run", settled, func() {
				coreglib.TimeoutAdd(250, func() {
					measure("dry-end")
					c.rep.Toasts = append([]string(nil), w.toasted...)
					var show func(i int)
					show = func(i int) {
						if i == len(longToasts) {
							// The other two pages, as the sidebar shows them.
							c.seq(150, func() { w.pickPage("logs") }, func() { measure("logs") },
								func() { w.pickPage("settings") }, func() { measure("settings") })
							return
						}
						// High: shown at once, not queued behind the run's
						// own toasts (each up for seconds).
						t := plainToast(longToasts[i])
						t.SetPriority(adw.ToastPriorityHigh)
						w.toasts.AddToast(t)
						coreglib.TimeoutAdd(200, func() {
							c.ok(slices.Contains(labelsUnder(&w.toasts.Widget), longToasts[i]), "toast %q isn't shown", longToasts[i])
							measure(fmt.Sprintf("toast-%d", i))
							t.Dismiss()
							coreglib.TimeoutAdd(150, func() { show(i + 1) })
						})
					}
					show(0)
				})
			})
		})
	})
}

// TestGUIMinWidth: at 360 px (libadwaita's smallest window), 480 px and
// 960 px wide, with the real Full Cleanup plan, its gate, the Run page
// and real toasts, the window's content never asks for more width than
// the window has, and GTK logs no warning.
func TestGUIMinWidth(t *testing.T) {
	needDisplay(t)
	for _, wd := range []int{360, 480, 960} {
		t.Run(strconv.Itoa(wd), func(t *testing.T) {
			_, env := installShims(t, shimOpts{})
			var buf bytes.Buffer
			rep, _ := childCmd(t, "real-narrow", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) },
				append(env, fmt.Sprintf("%s=%d", envNarrowW, wd))...)
			controls := 0
			for _, l := range strings.Split(buf.String(), "\n") {
				switch {
				case !glibWarned(l):
				case strings.Contains(l, toastControl):
					controls++
				default:
					t.Errorf("GTK logged: %s", l)
				}
			}
			if controls == 0 {
				t.Errorf("the provoked GTK warning never reached the captured stderr (%d bytes)", buf.Len())
			}
			moments := map[string]bool{}
			for _, m := range rep.Narrow {
				t.Log(m)
				var name string
				var width, content int
				if _, err := fmt.Sscanf(m, "%s width=%d content=%d", &name, &width, &content); err != nil {
					t.Errorf("measure %q: %v", m, err)
					continue
				}
				moments[strings.SplitN(name, "-prompt-", 2)[0]] = true
				if width != wd {
					t.Errorf("%s: the window is %d px wide, want %d", name, width, wd)
				}
				if content > width || strings.Contains(m, "too wide") {
					t.Errorf("%s: the content asks for %d px in a %d px window:\n%s", name, content, width, m)
				}
			}
			for _, want := range []string{"overview", "real", "cancelled-toast", "dry", "dry-end", "toast-0", "toast-3", "logs", "settings"} {
				if !moments[want] {
					t.Errorf("never measured %s (%q)", want, rep.Narrow)
				}
			}
			// The declined gate's own toast was up when it was measured.
			if t.Logf("toasts: %q", rep.Toasts); !slices.Contains(rep.Toasts, "Run cancelled, nothing ran") {
				t.Errorf("toasts %q, want the declined gate's", rep.Toasts)
			}
		})
	}
}

// longToasts are the longest toasts the window shows: the failed end with
// refused steps, a notice at ShortMax runes and an error with a path.
var longToasts = []string{
	"Run completed",
	"Run finished with 1 failed step · 5 steps not authorised",
	strings.Repeat("A notice that runs on and on. ", 4)[:runview.ShortMax-1] + "…",
	"Couldn't open it: open /home/someone/.local/state/archmaid/runs/2026-10-12_09-30-00_full-cleanup: no such file",
}

// toastControl is the text of the warning real-narrow provokes on purpose.
const toastControl = "toast-control-never-closed"

// glibWarned: a stderr line that is a GLib warning, critical or error (the
// log writer prints them as slog lines: "WARN ...", "ERROR ...").
func glibWarned(l string) bool {
	return strings.Contains(l, " WARN ") || strings.Contains(l, " ERROR ") || strings.Contains(l, "-WARNING") || strings.Contains(l, "-CRITICAL")
}
