package ui

// Ignoring in the System health sheet (#288) on a display: a fake Source
// that keeps its ignore list in memory and splits the items the way the
// backend does (healthview.Report.ApplyIgnores); nothing touches a state
// folder.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
)

type ignoreHealth struct {
	mu        sync.Mutex
	units     []string
	ign       map[string]time.Time
	failWrite error
	runs      int
}

func newIgnoreHealth() *ignoreHealth {
	return &ignoreHealth{units: []string{"a.service", "b.service", "c.service"}, ign: map[string]time.Time{}}
}

func (f *ignoreHealth) Run(context.Context) healthview.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	var items []healthview.Item
	for _, u := range f.units {
		items = append(items, healthview.Item{Key: "unit:" + u, Label: u})
	}
	r := healthview.Report{At: time.Now(), Checks: []healthview.Check{
		{ID: "failed-units", Title: "Failed services", State: healthview.Look, Group: "system",
			Detail: fmt.Sprintf("%d failed: %s", len(f.units), strings.Join(f.units, ", ")),
			Copy:   "journalctl -b -u a.service", Items: items},
		{ID: "restart", Title: "Restart recommended", State: healthview.Look, Group: "system", Detail: "running 7.3.1 · installed 7.3.3",
			Items: []healthview.Item{{Key: "restart:7.3.3", Label: "Running 7.3.1"}}},
		{ID: "news", Title: "Arch news", State: healthview.Look, Group: "packages", Detail: "1 new since your last update: Manual intervention",
			Link:  "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/",
			Items: []healthview.Item{{Key: "news:1794000000", Label: "Manual intervention"}}},
		{ID: "disk", Title: "Disk space", State: healthview.OK, Group: "hardware", Detail: "/ 118 G free"},
		{ID: "mirror", Title: "Mirror", State: healthview.Unknown, Group: "packages", Detail: "can't reach it"},
	}}
	ign := make(map[string]time.Time, len(f.ign))
	for k, v := range f.ign {
		ign[k] = v
	}
	r.ApplyIgnores(ign)
	return r
}

func (f *ignoreHealth) UnreadNews(context.Context) (healthview.Unread, error) {
	return healthview.Unread{}, nil
}

func (f *ignoreHealth) Ignore(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	if _, ok := f.ign[key]; !ok {
		f.ign[key] = time.Now()
	}
	return nil
}

func (f *ignoreHealth) Unignore(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	delete(f.ign, key)
	return nil
}

func (f *ignoreHealth) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.ign[key]
	return ok
}

func (f *ignoreHealth) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

var ignFake *ignoreHealth

func init() {
	covScenarios["cov-health-ignore"] = covHealthIgnore
	covOptions["cov-health-ignore"] = func(o *Options, _ *report) {
		ignFake = newIgnoreHealth()
		o.Health = ignFake
	}
}

// TestGUIHealthIgnore runs the Ignore scenario in the default look (light)
// and in glass (dark); its own part of headless.sh, since TestGUIHealth
// already takes most of a part's time.
func TestGUIHealthIgnore(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	child(t, "cov-health-ignore", "XDG_CONFIG_HOME="+t.TempDir(), glassSchemeEnv+"=light", glassModeEnv+"=default")
	child(t, "cov-health-ignore", "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"=dark", glassModeEnv+"=glass")
}

// shootWidget writes a PNG of one widget (a popover's content, which the
// window's own capture does not hold) next to the window shots.
func shootWidget(wd coreglib.Objector, name string) {
	dir := os.Getenv(shotsEnv)
	if dir == "" {
		return
	}
	if os.MkdirAll(dir, 0o700) == nil {
		_ = savePNG(wd, filepath.Join(dir, name+".png"))
	}
}

func covHealthIgnore(c *covEnv) {
	w, f := c.w, ignFake
	c.quiet()
	glassSetup(w)
	tagn := func(s string) string {
		return fmt.Sprintf("health-%s-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv), s)
	}
	var d *amDialog
	rowIDs := func() []string {
		var ids []string
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if n := gtk.BaseWidget(wd).Name(); strings.HasPrefix(n, "health-") {
				ids = append(ids, strings.TrimPrefix(n, "health-"))
			}
		})
		return ids
	}
	ignoredIDs := func() []string {
		var ids []string
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if n := gtk.BaseWidget(wd).Name(); strings.HasPrefix(n, "ignored-") {
				ids = append(ids, strings.TrimPrefix(n, "ignored-"))
			}
		})
		return ids
	}
	under := func(name string) gtk.Widgetter {
		var found gtk.Widgetter
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if found == nil && gtk.BaseWidget(wd).Name() == name {
				found = wd
			}
		})
		return found
	}
	buttonIn := func(root gtk.Widgetter, label string) *gtk.Button {
		var found *gtk.Button
		if root != nil {
			eachWidget(root, func(wd gtk.Widgetter) {
				if b, ok := wd.(*gtk.Button); ok && found == nil && b.Label() == label {
					found = b
				}
			})
		}
		return found
	}
	menuIn := func(root gtk.Widgetter) *gtk.MenuButton {
		var found *gtk.MenuButton
		if root != nil {
			eachWidget(root, func(wd gtk.Widgetter) {
				if b, ok := wd.(*gtk.MenuButton); ok && found == nil {
					found = b
				}
			})
		}
		return found
	}
	text := func() string { return dialogLabels(d, nil) }
	idle := func() bool { return !w.health.run.Running() }
	cellLine := func() string { _, _, _, line, _ := cellText(w); return line }
	click := func(get func() *gtk.Button) func() {
		return func() {
			b := get()
			if b == nil {
				c.fail("a button is missing")
				return
			}
			b.Emit("clicked")
		}
	}
	popBox := func(row string) gtk.Widgetter {
		mb := menuIn(under("health-" + row))
		if mb == nil {
			return nil
		}
		return mb.Popover().Child()
	}
	var base int
	steps := []func() bool{
		until(func() bool { return w.health.report != nil && idle() }),
		once(func() { w.win.SetDefaultSize(1440, 900); w.openHealth(); d = w.health.sheet }, func() bool { return d != nil && idle() }),
		once(func() {
			c.ok(cellLine() == "3 things need your attention", "the cell says %q before ignoring anything", cellLine())
			c.ok(menuIn(under("health-failed-units")) != nil && menuIn(under("health-failed-units")).Label() == "Ignore", "the row with 3 units has no Ignore menu")
			r := buttonIn(under("health-restart"), "Ignore")
			c.ok(r != nil && r.TooltipText() == "Stop counting this until something new shows up here", "the single-item Ignore: %v", r)
			c.ok(buttonIn(under("health-news"), "Ignore") != nil && buttonIn(under("health-news"), "Read on archlinux.org") != nil, "the news row lacks Ignore or Read")
			c.ok(!strings.Contains(text(), "IGNORED"), "an IGNORED section with nothing ignored")
			base = f.runCount()
		}, nil),
		// A single-item row: Ignore moves it to IGNORED and the count drops.
		once(click(func() *gtk.Button { return buttonIn(under("health-restart"), "Ignore") }), func() bool { return idle() && !slices.Contains(rowIDs(), "restart") }),
		once(func() {
			c.ok(f.has("restart:7.3.3"), "the key was not written to the list")
			c.ok(f.runCount() > base, "no run followed the Ignore")
			c.ok(slices.Equal(ignoredIDs(), []string{"restart:7.3.3"}), "the IGNORED rows are %v", ignoredIDs())
			t := text()
			c.ok(strings.Contains(t, "IGNORED") && strings.Contains(t, "Restart recommended") && strings.Contains(t, "Running 7.3.1 · ignored just now"), "the IGNORED section: %s", t)
			c.ok(cellLine() == "2 things need your attention", "the cell says %q after ignoring one", cellLine())
			c.ok(!strings.Contains(strings.Split(t, "IGNORED")[0], "Restart recommended"), "the ignored row is still among the live ones")
		}, nil),
		// A row with several items: the popover ignores one; the row stays Look.
		once(func() { menuIn(under("health-failed-units")).Popup() }, func() bool { return popBox("failed-units") != nil && gtk.BaseWidget(popBox("failed-units")).Mapped() }),
		once(func() {
			box := popBox("failed-units")
			n := 0
			eachWidget(box, func(wd gtk.Widgetter) {
				if b, ok := wd.(*gtk.Button); ok && b.Label() == "Ignore" {
					n++
				}
			})
			c.ok(n == 3 && buttonIn(box, "Ignore all") != nil, "the popover has %d Ignore buttons (want 3) and Ignore all %v", n, buttonIn(box, "Ignore all") != nil)
			c.ok(strings.Contains(strings.Join(labelsUnder(gtk.BaseWidget(box)), "\n"), "b.service"), "the popover does not list the units")
			shootWidget(box, tagn("ignore-popover"))
			first := buttonIn(box, "Ignore")
			first.Emit("clicked")
		}, func() bool { return idle() && f.has("unit:a.service") && strings.Contains(text(), "· 1 ignored") }),
		once(func() {
			c.ok(slices.Contains(rowIDs(), "failed-units"), "a partly ignored row left the list")
			c.ok(strings.Contains(text(), "2 failed: a.service, b.service, c.service · 1 ignored") || strings.Contains(text(), "· 1 ignored"), "no count in the row: %s", text())
			c.ok(len(ignoredIDs()) == 2, "the IGNORED rows are %v, want 2", ignoredIDs())
			c.ok(cellLine() == "2 things need your attention", "the cell says %q with one unit ignored", cellLine())
		}, nil),
		// Ignore all: the check is Ignored and leaves the live rows.
		once(func() { menuIn(under("health-failed-units")).Popup() }, func() bool { return popBox("failed-units") != nil && gtk.BaseWidget(popBox("failed-units")).Mapped() }),
		once(func() { buttonIn(popBox("failed-units"), "Ignore all").Emit("clicked") }, func() bool { return idle() && !slices.Contains(rowIDs(), "failed-units") }),
		once(func() {
			c.ok(len(ignoredIDs()) == 4, "the IGNORED rows are %v, want 4 (3 units and the kernel)", ignoredIDs())
			c.ok(cellLine() == "1 thing needs your attention", "the cell says %q", cellLine())
			adj := w.health.scroll.VAdjustment()
			adj.SetValue(adj.Upper()) // down to the IGNORED section
		}, nil),
		until(func() bool { return true }),
		once(func() { shoot(w, "health", tagn("ignore-sheet")) }, nil),
		// Undo brings an item back.
		once(click(func() *gtk.Button { return buttonIn(under("ignored-restart:7.3.3"), "Undo") }), func() bool { return idle() && slices.Contains(rowIDs(), "restart") }),
		once(func() {
			c.ok(!f.has("restart:7.3.3"), "Undo left the key on the list")
			c.ok(len(ignoredIDs()) == 3 && cellLine() == "2 things need your attention", "after Undo: %v, cell %q", ignoredIDs(), cellLine())
		}, nil),
		// A new item alerts again, beside the ignored ones.
		once(func() {
			f.mu.Lock()
			f.units = append(f.units, "d.service")
			f.mu.Unlock()
			w.refreshHealth()
		}, func() bool { return idle() && slices.Contains(rowIDs(), "failed-units") }),
		once(func() {
			c.ok(buttonIn(under("health-failed-units"), "Ignore") != nil, "the one new unit has no plain Ignore button")
			c.ok(strings.Contains(text(), "d.service") && strings.Contains(text(), "· 3 ignored"), "the new unit is not in the row: %s", text())
			c.ok(cellLine() == "3 things need your attention", "the cell says %q with a new item", cellLine())
		}, nil),
		// The list cannot be written: a toast, nothing else changes.
		once(func() {
			f.mu.Lock()
			f.failWrite = errors.New("too many ignored items")
			f.mu.Unlock()
			base = f.runCount()
			click(func() *gtk.Button { return buttonIn(under("health-news"), "Ignore") })()
		}, func() bool {
			return slices.Contains(w.toasted, "Couldn't save the ignore list: too many ignored items")
		}),
		until(func() bool { return idle() }),
		once(func() {
			c.ok(slices.Contains(rowIDs(), "news") && !f.has("news:1794000000"), "a failed write changed the row")
			c.ok(f.runCount() == base, "a failed write ran the checks (%d, %d)", base, f.runCount())
			c.ok(cellLine() == "3 things need your attention", "the cell says %q after a failed write", cellLine())
			f.mu.Lock()
			f.failWrite = nil
			f.mu.Unlock()
		}, nil),
		// Everything ignored: the cell is calm.
		once(click(func() *gtk.Button { return buttonIn(under("health-news"), "Ignore") }), func() bool { return idle() && !slices.Contains(rowIDs(), "news") }),
		once(click(func() *gtk.Button { return buttonIn(under("health-restart"), "Ignore") }), func() bool { return idle() && !slices.Contains(rowIDs(), "restart") }),
		once(click(func() *gtk.Button { return buttonIn(under("health-failed-units"), "Ignore") }), func() bool { return idle() && !slices.Contains(rowIDs(), "failed-units") }),
		once(func() {
			emph, _, tagShown, line, lineShown := cellText(w)
			c.ok(!emph && !tagShown && !lineShown, "with everything ignored the cell is emphasised %v, tag %v, line %q", emph, tagShown, line)
			c.ok(!strings.Contains(strings.ToUpper(text()), "NEEDS YOUR ATTENTION"), "a 'Needs your attention' section with everything ignored: %s", text())
			c.ok(len(ignoredIDs()) == 6, "the IGNORED rows are %v, want 6", ignoredIDs())
		}, nil),
		once(func() { c.press(d, "close") }, func() bool { return w.health.sheet == nil }),
	}
	c.waitSeq(120, steps...)
}
