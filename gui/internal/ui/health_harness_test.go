package ui

// System health (#270), on the headless display: the Overview cell calm and
// emphasised, the sheet's order and its buttons, the news callout of the
// confirms and its 1.5 s cutoff, Run topgrade always shown, and never two
// checks at once. The checks are a fake Source.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// fakeHealth is a healthview.Source the tests steer.
type fakeHealth struct {
	mu          sync.Mutex
	runs        int
	cur, peak   int
	unreadCalls int
	report      healthview.Report
	hold        chan struct{} // when set, Run waits for it (or its context)
	runDelay    time.Duration
	unread      func(ctx context.Context) (healthview.Unread, error)
}

func (f *fakeHealth) Run(ctx context.Context) healthview.Report {
	f.mu.Lock()
	f.runs++
	f.cur++
	f.peak = max(f.peak, f.cur)
	hold, delay := f.hold, f.runDelay
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	time.Sleep(delay)
	f.mu.Lock()
	f.cur--
	r := f.report
	f.mu.Unlock()
	r.At = time.Now()
	return r
}

func (f *fakeHealth) UnreadNews(ctx context.Context) (healthview.Unread, error) {
	f.mu.Lock()
	f.unreadCalls++
	fn := f.unread
	f.mu.Unlock()
	if fn == nil {
		return healthview.Unread{}, nil
	}
	return fn(ctx)
}

func (f *fakeHealth) set(r healthview.Report) { f.mu.Lock(); f.report = r; f.mu.Unlock() }
func (f *fakeHealth) counts() (runs, peak, unread int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs, f.peak, f.unreadCalls
}

var healthFake *fakeHealth

func look(id, title, detail string) healthview.Check {
	return healthview.Check{ID: id, Title: title, State: healthview.Look, Detail: detail, Group: "system"}
}

var (
	calmReport = healthview.Report{Checks: []healthview.Check{
		{ID: "disk", Title: "Disk space", State: healthview.OK, Detail: "/ 118 G free", Group: "hardware"},
		{ID: "pacnew", Title: "Config files to merge", State: healthview.OK, Detail: "No .pacnew or .pacsave files", Group: "packages"},
		{ID: "mirror", Title: "Mirror", State: healthview.Unknown, Detail: "can't reach it", Group: "packages"},
	}}
	twoLooks = healthview.Report{Checks: []healthview.Check{
		{ID: "disk", Title: "Disk space", State: healthview.OK, Detail: "/ 118 G free · /boot 412 M free", Group: "hardware"},
		{ID: "news", Title: "Arch news", State: healthview.Look, Group: "packages",
			Detail: "1 new since your last update: Manual intervention for pacman 7.0.0 and local repositories required",
			Link:   "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"},
		{ID: "restart", Title: "Restart recommended", State: healthview.Look, Group: "system", Detail: "running 7.3.1 · installed 7.3.3"},
		{ID: "failed-units", Title: "Failed services", State: healthview.Look, Group: "system", Detail: "1 failed: bluetooth.service",
			Copy: "journalctl -b -u bluetooth.service"},
		{ID: "mirror", Title: "Mirror", State: healthview.Unknown, Group: "packages", Detail: "can't reach it"},
	}}
	// calmEvil is calm plus a row whose link is not archlinux.org's: tests only.
	calmEvil = healthview.Report{Checks: append(append([]healthview.Check(nil), calmReport.Checks...),
		healthview.Check{ID: "evil", Title: "Odd link", State: healthview.OK, Group: "packages", Detail: "x", Link: "http://archlinux.org.evil.example/x"})}
)

func init() {
	for _, s := range []string{"cov-health-cell", "cov-health-sheet", "cov-health-news", "cov-health-runs", "cov-health-topgrade", "cov-health-scroll", "cov-health-reopen"} {
		covScenarios[s] = map[string]func(*covEnv){
			"cov-health-cell": covHealthCell, "cov-health-sheet": covHealthSheet, "cov-health-news": covHealthNews,
			"cov-health-runs": covHealthRuns, "cov-health-topgrade": covHealthTopgrade, "cov-health-scroll": covHealthScroll, "cov-health-reopen": covHealthReopen,
		}[s]
		covOptions[s] = func(o *Options, _ *report) {
			healthFake = &fakeHealth{report: calmReport}
			o.Health = healthFake
			newsWait = 400 * time.Millisecond
			switch s {
			case "cov-health-runs":
				healthEveryMS, healthStale = 700, 150*time.Millisecond
				healthFake.runDelay = 120 * time.Millisecond
			case "cov-health-reopen":
				healthSheetStale = 300 * time.Millisecond
				healthFake.runDelay = 400 * time.Millisecond
			case "cov-health-cell":
				healthFake.hold = make(chan struct{}) // the first run is held: "Checking…"
			}
		}
	}
}

func cellText(w *Window) (emph bool, tagText string, tagShown bool, line string, lineShown bool) {
	h := &w.health
	return h.cell.HasCSSClass("am-cell-attn"), h.look.Text(), h.look.IsVisible(), h.attn.Text(), h.attn.IsVisible()
}

func covHealthCell(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	glassSetup(w)
	look := os.Getenv(glassModeEnv)
	var warnInk, warnCSS [3]int
	tagn := func(s string) string { return fmt.Sprintf("health-%s-%s-%s", look, os.Getenv(glassSchemeEnv), s) }
	c.seq(300,
		func() { w.win.SetDefaultSize(1440, 900) }, // for the screenshots
		func() {
			emph, _, _, line, shown := cellText(w)
			c.ok(!emph && shown && line == "Checking…", "while the first run goes: emphasised %v, line %q (%v)", emph, line, shown)
			c.ok(w.menu[overview.IDHealth] != nil, "the System health cell is not in the grid")
			f.set(twoLooks)
			close(f.hold)
		},
		func() {
			emph, tagText, tagShown, line, shown := cellText(w)
			c.ok(emph && tagText == "look" && tagShown, "emphasised %v, tag %q (%v)", emph, tagText, tagShown)
			c.ok(shown && line == "3 things need your attention", "the third line %q (%v), want 3 things", line, shown)
			c.ok(w.health.icon.HasCSSClass("am-attn-icon"), "the icon is not warn-coloured")
			c.ok(w.health.icon.IconName() == "archmaid-health-symbolic", "the cell's icon is %q, not archmaid's own pulse", w.health.icon.IconName())
			p := gtk.IconThemeGetForDisplay(gdk.DisplayGetDefault()).LookupIcon("archmaid-health-symbolic", nil, 16, 1, gtk.TextDirNone, 0)
			c.ok(p != nil && p.IsSymbolic(), "the pulse icon is not symbolic (it would not take the warn colour)")
			// The pulse really is painted warn: its pixels, not just the class.
			if ink, css, ok := iconInk(c); ok {
				warnInk, warnCSS = ink, css
				c.ok(inkIs(ink, css), "the attention icon is painted %v, its CSS colour is %v", ink, css)
				c.ok(css != colourOf(w.health.cell), "the attention icon (%v) is not told apart from the cell's text", css)
			}
			shoot(w, "health", tagn("cell-attention"))
			f.set(healthview.Report{Checks: []healthview.Check{look1(), {ID: "mirror", State: healthview.Unknown}}})
			w.refreshHealth()
		},
		func() {
			_, _, _, line, _ := cellText(w)
			c.ok(line == "1 thing needs your attention", "singular: %q", line)
			f.set(calmReport)
			w.refreshHealth()
		},
		func() {
			// Calm: the others' look. Unknown never counts.
			emph, _, tagShown, _, lineShown := cellText(w)
			c.ok(!emph && !tagShown && !lineShown, "calm: emphasised %v, tag %v, line %v", emph, tagShown, lineShown)
			c.ok(!w.health.icon.HasCSSClass("am-attn-icon"), "a calm cell has a warn icon")
			if ink, css, ok := iconInk(c); ok {
				c.ok(inkIs(ink, css), "the calm icon is painted %v, its CSS colour is %v", ink, css)
				c.ok(!inkIs(ink, warnInk) && css != warnCSS, "the calm icon (%v) is still warn (%v)", ink, warnInk)
			}
			shoot(w, "health", tagn("cell-calm"))
		},
	)
}

func look1() healthview.Check {
	return look("restart", "Restart recommended", "running 7.3.1 · installed 7.3.3")
}

func covHealthSheet(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	glassSetup(w)
	f.set(twoLooks)
	var d *amDialog
	rowNames := func() []string {
		var ids []string
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if n := gtk.BaseWidget(wd).Name(); strings.HasPrefix(n, "health-") {
				ids = append(ids, strings.TrimPrefix(n, "health-"))
			}
		})
		return ids
	}
	button := func(row, label string) *gtk.Button {
		var found *gtk.Button
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if gtk.BaseWidget(wd).Name() == "health-"+row {
				eachWidget(wd, func(x gtk.Widgetter) {
					if b, ok := x.(*gtk.Button); ok && b.Label() == label && found == nil {
						found = b
					}
				})
			}
		})
		return found
	}
	rec := w.launcher.(*recordLauncher)
	checked := false
	c.seq(300,
		func() { w.refreshHealth() },
		func() {
			w.openHealth()
			d = w.health.sheet
			c.ok(d != nil && d.Heading() == "System health", "no sheet")
		},
		func() {
			// LOOK first, then unknown, then OK; in group order inside each.
			want := []string{"restart", "failed-units", "news", "mirror", "disk"}
			var heads []string
			eachWidget(&d.Widget, func(wd gtk.Widgetter) {
				if n := gtk.BaseWidget(wd).Name(); strings.HasPrefix(n, "section-") {
					heads = append(heads, strings.TrimPrefix(n, "section-"))
				}
			})
			c.ok(slices.Equal(heads, []string{"Needs your attention", "Packages", "Hardware"}), "section headers %v", heads)
			if got := rowNames(); !slices.Equal(got, want) {
				c.fail("the rows are %v, want %v (look, unknown, ok; system before packages before hardware)", got, want)
			}
			txt := dialogLabels(d, nil)
			c.ok(strings.Contains(txt, "Read-only: nothing is changed and no password is asked. Checked just now."), "subtitle: %q", txt)
			c.ok(strings.Contains(txt, "LOOK") && strings.Contains(txt, "OK"), "no state tags: %q", txt)
			shoot(w, "health", fmt.Sprintf("sheet-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv)))
			// Copy: the command on the clipboard, never run, with a toast.
			cp := button("failed-units", "Copy command")
			if cp == nil {
				c.fail("no Copy command on the failed services row")
			} else {
				cp.Emit("clicked")
				c.ok(w.copied == "journalctl -b -u bluetooth.service", "copied %q", w.copied)
				c.ok(cp.TooltipText() == "journalctl -b -u bluetooth.service", "the button's tooltip is %q, not the command", cp.TooltipText())
				c.ok(cp.TooltipText() == "journalctl -b -u bluetooth.service", "the button's tooltip is %q, not the command", cp.TooltipText())
				c.ok(slices.Contains(w.toasted, "Copied “journalctl -b -u bluetooth.service”. Paste it in a terminal."), "no Copied toast: %q", w.toasted)
			}
			// Read: the news link, with the launcher; none for a link that isn't archlinux.org.
			rd := button("news", "Read on archlinux.org")
			if rd == nil {
				c.fail("no Read on the news row")
			} else {
				rd.Emit("clicked")
				c.ok(slices.Contains(rec.Calls(), "uri https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"), "launched %q", rec.Calls())
			}
			c.ok(button("disk", "Copy command") == nil && button("disk", "Open") == nil, "a row without an action has a button")
			// Check again: a spinner while it runs, then the new answer.
			f.set(calmEvil)
			again := button2(d, "Check again")
			c.ok(again != nil, "no Check again")
			if again != nil {
				again.Emit("clicked")
				c.ok(again.Label() == "Checking…" && !again.Sensitive(), "while it runs the button says %q", again.Label())
			}
		},
		func() {
			// The answer comes by an idle callback, which a busy frame clock
			// (the sheet's spinner) can hold back: wait for it.
			waitFor(func() bool { return !w.health.run.Running() }, func() {
				c.ok(slices.Equal(rowNames(), []string{"mirror", "pacnew", "evil", "disk"}), "after Check again: %v", rowNames())
				c.ok(button("evil", "Open") == nil && button("evil", "Read on archlinux.org") == nil, "a link off archlinux.org got a button")
				again := button2(d, "Check again")
				c.ok(again != nil && again.Sensitive(), "Check again is not back")
				c.press(d, "close")
				checked = true
			})
		},
		func() {}, func() {}, func() {}, func() {}, func() {}, func() {}, func() {},
		func() {
			c.ok(checked && w.health.sheet == nil, "the sheet did not close, or the last checks never ran (%v)", checked)
			if w.health.sheet != nil {
				w.health.sheet.ForceClose() // else win.Close closes the dialog, not the window
			}
		},
	)
}

// button2 is the button labelled label anywhere under the dialog.
func button2(d *amDialog, label string) *gtk.Button {
	var found *gtk.Button
	eachWidget(&d.Widget, func(wd gtk.Widgetter) {
		if b, ok := wd.(*gtk.Button); ok && found == nil && (b.Label() == label || (label == "Check again" && b.HasCSSClass("am-health-again"))) {
			found = b
		}
	})
	return found
}

func covHealthNews(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	glassSetup(w)
	item := func(title string, ago time.Duration) healthview.NewsItem {
		return healthview.NewsItem{Title: title, Link: "https://archlinux.org/news/x/", Date: time.Now().Add(-ago)}
	}
	gate := func(id engine.PromptID, action, heading string) {
		w.current = action
		c.feed(engine.NeedsConfirm{ID: id, Title: heading, YesLabel: "Run",
			Plan: []engine.PlanStep{{ID: "audit", Title: "Auditing AUR packages…"}, {ID: "topgrade", Title: "Updating system with topgrade…", NeedsPriv: true}}})
	}
	callout := func(d *amDialog) bool {
		found := false
		eachWidget(&d.Widget, func(wd gtk.Widgetter) {
			if gtk.BaseWidget(wd).HasCSSClass("am-news") {
				found = true
			}
		})
		return found
	}
	var slowDone chan struct{}
	var gateAt time.Time
	c.seq(300,
		func() { // news: the callout at the top, the newest item, "and N more", the link
			f.unread = func(context.Context) (healthview.Unread, error) {
				return healthview.Unread{Items: []healthview.NewsItem{item("Manual intervention for pacman 7.0.0 and local repositories required", 48*time.Hour), item("Older post", 96*time.Hour)}}, nil
			}
			gate(1, actions.IDTopgrade, "Run topgrade (system update)?")
		},
		func() {
			d := w.dialogs[1]
			if d == nil {
				c.fail("no confirm with news")
				return
			}
			c.ok(callout(d), "no news callout")
			txt := dialogLabels(d, nil)
			for _, want := range []string{"NEWS", "Read before updating", "Manual intervention for pacman 7.0.0 and local repositories required", "and 1 more", "archlinux.org · posted 2 d ago · after your last update"} {
				c.ok(strings.Contains(txt, want), "callout lacks %q in %q", want, txt)
			}
			c.ok(button2(d, "Open on archlinux.org") != nil, "no Open on archlinux.org")
			shoot(w, "health", fmt.Sprintf("confirm-news-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv)))
			c.press(d, "no")
		},
		func() { // no unread items: no callout
			f.unread = func(context.Context) (healthview.Unread, error) { return healthview.Unread{}, nil }
			gate(2, actions.IDFullCleanup, "Full Cleanup?")
		},
		func() {
			d := w.dialogs[2]
			c.ok(d != nil && !callout(d), "an empty news list shows a callout, or no confirm: %v", d != nil)
			if d != nil {
				c.press(d, "no")
			}
		},
		func() { // an error: no callout either
			f.unread = func(context.Context) (healthview.Unread, error) { return healthview.Unread{}, context.DeadlineExceeded }
			gate(3, actions.IDTopgrade, "Run topgrade?")
		},
		func() {
			d := w.dialogs[3]
			c.ok(d != nil && !callout(d), "a failed news fetch shows a callout, or no confirm")
			if d != nil {
				c.press(d, "no")
			}
		},
		func() { // a slow feed: the confirm opens at the cutoff, without the callout
			slowDone = make(chan struct{})
			f.unread = func(ctx context.Context) (healthview.Unread, error) {
				<-slowDone // ignores its context: the window must not wait for it
				return healthview.Unread{Items: []healthview.NewsItem{item("late", time.Hour)}}, nil
			}
			gateAt = time.Now()
			gate(4, actions.IDTopgrade, "Run topgrade?")
			c.ok(w.dialogs[4] == nil, "the confirm opened before the news came or the cutoff passed")
		},
		func() {},
		func() {
			d := w.dialogs[4]
			c.ok(d != nil, "no confirm after the cutoff (%v)", time.Since(gateAt))
			c.ok(d != nil && !callout(d), "a late answer made a callout")
			c.ok(time.Since(gateAt) < 1500*time.Millisecond, "the confirm took %v", time.Since(gateAt))
			close(slowDone)
			if d != nil {
				c.press(d, "no")
			}
		},
		func() { // another action's confirm never asks for news
			_, _, before := f.counts()
			gate(5, actions.IDClearCaches, "Clear caches?")
			c.ok(w.dialogs[5] != nil, "another action's confirm waited for the news")
			_, _, after := f.counts()
			c.ok(before == after, "news asked for %d times for another action", after-before)
			if d := w.dialogs[5]; d != nil {
				c.press(d, "no")
			}
		},
	)
}

func covHealthRuns(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	var base int
	c.seq(250,
		func() {
			runs, _, _ := f.counts()
			c.ok(runs >= 1, "no check at start (%d runs)", runs)
		},
		func() {
			// Twenty requests at once: one runs, at most one more follows.
			base, _, _ = f.counts()
			for range 20 {
				w.refreshHealth()
			}
		},
		func() {},
		func() {
			runs, peak, _ := f.counts()
			c.ok(peak == 1, "%d checks ran at once", peak)
			c.ok(runs-base <= 2, "twenty requests made %d runs", runs-base)
		},
		func() { // the 30 minute timer (shortened to 700 ms)
			base, _, _ = f.counts()
		},
		func() {},
		func() {},
		func() {},
		func() {
			runs, _, _ := f.counts()
			c.ok(runs > base, "the timer did not run the checks (%d, %d)", base, runs)
			base = runs
			w.stack.SetVisibleChildName("logs")
		},
		func() { w.stack.SetVisibleChildName("overview") }, // older than healthStale: a run
		func() {},
		func() {
			runs, _, _ := f.counts()
			c.ok(runs > base, "showing the Overview with an old result did not run the checks (%d, %d)", base, runs)
			base = runs
			// A run that ends: it changed things, so the checks run again.
			c.feed(engine.Finished{Action: actions.IDTopgrade, Outcome: engine.OutcomeCompleted})
		},
		func() {},
		func() {
			runs, peak, _ := f.counts()
			c.ok(runs > base, "a finished run did not refresh the checks (%d, %d)", base, runs)
			c.ok(peak == 1, "%d checks ran at once", peak)
		},
	)
}

func covHealthTopgrade(c *covEnv) {
	w := c.w
	c.quiet()
	c.seq(250,
		func() {
			// Calm cache, no updates waiting: Run topgrade is still there.
			c.ok(w.upd.topg.IsVisible(), "Run topgrade is hidden while no updates wait")
			c.ok(w.upd.topg.Sensitive(), "Run topgrade is off while idle")
			w.busy = true
			w.renderUpdates()
		},
		func() {
			c.ok(w.upd.topg.IsVisible() && !w.upd.topg.Sensitive(), "during a run: visible %v sensitive %v, want shown and off", w.upd.topg.IsVisible(), w.upd.topg.Sensitive())
			w.busy = false
			w.renderUpdates()
		},
		func() { c.ok(w.upd.topg.IsVisible() && w.upd.topg.Sensitive(), "Run topgrade did not come back") },
	)
}

// TestGUIHealth runs the scenarios; the cell and the sheet in both looks,
// dark and light, with screenshots under ARCHMAID_GUI_SHOTS.
func TestGUIHealth(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	for _, sc := range []string{"cov-health-cell", "cov-health-sheet", "cov-health-news", "cov-health-scroll"} {
		for _, look := range []string{"default", "glass"} {
			for _, scheme := range []string{"light", "dark"} {
				dir := plain
				if look == "glass" {
					dir = cfg
				}
				child(t, sc, "XDG_CONFIG_HOME="+dir, glassSchemeEnv+"="+scheme, glassModeEnv+"="+look)
			}
		}
	}
	child(t, "cov-health-runs")
	child(t, "cov-health-reopen")
	child(t, "cov-health-topgrade")
}

// waitFor calls then once cond holds, polling for up to 8 s.
func waitFor(cond func() bool, then func()) {
	tries := 0
	coreglib.TimeoutAdd(100, func() bool {
		if tries++; cond() || tries > 80 {
			then()
			return false
		}
		return true
	})
}
