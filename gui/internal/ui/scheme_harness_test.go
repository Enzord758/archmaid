package ui

// A live light/dark switch (the KDE check, #255): the light theme's darker
// state colours must follow AdwStyleManager's dark flag as it flips, in
// both directions, for style.css and for the glass tokens.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

func init() { covScenarios["cov-scheme"] = covScheme }

// hexOf is a widget's resolved text colour as #rrggbb.
func hexOf(w gtk.Widgetter) string {
	c := colourOf(w)
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

// colourOf is a widget's resolved text colour as 0..255 components.
func colourOf(w gtk.Widgetter) [3]int {
	c := gtk.BaseWidget(w).Color()
	return [3]int{int(c.Red()*255 + 0.5), int(c.Green()*255 + 0.5), int(c.Blue()*255 + 0.5)}
}

// covScheme flips the colour scheme light, dark, light, dark and reads the
// colour the Last run's ok tag, the sudo tag and a state marker resolve to.
func covScheme(c *covEnv) {
	w := c.w
	c.quiet()
	sm := adw.StyleManagerGetDefault()
	probe := gtk.NewLabel("x")
	probe.AddCSSClass("am-state-ok")
	w.shell.side.Append(probe)
	okTag := gtk.NewLabel("ok")
	okTag.AddCSSClass("am-tag-ok")
	w.shell.side.Append(okTag)
	want := map[bool][3]string{
		false: {"#3d6114", "#75510c", "#3d6114"}, // light: ok tag, root tag, ok marker
		true:  {"#9ece6a", "#e0af68", "#9ece6a"},
	}
	check := func(where string, dark bool) {
		c.ok(sm.Dark() == dark, "%s: dark is %v, want %v", where, sm.Dark(), dark)
		root := gtk.NewLabel("root")
		root.AddCSSClass("am-tag-root")
		w.shell.side.Append(root)
		defer w.shell.side.Remove(root)
		got := [3]string{hexOf(okTag), hexOf(root), hexOf(probe)}
		c.ok(got == want[dark], "%s: ok tag, root tag, ok marker are %v, want %v", where, got, want[dark])
		if os.Getenv(glassModeEnv) == "glass" {
			c.ok(glassProvider != nil, "%s: glass is off", where)
			// The glass tokens follow too: the window is 0.60 opaque on a
			// light theme (153 of 255), 0.76 on a dark one (194).
			if img, err := windowImage(w); err == nil {
				head := alphaAt(img, w.win.Width()*3/10, 14)
				c.ok((head > 175) == dark, "%s: header alpha %d, dark %v", where, head, dark)
			}
		}
	}
	flip := func(s adw.ColorScheme) func() { return func() { sm.SetColorScheme(s) } }
	c.seq(300,
		flip(adw.ColorSchemeForceLight),
		func() { check("light", false) },
		flip(adw.ColorSchemeForceDark),
		func() { check("dark after light", true) },
		flip(adw.ColorSchemeForceLight),
		func() { check("light after dark", false) },
		flip(adw.ColorSchemeForceDark),
		func() { check("dark again", true) },
	)
}

// TestGUIColourSchemeLive: the state colours follow a live scheme switch,
// with the default look and with glass.
func TestGUIColourSchemeLive(t *testing.T) {
	needDisplay(t)
	for _, look := range []string{"default", "glass"} {
		cfg := t.TempDir()
		if look == "glass" {
			if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
				t.Fatal(err)
			}
		}
		child(t, "cov-scheme", "XDG_CONFIG_HOME="+cfg, glassModeEnv+"="+look)
	}
}
