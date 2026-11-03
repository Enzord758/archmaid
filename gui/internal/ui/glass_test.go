package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

// TestGlassModeFor: the skin the settings ask for. Off is the default look
// whatever Blur says; with glass on, Auto is translucent only where
// Hyprland is (its session variable, faked), On and Off force either way.
func TestGlassModeFor(t *testing.T) {
	hypr := func(k string) string {
		if k == settings.HyprlandEnv {
			return "sig"
		}
		return ""
	}
	none := func(string) string { return "" }
	for _, c := range []struct {
		s    settings.Settings
		env  func(string) string
		want glassMode
	}{
		{settings.Settings{}, hypr, glassOff},
		{settings.Settings{Blur: settings.BlurOn}, hypr, glassOff},
		{settings.Settings{Glass: true}, hypr, glassBlur},
		{settings.Settings{Glass: true}, none, glassFallback},
		{settings.Settings{Glass: true, Blur: settings.BlurAuto}, none, glassFallback},
		{settings.Settings{Glass: true, Blur: settings.BlurOn}, none, glassBlur},
		{settings.Settings{Glass: true, Blur: settings.BlurOff}, hypr, glassFallback},
	} {
		if got := glassModeFor(c.s, c.env); got != c.want {
			t.Errorf("%+v, hyprland %v: mode %d, want %d", c.s, c.env(settings.HyprlandEnv) != "", got, c.want)
		}
	}
}

// TestContentBlurOK: the blur under a dialog needs the blur skin and a GPU
// renderer; cairo (archmaid-gui's default) and the fallback skip it.
func TestContentBlurOK(t *testing.T) {
	for _, c := range []struct {
		mode glassMode
		r    string
		want bool
	}{
		{glassBlur, "GskNglRenderer", true},
		{glassBlur, "GskVulkanRenderer", true},
		{glassBlur, "GskCairoRenderer", false},
		{glassBlur, "", false},
		{glassFallback, "GskNglRenderer", false},
		{glassOff, "GskNglRenderer", false},
	} {
		if got := contentBlurOK(c.mode, c.r); got != c.want {
			t.Errorf("mode %d on %q: %v, want %v", c.mode, c.r, got, c.want)
		}
	}
}

// TestGlassCSS reads glass.css: the numbers the owner chose, no corner
// marks, the content blur rule, and nothing that redefines a libadwaita or
// DMS colour name or reaches outside the .archmaid scope.
func TestGlassCSS(t *testing.T) {
	css := glassCSS
	// Window 60 % light, 76 % dark; sheet 72 % and 78 %; text surfaces 93 %;
	// the fallback's window 96 % and sidebar 90 %.
	for _, want := range []string{
		"am_glass_win alpha(@window_bg_color, 0.60)",
		"am_glass_win alpha(@window_bg_color, 0.76)",
		"am_glass_sheet alpha(@dialog_bg_color, 0.72)",
		"am_glass_sheet alpha(@dialog_bg_color, 0.78)",
		"am_glass_text alpha(@view_bg_color, 0.93)",
		"am_glass_win_fb alpha(@window_bg_color, 0.96)",
		"am_glass_side_fb alpha(shade(@window_bg_color, 0.93), 0.90)",
		"filter: blur(5px)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("glass.css lacks %q", want)
		}
	}
	if strings.Contains(css, "am_mark") {
		t.Error("glass.css draws the corner marks (@am_mark): the glass skin has none")
	}
	if !regexp.MustCompile(`\.am-blueprint \{\s*background-image: linear-gradient[^}]*background-size: auto;`).MatchString(css) {
		t.Error("glass.css doesn't replace the marks of .am-blueprint")
	}
	for _, name := range []string{"window_bg_color", "view_bg_color", "card_bg_color", "dialog_bg_color", "accent_bg_color", "window_fg_color", "sidebar_bg_color", "headerbar_bg_color"} {
		if regexp.MustCompile(`@define-color ` + name + `\b`).MatchString(css) {
			t.Errorf("glass.css redefines %s", name)
		}
	}
	// Every selector is scoped (comments and @-rules aside).
	bare := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	for _, line := range strings.Split(bare, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "@") || !(strings.HasSuffix(l, "{") || strings.HasSuffix(l, ",")) {
			continue
		}
		if !strings.HasPrefix(l, ".archmaid.am-glass") && !strings.HasPrefix(l, "window.archmaid.am-glass") {
			t.Errorf("an unscoped selector in glass.css: %q", l)
		}
	}
}
