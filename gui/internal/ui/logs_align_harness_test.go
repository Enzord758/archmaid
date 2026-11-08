package ui

// The Logs page lines up with itself (#262), in both looks, dark and light,
// wide and narrow: the run list's rows against the log pane's header, the
// log's header rows against the log text, and the pane edges.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

func init() { covScenarios["cov-logs-align"] = covLogsAlign }

func covLogsAlign(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	w.showLogs()
	p := w.logs
	var st []func()
	for _, look := range []bool{false, true} {
		for _, wd := range []int{1920, 1440, 1280, 900, 700, 500} {
			st = append(st,
				func() { w.win.SetDefaultSize(wd, 800); w.set.glass.SetActive(look); p.split.SetShowContent(true) },
				func() {},
				func() {},
				func() {
					where := fmt.Sprintf("glass %v at %d px (collapsed %v)", look, wd, p.split.Collapsed())
					shoot(w, "logs", fmt.Sprintf("glass-%v-%s-%d", look, os.Getenv(glassSchemeEnv), wd))
					logsGeometry(c, w, where)
				})
		}
	}
	c.seq(150, st...)
}

// logsGeometry checks the page's alignments: the log's text starts where
// its header's title starts and its aside ends where the header's buttons
// end.
func logsGeometry(c *covEnv, w *Window, where string) {
	p := w.logs
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.5 }
	var scroll gtk.Widgetter
	eachWidget(p.split, func(wd gtk.Widgetter) {
		if scroll == nil && gtk.BaseWidget(wd).HasCSSClass(textSurfClass) {
			scroll = wd
		}
	})
	c.ok(scroll != nil, "%s: no log pane", where)
	if scroll == nil {
		return
	}
	var text, aside *gtk.Label
	eachWidget(scroll, func(wd gtk.Widgetter) {
		l, ok := wd.(*gtk.Label)
		if !ok || !l.IsVisible() || l.Text() == "" {
			return
		}
		if l.XAlign() == 1 {
			if aside == nil {
				aside = l
			}
		} else if text == nil {
			text = l
		}
	})
	if text == nil || aside == nil {
		c.fail("%s: the log shows no text or aside (%v %v)", where, text, aside)
		return
	}
	// The header starts at its Back button when the runs are a page of
	// their own (narrow), else at its title.
	tx, _, _, _, _ := alignBounds(w, p.title)
	if p.split.Collapsed() {
		tx, _, _, _, _ = alignBounds(w, p.back)
	}
	lx, _, _, _, _ := alignBounds(w, text)
	_, _, ar, _, _ := alignBounds(w, aside)
	_, _, fr, _, _ := alignBounds(w, p.folder)
	c.ok(near(lx, tx), "%s: the log's text starts at %.1f, the header's title at %.1f", where, lx, tx)
	c.ok(near(ar, fr), "%s: the log's aside ends at %.1f, the header's last button at %.1f", where, ar, fr)
}

// TestGUILogsAlign: see covLogsAlign.
func TestGUILogsAlign(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"dark", "light"} {
		child(t, "cov-logs-align", "XDG_CONFIG_HOME="+cfg, "ARCHMAID_GUI_STATE="+covLogsFixture(t), glassSchemeEnv+"="+s)
	}
}
