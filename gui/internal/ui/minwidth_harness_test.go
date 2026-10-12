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
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// envNarrowW is real-narrow's window width (the parent sets it).
const envNarrowW = "ARCHMAID_GUI_NARROW_W"

// envWidths overrides the widths TestGUIMinWidth and TestGUIMinWidthStates
// run at ("380,960"): for screenshots at another width (ARCHMAID_GUI_SHOTS).
const envWidths = "ARCHMAID_GUI_NARROW_WIDTHS"

// narrowWidths is envWidths' list, or def without it.
func narrowWidths(t *testing.T, def ...int) []int {
	v := os.Getenv(envWidths)
	if v == "" {
		return def
	}
	var out []int
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 360 {
			t.Fatalf("%s=%q: %q isn't a width of at least 360", envWidths, v, f)
		}
		out = append(out, n)
	}
	return out
}

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

// widestUnder is the largest minimum width of root and every mapped
// widget under it: for a dialog, its sheet's content (the dialog itself
// measures 0, being shown in the window's dialog host; the button layout
// it doesn't use is unmapped).
func widestUnder(root gtk.Widgetter) int {
	n := minWidth(root)
	for c := gtk.BaseWidget(root).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if gtk.BaseWidget(c).Mapped() {
			n = max(n, widestUnder(c))
		}
	}
	return n
}

// narrowMeasure is one moment's measure: the window's width, its
// content's minimum (the toast overlay, which libadwaita checks against
// the window) and what is too wide in it or in an open dialog (a dialog
// sits beside the content, in the window's dialog host).
func narrowMeasure(w *Window, moment string) string {
	width := w.win.Width()
	s := fmt.Sprintf("%s width=%d content=%d page=%s", moment, width, minWidth(w.toasts), w.stack.VisibleChildName())
	wide := tooWide(w.toasts, width)
	dialogs := slices.Collect(maps.Values(w.dialogs))
	if w.quitDialog != nil {
		dialogs = append(dialogs, w.quitDialog)
	}
	for _, d := range dialogs {
		// The dialog's own minimum, for the log: its sheet asks for this
		// much of the window (libadwaita's "AdwFloatingSheet … exceeds").
		s += fmt.Sprintf(" dialog=%d", widestUnder(d))
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
				moment := phase
				if cf := ch.Opened.Confirm; cf != nil && cf.Kind == engine.ConfirmOrphans {
					// The VM's dialog: its counts in the title, the dry
					// run's banner, both lists.
					moment += "-orphans"
					c.ok(cf.Title == vmOrphansTitle, "the orphans dialog's title is %q, want %q", cf.Title, vmOrphansTitle)
					c.ok(w.drvDry, "the orphans dialog isn't a dry run's")
					// The cascade's lines carry versions; the orphans
					// among them aren't listed twice.
					// One-line labels: the headings, not the lists.
					labels := slices.DeleteFunc(labelsUnder(&d.Widget), func(l string) bool { return strings.Contains(l, "\n") })
					for _, want := range []string{dryBannerText, "Orphaned (15)", "Removed with them (24)"} {
						c.ok(slices.Contains(labels, want), "the orphans dialog shows no %q: %q", want, labels)
					}
				}
				measure(fmt.Sprintf("%s-prompt-%d", moment, id))
				if cf := ch.Opened.Confirm; cf != nil && len(cf.Plan) > 0 {
					// The real plan's rail: tags under the titles in a
					// narrow dialog, at the line's end in a wide one.
					c.ok(checkRailTags(c, d, moment+"-gate") >= 7, "%s: fewer rail steps with tags drawn than the plan's 7", moment)
				}
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
	c.ok(checkTags(c, "overview") >= 10, "overview: fewer tag lines found than the plan's 7 and the cells' 3")
	// The rest of the Overview scrolled into view for the screenshots: the
	// hero's buttons and the last run, then the cells at its end.
	scroll, _ := w.stack.ChildByName("overview").(*gtk.ScrolledWindow)
	scrollTo := func(end bool) {
		if scroll == nil {
			return
		}
		adj := scroll.VAdjustment()
		v := adj.Upper()
		if b, ok := boundsIn(w.heroButtons, scroll.Child()); ok && !end {
			v = b.y - 160
		}
		adj.SetValue(v)
	}
	// Each shot 150 ms after its scroll, so its frames are drawn.
	scrollTo(false)
	scrolled := time.Now()
	var midDone bool
	until("the Overview's middle", func() bool { return time.Since(scrolled) > 150*time.Millisecond }, func() {
		measure("overview-mid")
		scrollTo(true)
		scrolled, midDone = time.Now(), true
	})
	until("the Overview's end", func() bool { return midDone && time.Since(scrolled) > 150*time.Millisecond }, func() {
		measure("overview-end")
		if scroll != nil {
			scroll.VAdjustment().SetValue(0)
		}
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
						c.ok(checkTags(c, "dry-end") >= 3, "dry-end: fewer than 3 step headers with tags drawn")
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
	})
}

// TestGUIMinWidth: at 360 px (libadwaita's smallest window), 480 px and
// 960 px wide, with the real Full Cleanup plan, its gate, the Run page
// and real toasts, the window's content never asks for more width than
// the window has, and GTK logs no warning.
func TestGUIMinWidth(t *testing.T) {
	needDisplay(t)
	for _, wd := range narrowWidths(t, 360, 480, 960) {
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
			for _, want := range []string{"overview", "overview-mid", "overview-end", "real", "cancelled-toast", "dry", "dry-orphans", "dry-end", "toast-0", "toast-3", "logs", "settings"} {
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

// vmOrphans and vmCascade are the test VM's real orphan plan (Dev 5's
// b5r round, its dryrun/orphans.log): 15 orphans, 39 packages once the
// dependencies cascade. real-narrow's Driver plans them, so its dry run
// opens the orphans dialog the VM showed 392 px wide in a 360 px window.
var (
	vmOrphans = strings.Fields("asar clojure gobject-introspection meson nvm patchelf pnpm python-build python-clickgen python-dbusmock python-hatchling python-installer vala walbottle-git xorg-server-xvfb")
	vmCascade = strings.Split(`ada 4.0.0-1.1
asar 4.3.1-1
clojure 1.12.5.1664-1
docbook-xml 4.5-11
docbook-xsl 1.79.2-9
glib2-docs 2.90.1-1.1
gobject-introspection 1.86.0-2.1
gtk-doc 1.37.0-1
java-environment-common 3-6
java-runtime-common 3-6
jdk-openjdk 27.u35-2
libnet 2:1.3-2.1
meson 1.12.1-2
node-gyp 13.1.0-1
nodejs 26.11.1-2
nodejs-nopt 10.0.1-1
nvm 0.40.7-1
patchelf 0.19.2-1.1
pnpm 12.12.1-1
python-build 1.6.0-1
python-clickgen 2.2.5-1
python-dbusmock 0.38.1-1
python-editables 0.6-1
python-hatchling 1.32.3-1
python-installer 1.0.1-1
python-markdown 3.11.0-1
python-pathspec 1.1.1-1
python-pluggy 1.6.0-3
python-pygments 2.21.0-1
python-pyproject-hooks 1.3.3-1
python-toml 0.10.2-13
python-tomlkit 0.15.1-1
python-tqdm 4.70.1-1
python-trove-classifiers 2026.9.21.13-1
semver 7.8.5-1
simdjson 1:5.0.3-1.1
vala 0.56.19-2.1
walbottle-git 0.2.0.r71.ge643df3-3
xorg-server-xvfb 21.1.25-1.1`, "\n")
)

// vmOrphansTitle is the VM's orphans dialog title, from its plan's counts.
const vmOrphansTitle = "Remove 15 orphaned package(s) — 39 total once dependencies cascade?"

// toastControl is the text of the warning real-narrow provokes on purpose.
const toastControl = "toast-control-never-closed"

// glibWarned: a stderr line that is a GLib warning, critical or error (the
// log writer prints them as slog lines: "WARN ...", "ERROR ...").
func glibWarned(l string) bool {
	return strings.Contains(l, " WARN ") || strings.Contains(l, " ERROR ") || strings.Contains(l, "-WARNING") || strings.Contains(l, "-CRITICAL")
}
