package ui

// Settings (#266): it came back at the scroll offset it was left at, and in
// glass the header strip of a scrolled page was darker than the page (the
// toolbar view draws its own opaque bar over scrolled content).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

func init() { covScenarios["cov-settings-scroll"] = covSettingsScroll }

func covSettingsScroll(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	adj := w.set.scroll.VAdjustment()
	var rest int
	c.seq(250,
		func() { w.win.SetDefaultSize(960, 460); w.stack.SetVisibleChildName("settings") },
		func() {},
		func() {
			c.ok(adj.Upper()-adj.PageSize() > 100, "Settings does not scroll: upper %.0f page %.0f", adj.Upper(), adj.PageSize())
			if img, err := windowImage(w); err == nil {
				rest = alphaAt(img, w.win.Width()*3/10, 14)
			}
			adj.SetValue(120)
		},
		func() {},
		func() {
			// Scrolled: the header strip is as translucent as it was.
			if img, err := windowImage(w); err == nil {
				head := alphaAt(img, w.win.Width()*3/10, 14)
				c.ok(head <= rest+3, "scrolled, the header's alpha is %d, at rest %d (an opaque bar over the glass)", head, rest)
				// And no shade band under the bar: the margin beside the cards
				// (x 270) is as bright just under the header as lower down.
				top, low := lumaAt(img, 270, 90), lumaAt(img, 270, 160)
				c.ok(top-low <= 1 && low-top <= 1, "under the header the page is %d bright, lower down %d (a shade over the glass)", top, low)
				shoot(w, "settings-scroll", fmt.Sprintf("%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv)))
			}
			w.stack.SetVisibleChildName("overview")
		},
		func() { w.stack.SetVisibleChildName("settings") },
		func() {},
		func() { c.ok(adj.Value() == 0, "coming back to Settings starts at %.0f, want the top", adj.Value()) },
	)
}

// TestGUISettingsScroll: see covSettingsScroll.
func TestGUISettingsScroll(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"dark", "light"} {
		child(t, "cov-settings-scroll", "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"="+s, glassModeEnv+"=glass")
	}
}
