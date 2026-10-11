package ui

// More cover scenarios (see cover_harness_test.go): the Run page's
// timeline, the Logs page, the Settings page and the screenshot failure.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/logview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/frontend"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ---- timeline ----

func init() { covScenarios["cov-timeline"] = covTimeline }

// covTimeline: the timeline through its own widgets. A click on a step's
// header folds and opens it; lines outside any step get a section whose
// header hides itself (no row); a failed step of a finished run offers
// "Open cleanup.log" when its plan named no category; scrolling up, or Up,
// Page Up and Home, stop following the newest line and other keys and
// scrolling down don't.
func covTimeline(c *covEnv) {
	w := c.w
	c.quiet()
	w.current = actions.IDFullCleanup
	c.feed(
		engine.QueueStarted{Action: actions.IDFullCleanup, Started: time.Now(), Steps: []engine.PlanStep{
			{ID: "one", Title: "First step"}, {ID: "two", Title: "Second step"}}},
		engine.StepStarted{ID: "one"},
		engine.OutputChunk{ID: "one", Lines: []string{"a", "b", "c"}},
		engine.OutputChunk{ID: "", Lines: []string{"outside any step"}},
		engine.StepDone{ID: "one", Status: steps.Failed, Err: "boom"},
		engine.StepStarted{ID: "two"},
		engine.OutputChunk{ID: "two", Lines: []string{"d"}},
		engine.StepDone{ID: "two", Status: steps.OK},
		engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted, Errs: 1},
	)
	sectionOf := func(step string) *section {
		for _, s := range w.tl.sections {
			if s.step == step {
				return s
			}
		}
		c.fail("no section for %q (have %d)", step, len(w.tl.sections))
		return &section{}
	}
	c.seq(300,
		func() {
			stray := sectionOf("")
			c.ok(stray.header != nil, "the lines outside any step have no header drawn")
			if stray.header != nil {
				c.ok(!stray.header.box.Visible(), "the header of lines outside any step is shown")
			}
			one := sectionOf("one")
			c.ok(one.header != nil, "step one has no header drawn")
			if one.header != nil {
				c.ok(one.header.acts.box.Visible(), "a failed step of a finished run offers no actions")
				c.ok(one.header.acts.log.Label() == "Open cleanup.log", "log button %q, want Open cleanup.log", one.header.acts.log.Label())
			}
			c.ok(one.lines && one.items == 3, "the failed step shows %d items (lines %v), want its 3 lines", one.items, one.lines)
			// Click the header's line.
			click := func(s *section) {
				row := gtk.BaseWidget(s.header.box).FirstChild()
				n := 0
				for _, o := range controllersOf(row) {
					if g, ok := o.Cast().(*gtk.GestureClick); ok {
						g.Emit("released", 1, 5.0, 5.0)
						n++
					}
				}
				c.ok(n == 1, "the header line has %d click controllers", n)
			}
			if one.header != nil {
				click(one)
				c.ok(!one.lines && one.userSet && !one.userOpen, "a click on an open step's header didn't fold it (lines %v)", one.lines)
				click(one)
				c.ok(one.lines && one.userOpen && one.items == 3, "a second click didn't open it (lines %v items %d)", one.lines, one.items)
			}
		},
		func() {
			scrolls, keys := 0, 0
			// The scrolled window has a scroll controller of its own; the
			// page's is among them.
			wheel := func(dy float64) {
				for _, o := range controllersOf(w.tl.scroll) {
					if sc, ok := o.Cast().(*gtk.EventControllerScroll); ok {
						sc.Emit("scroll", 0.0, dy)
						scrolls++
					}
				}
			}
			w.follow = true
			wheel(1)
			c.ok(w.follow, "scrolling down stopped following")
			wheel(-1)
			c.ok(!w.follow, "scrolling up kept following")
			for _, o := range controllersOf(w.tl.view) {
				if k, ok := o.Cast().(*gtk.EventControllerKey); ok {
					keys++
					for key, stops := range map[uint]bool{gdk.KEY_Up: true, gdk.KEY_Page_Up: true, gdk.KEY_Home: true, gdk.KEY_KP_Up: true, gdk.KEY_a: false, gdk.KEY_Down: false} {
						w.follow = true
						k.Emit("key-pressed", key, uint(0), gdk.ModifierType(0))
						c.ok(w.follow != stops, "key %d: following %v, want %v", key, w.follow, !stops)
					}
				}
			}
			c.ok(scrolls > 0 && keys > 0, "controllers found: %d scroll, %d key", scrolls, keys)
		},
	)
}

// ---- logs ----

func init() {
	covScenarios["cov-logs"] = covLogs
	covScenarios["cov-logs-unreadable"] = covLogsUnreadable
}

// covLogsFixture writes a state dir: B (newest) with a cleanup log, A with
// topgrade (in sections), audit and orphans logs, C with only an empty log,
// and last-run.json pointing at A.
func covLogsFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	now := time.Now().Truncate(time.Minute)
	at := func(ago time.Duration) *logstore.Store {
		when := now.Add(-ago)
		return logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return when }})
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	body := "―― 00:45:36 - System update ――\n:: Synchronizing package databases...\n" +
		"―― 00:45:44 - Flatpak User Packages ――\nNothing to update.\n"
	a := at(3 * time.Hour)
	must(a.Append(logstore.Topgrade, "OK", "Topgrade completed", body))
	must(a.Append(logstore.Audit, "OK", "AUR audit", "3 foreign packages checked\n"))
	must(a.Append(logstore.Orphans, "OK", "Orphans", "none\n"))
	must(at(time.Hour).Append(logstore.Cleanup, "OK", "Clearing cache", "done\n"))
	c := at(5 * time.Hour)
	must(os.MkdirAll(c.RunDir(), 0o700))
	must(os.WriteFile(c.Path(logstore.Cleanup), nil, 0o600))
	when := now.Add(-3 * time.Hour)
	must(a.WriteLastRun(logstore.RunRecord{Action: actions.IDTopgrade, Outcome: logstore.OutcomeOK, Started: when, Finished: when.Add(time.Minute)}))
	return dir
}

// covLogs: the Logs page through its widgets' signals.
func covLogs(c *covEnv) {
	w := c.w
	c.quiet()
	w.showLogs()
	p := w.logs
	if !(len(p.runList) == 3) {
		c.fail("runs %d, want 3 (newest first: B, A, C)", len(p.runList))
		c.finish()
		return
	}
	runB, runA, runC := p.runList[0], p.runList[1], p.runList[2]
	shown := func() string { return strings.Join(logLines(w), "|") }
	c.seq(100,
		func() { // a click on a run in the sidebar shows it, and the log pane
			p.runs.Emit("row-activated", p.runs.RowAtIndex(1))
			c.ok(p.run != nil && p.run.Name == runA.Name, "activating the second row showed %v, want %s", p.run, runA.Name)
			c.ok(p.split.ShowContent(), "activating a run left the sidebar in front")
			p.back.Emit("clicked")
			c.ok(!p.split.ShowContent(), "Back left the log in front")
		},
		func() { // another category of the run, from its toggle
			var other logstore.Category
			for _, cat := range p.run.Categories {
				if cat != p.cat {
					other = cat
				}
			}
			c.ok(other != "", "run A has one category: %v", p.run.Categories)
			p.cats.SetActiveName(string(other))
			c.ok(p.shown == runA.Name+"/"+string(other), "after the toggle the log is %q, want %s/%s", p.shown, runA.Name, other)
		},
		func() { // Open folder fails: a toast, and the folder was asked for
			fl := &failLauncher{err: errors.New("no file manager")}
			w.launcher = fl
			n := len(w.toasted)
			p.folder.Emit("clicked")
			c.ok(len(w.toasted) == n+1 && w.toasted[n] == "Couldn't open the folder: no file manager", "toasts %q", w.toasted[n:])
			c.ok(strings.Join(fl.Calls(), ",") == "folder "+runA.Dir(), "launched %q, want the run's folder", fl.Calls())
		},
		func() { // the back button follows the split view collapsing
			p.split.SetCollapsed(true)
			c.ok(p.back.Visible(), "Back is hidden in the collapsed form")
			p.split.SetCollapsed(false)
			c.ok(!p.back.Visible(), "Back is shown in the wide form")
		},
		func() { // the search button opens the bar; closing it clears the text
			btn, _ := findTooltip(p.split, "Search this log").(*gtk.ToggleButton)
			if btn == nil {
				c.fail("no search button")
				return
			}
			btn.SetActive(true)
			c.ok(p.bar.SearchMode(), "the search button didn't open the bar")
			p.search.SetText("sync")
			p.bar.SetSearchMode(false)
			c.ok(p.search.Text() == "" && !btn.Active(), "closing the bar left text %q, button active %v", p.search.Text(), btn.Active())
		},
		func() { // a section header folds and unfolds its section from the list
			w.openLog(runA, logstore.Topgrade)
			var lv *gtk.ListView
			eachWidget(p.split, func(x gtk.Widgetter) {
				if v, ok := x.(*gtk.ListView); ok && lv == nil {
					lv = v
				}
			})
			sec := -1
			for i, r := range p.rows {
				if r.Kind == logview.RowSection && sec < 0 {
					sec = i
				}
			}
			if lv == nil || sec < 0 {
				c.fail("list view %v, section row %d", lv != nil, sec)
				return
			}
			open := len(logLines(w))
			lv.Emit("activate", uint(sec))
			c.ok(len(logLines(w)) < open, "activating a section header left %d rows of %d", len(logLines(w)), open)
			lv.Emit("activate", uint(sec))
			c.ok(len(logLines(w)) == open, "activating it again left %d rows of %d", len(logLines(w)), open)
			// Nothing to fold at these: out of range, an entry row, while searching.
			for _, i := range []int{-1, len(p.rows), len(p.rows) + 5, 0} {
				w.toggleSection(i)
			}
			c.ok(len(logLines(w)) == open, "toggling a row that isn't a section changed the list")
			w.openLog(runA, logstore.Topgrade)
			p.bar.SetSearchMode(true)
			p.search.SetText("system")
			for i := range p.rows {
				w.toggleSection(i)
			}
			c.ok(len(p.collapsed) == 0, "a section was folded while searching: %v", p.collapsed)
			p.bar.SetSearchMode(false)
		},
		func() { // Open in Logs for a run that is not the one shown
			w.showRun(runB, "")
			c.ok(p.runs.SelectedRow().Index() == 0, "run B isn't the selected row")
			w.openLastRunLogs()
			c.ok(p.run != nil && p.run.Name == runA.Name, "Open in Logs showed %v, want %s", p.run, runA.Name)
			c.ok(p.runs.SelectedRow().Index() == 1, "the selected row is %d, want run A's (1)", p.runs.SelectedRow().Index())
			c.ok(w.stack.VisibleChildName() == "logs", "page %q", w.stack.VisibleChildName())
		},
		func() { // a run with no logs says so
			w.showRun(runC, "")
			c.ok(strings.HasSuffix(p.title.Text(), " · no logs") && p.shown == runC.Name+"/", "title %q shown %q", p.title.Text(), p.shown)
			c.ok(len(logLines(w)) == 0, "run C lists %q", logLines(w))
		},
		func() { // a log that is gone, and one that can't be read
			w.showRun(runA, "")
			if err := os.Remove(filepath.Join(runA.Dir(), "audit.log")); err != nil {
				c.fail("%v", err)
			}
			w.openLog(runA, logstore.Audit)
			c.ok(shown() == "(empty)", "a log removed since the listing shows %q, want (empty)", shown())
			if err := os.Remove(filepath.Join(runA.Dir(), "orphans.log")); err != nil {
				c.fail("%v", err)
			}
			if err := os.Mkdir(filepath.Join(runA.Dir(), "orphans.log"), 0o700); err != nil {
				c.fail("%v", err)
			}
			w.openLog(runA, logstore.Orphans)
			c.ok(strings.HasPrefix(shown(), "Can't read this log: "), "an unreadable log shows %q", shown())
		},
	)
}

// covLogsUnreadable: a state dir that is not a directory.
func covLogsUnreadable(c *covEnv) {
	w := c.w
	c.quiet()
	w.showLogs()
	p := w.logs
	text := strings.Join(labelsUnder(&p.runs.Widget), "|")
	c.ok(strings.Contains(text, "The logs can't be read"), "the sidebar shows %q", text)
	c.ok(p.run == nil && p.title.Text() == "No logs yet" && len(p.runList) == 0, "run %v title %q", p.run, p.title.Text())
	c.finish()
}

// ---- settings ----

func init() {
	covScenarios["cov-settings"] = covSettings
	covScenarios["cov-nohome"] = covNoHome
}

// covSettings: HOME/.config and $XDG_CONFIG_HOME/archmaid are files, so the
// settings can't be read or written and the topgrade config can't be made;
// then the config folder is cleared and the editor can't be opened.
func covSettings(c *covEnv) {
	w := c.w
	c.quiet()
	p := w.set
	home := os.Getenv("HOME")
	c.ok(p.problem.IsVisible() && strings.Contains(p.problem.Text(), "can't be read"), "problem %q visible %v, want the unreadable settings said", p.problem.Text(), p.problem.IsVisible())

	p.editor.Emit("clicked")
	c.ok(strings.HasPrefix(p.configError.Text(), "Couldn't create it: "), "config error %q", p.configError.Text())

	if err := os.Remove(filepath.Join(home, ".config")); err != nil {
		c.fail("%v", err)
	}
	fl := &failLauncher{err: errors.New("no default app")}
	w.launcher = fl
	conf := filepath.Join(home, ".config", "topgrade.toml")
	p.editor.Emit("clicked")
	c.ok(p.configError.Text() == "Couldn't open it: no default app", "config error %q", p.configError.Text())
	b, err := os.ReadFile(conf)
	c.ok(err == nil && string(b) == frontend.DefaultTopgradeConfig, "config %q %v, want the defaults", b, err)
	c.ok(strings.Join(fl.Calls(), ",") == "file "+conf, "launched %q", fl.Calls())

	// A folder typed with spaces around it is kept trimmed, and applies
	// although the file can't be written.
	dl := os.Getenv("ARCHMAID_GUI_DL")
	p.downloads.SetText("  " + dl + "  ")
	w.applyDownloads()
	c.ok(p.downloads.Text() == dl, "the folder row holds %q, want it trimmed to %q", p.downloads.Text(), dl)
	c.ok(w.settings.Downloads == dl, "settings keep %q", w.settings.Downloads)
	c.ok(p.problem.IsVisible() && strings.HasPrefix(p.problem.Text(), "Not saved (it applies until you close archmaid): "), "problem %q", p.problem.Text())
	c.ok(!p.dlError.IsVisible(), "the folder error is shown: %q", p.dlError.Text())
	c.finish()
}

// covNoHome: without HOME there is no topgrade config to open.
func covNoHome(c *covEnv) {
	w := c.w
	c.quiet()
	p := w.set
	c.ok(!p.editor.Sensitive(), "Open in editor is usable without a home folder")
	c.ok(p.configError.IsVisible() && strings.Contains(p.configError.Text(), frontend.ErrNoHome.Error()), "config error %q visible %v", p.configError.Text(), p.configError.IsVisible())
	c.ok(!p.problem.IsVisible(), "the settings file is said unusable: %q", p.problem.Text())
	c.finish()
}

// ---- screenshots ----

func init() { covScenarios["cov-savepng"] = covSavePNG }

// covSavePNG: a screenshot of a widget that isn't on screen fails, and says
// which step did.
func covSavePNG(c *covEnv) {
	label := gtk.NewLabel("not shown")
	err := savePNG(label, filepath.Join(os.Getenv("HOME"), "never.png"))
	c.ok(err != nil, "a screenshot of an unrealized widget worked")
	if err != nil && shotsBuilt {
		c.ok(strings.Contains(err.Error(), "step 1 failed"), "error %q, want step 1 named", err)
	}
	if _, serr := os.Stat(filepath.Join(os.Getenv("HOME"), "never.png")); serr == nil {
		c.fail("a failed screenshot left a file")
	}
	c.finish()
}
