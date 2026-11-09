package ui

// Task #252: the Overview's Updates card on a display. A scratch state dir
// holds updates-cache.json; Options.RefreshUpdates is a fake (the real one
// runs systemctl, which testenv traps) that rewrites the file after a delay,
// stays silent or fails. The window's poll and timeout are shortened.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/actions"
)

type updFake struct {
	mu    sync.Mutex
	dir   string
	mode  string // "write", "silent" or "fail"
	calls int
	count int // what "write" puts in the cache
	wg    sync.WaitGroup
}

func (f *updFake) refresh(context.Context) error {
	f.mu.Lock()
	f.calls++
	mode, count := f.mode, f.count
	f.mu.Unlock()
	switch mode {
	case "fail":
		return errors.New("driver: updates check not started: systemctl: exit status 1 (Failed to connect to bus); no /usr/lib/archmaid/archmaid-updates-check")
	case "write":
		f.wg.Add(1)
		time.AfterFunc(400*time.Millisecond, func() {
			defer f.wg.Done()
			f.writeCache(count, time.Now())
		})
	}
	return nil
}

func (f *updFake) setMode(m string, count int) {
	f.mu.Lock()
	f.mode, f.count = m, count
	f.mu.Unlock()
}

func (f *updFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *updFake) writeCache(count int, at time.Time) {
	b := fmt.Sprintf(`{"v":3,"count":%d,"repo":%d,"aur":null,"aur_held":1,"checked":%q,"packages":[]}`, count, count, at.Format(time.RFC3339))
	p := filepath.Join(f.dir, "updates-cache.json")
	tmp := p + ".tmp"
	if os.WriteFile(tmp, []byte(b), 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}

func newUpdFake(mode string) *updFake {
	dir, err := os.MkdirTemp("", "archmaid-upd-")
	if err != nil {
		panic(err)
	}
	return &updFake{dir: dir, mode: mode}
}

var updEnv *updFake

func init() {
	setup := func(o *Options, _ *report) {
		updEnv = newUpdFake("write")
		updEnv.writeCache(3, time.Now().Add(-20*time.Minute))
		o.StateDir = updEnv.dir
		o.RefreshUpdates = updEnv.refresh
	}
	// The narrow one runs at 380 px (harness_test.go sets the size).
	covOptions["cov-updates"], covOptions["cov-updates-narrow"] = setup, setup
	covScenarios["cov-updates"], covScenarios["cov-updates-narrow"] = covUpdates, covUpdates
	covOptions["cov-updates-nocall"] = func(o *Options, _ *report) {
		updEnv = newUpdFake("")
		o.StateDir = updEnv.dir
	}
	covScenarios["cov-updates-nocall"] = covUpdatesNoCall
}

func TestGUICovUpdates(t *testing.T) { covChild(t, "cov-updates") }

func TestGUICovUpdatesNarrow(t *testing.T) { covChild(t, "cov-updates-narrow") }

func TestGUICovUpdatesNoCall(t *testing.T) { covChild(t, "cov-updates-nocall") }

// waitSeq runs the steps in order. A step returns true when done; false
// tries it again 100 ms later, up to limit tries, then fails the scenario.
func (c *covEnv) waitSeq(limit int, steps ...func() bool) {
	var run func(i, try int)
	run = func(i, try int) {
		if i == len(steps) {
			c.finish()
			return
		}
		coreglib.TimeoutAdd(100, func() {
			if steps[i]() {
				run(i+1, 0)
				return
			}
			if try+1 >= limit {
				c.fail("step %d never came true", i)
				c.finish()
				return
			}
			run(i, try+1)
		})
	}
	run(0, 0)
}

// covUpdates: waiting -> click -> Checking… -> the new count; the silent
// check ends as "Check failed"; a start failure says why; Run topgrade
// starts topgrade, and a run disables both buttons.
func covUpdates(c *covEnv) {
	w, f := c.w, updEnv
	c.quiet()
	w.updTimeout, w.updPoll = 2*time.Second, 100
	u := w.upd
	sm := adw.StyleManagerGetDefault()
	click := func(b *gtk.Button) { b.Emit("clicked") }
	titleIs := func(want string) func() bool { return func() bool { return u.title.Text() == want } }
	c.waitSeq(80,
		func() bool { w.pickPage("overview"); return true },
		func() bool {
			c.ok(u.title.Text() == "3 updates waiting", "title %q", u.title.Text())
			c.ok(strings.Contains(u.detail.Text(), "1 held") && strings.Contains(u.detail.Text(), "checked 20 min ago"), "detail %q", u.detail.Text())
			c.ok(u.tag.Text() == "UPDATES", "tag %q", u.tag.Text())
			c.ok(u.topg.IsVisible() && u.topg.Sensitive(), "Run topgrade not offered with updates waiting")
			c.ok(u.check.Sensitive() && !u.spin.IsVisible(), "check button/spinner idle state")
			if c.rep.Scenario == "cov-updates-narrow" {
				c.ok(w.narrow, "the window is not narrow")
				minW, _, _, _ := gtk.BaseWidget(w.stack.VisibleChild()).Measure(gtk.OrientationHorizontal, -1)
				c.ok(minW <= 380, "the Overview's minimum width is %d, window 380", minW)
				c.ok(w.win.Width() <= 382, "window %d wide", w.win.Width())
			}
			sm.SetColorScheme(adw.ColorSchemeForceLight)
			return true
		},
		func() bool {
			c.ok(!sm.Dark(), "not light")
			shoot(w, c.rep.Scenario, "waiting-light")
			sm.SetColorScheme(adw.ColorSchemeForceDark)
			return true
		},
		func() bool {
			c.ok(sm.Dark(), "not dark")
			shoot(w, c.rep.Scenario, "waiting-dark")
			sm.SetColorScheme(adw.ColorSchemeDefault)
			return true
		},
		func() bool {
			// Click: Checking…, spinner, button off, no topgrade; the fake
			// rewrites the cache 0.4 s later with nothing waiting.
			f.setMode("write", 0)
			click(u.check)
			c.ok(u.title.Text() == "Checking…", "title after click %q", u.title.Text())
			c.ok(u.spin.IsVisible(), "no spinner while checking")
			c.ok(!u.check.Sensitive(), "check button still enabled while checking")
			c.ok(u.topg.IsVisible(), "Run topgrade hidden while checking: it is always there (#270)")
			c.ok(u.detail.Text() == "1 held · checked 20 min ago", "detail while checking %q", u.detail.Text())
			shoot(w, c.rep.Scenario, "checking")
			return true
		},
		titleIs("Up to date"),
		func() bool {
			c.ok(f.callCount() == 1, "RefreshUpdates called %d times", f.callCount())
			c.ok(u.tag.Text() == "UP TO DATE" && !u.spin.IsVisible(), "tag %q / spinner", u.tag.Text())
			c.ok(u.check.Sensitive(), "check button not back")
			c.ok(u.topg.IsVisible(), "Run topgrade hidden with nothing to update: it is always there (#270)")
			c.ok(strings.HasPrefix(u.detail.Text(), "1 held · checked just now"), "detail %q", u.detail.Text())
			shoot(w, c.rep.Scenario, "uptodate")
			// The silent check: nothing changes the file.
			f.setMode("silent", 0)
			click(u.check)
			c.ok(u.title.Text() == "Checking…", "title %q", u.title.Text())
			return true
		},
		titleIs("Check failed"),
		func() bool {
			c.ok(u.detail.Text() == "No new result after 2 seconds", "detail %q", u.detail.Text())
			c.ok(u.tag.Text() == "CHECK FAILED" && !u.spin.IsVisible() && u.check.Sensitive(), "failed state: tag %q", u.tag.Text())
			shoot(w, c.rep.Scenario, "failed")
			// The check could not be started.
			f.setMode("fail", 0)
			click(u.check)
			return true
		},
		func() bool {
			return strings.HasPrefix(u.detail.Text(), "Could not start the check: driver: updates check not started")
		},
		func() bool {
			c.ok(u.title.Text() == "Check failed" && !strings.Contains(u.detail.Text(), "\n") && u.check.Sensitive(), "start failure %q / %q", u.title.Text(), u.detail.Text())
			c.ok(f.callCount() == 3, "calls %d", f.callCount())
			// A newer cache, and the page shown again: the card follows it.
			f.writeCache(4, time.Now())
			w.pickPage("settings")
			w.pickPage("overview")
			return true
		},
		titleIs("4 updates waiting"),
		func() bool {
			c.ok(u.topg.IsVisible() && u.check.Sensitive(), "buttons")
			click(u.topg)
			c.ok(w.busy && w.current == actions.IDTopgrade, "Run topgrade did not start topgrade: busy %v current %q", w.busy, w.current)
			c.ok(!u.check.Sensitive() && !u.topg.Sensitive(), "buttons enabled while a run goes: check %v topgrade %v", u.check.Sensitive(), u.topg.Sensitive())
			n := f.callCount()
			click(u.check) // a click on a disabled button is a no-op; so is the method
			w.checkUpdates()
			c.ok(f.callCount() == n && u.title.Text() == "4 updates waiting", "a check started during a run")
			w.stop()
			return true
		},
		func() bool { return !w.busy },
		func() bool {
			c.ok(u.check.Sensitive() && u.topg.Sensitive(), "buttons stayed disabled after the run")
			f.wg.Wait()
			_ = os.RemoveAll(f.dir)
			return true
		},
	)
}

// covUpdatesNoCall: without RefreshUpdates (the stub) the button is off.
func covUpdatesNoCall(c *covEnv) {
	c.quiet()
	c.seq(200, func() {
		u := c.w.upd
		c.ok(u.title.Text() == "Not checked yet" && u.tag.Text() == "NOT CHECKED", "title %q tag %q", u.title.Text(), u.tag.Text())
		c.ok(!u.check.Sensitive() && u.topg.IsVisible(), "check %v topgrade shown %v", u.check.Sensitive(), u.topg.IsVisible())
	}, func() { _ = os.RemoveAll(updEnv.dir) })
}
