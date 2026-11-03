package ui

// The light theme's state colours (task #247): style_light.css gives the
// Tokyo Night state colours darker variants, loaded while the theme is
// light, so tag outlines and state text read at 4.5:1 (WCAG AA, small
// text) on every light surface. This reads them out of style.css, so it
// fails if someone lightens them again. The dark theme's values are the
// top-level definitions and are not tested here.

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

type rgb [3]float64

func parseHex(t *testing.T, h string) rgb {
	t.Helper()
	var c rgb
	for i := range c {
		v, err := strconv.ParseUint(h[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("bad colour %q: %v", h, err)
		}
		c[i] = float64(v)
	}
	return c
}

// mixRGB is a over b at alpha: a*alpha + b*(1-alpha), per channel.
func mixRGB(a, b rgb, alpha float64) rgb {
	var c rgb
	for i := range c {
		c[i] = math.Round(a[i]*alpha + b[i]*(1-alpha))
	}
	return c
}

// luminance is WCAG 2.x's relative luminance.
func luminance(c rgb) float64 {
	var l [3]float64
	for i, v := range c {
		v /= 255
		if v <= 0.03928 {
			l[i] = v / 12.92
		} else {
			l[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
}

func contrast(a, b rgb) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// lightColours reads the @define-color lines of style_light.css.
func lightColours(t *testing.T) map[string]string {
	t.Helper()
	css, err := os.ReadFile("style_light.css")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`@define-color (am_\w+) (#[0-9a-fA-F]{6});`).FindAllSubmatch(css, -1) {
		got[string(m[1])] = string(m[2])
	}
	return got
}

// TestLightStateColoursContrast: each light state colour is at least 4.5:1
// on the light surfaces it sits on (libadwaita's light window, card/sheet,
// sidebar, and the sidebar's selected row, a 12% black over it, which is
// darker than libadwaita's own) and, on all but the selected row (a sidebar
// row never carries a tag), on its own 14% tint over each of them.
func TestLightStateColoursContrast(t *testing.T) {
	cols := lightColours(t)
	sidebar := parseHex(t, "#ebebed")
	surfaces := map[string]rgb{
		"window":       parseHex(t, "#fafafb"),
		"card":         parseHex(t, "#ffffff"),
		"sheet":        parseHex(t, "#fafafb"),
		"sidebar":      sidebar,
		"selected row": mixRGB(rgb{0, 0, 0}, sidebar, 0.12),
	}
	for _, name := range []string{"am_ok", "am_warn", "am_danger", "am_running"} {
		hex, ok := cols[name]
		if !ok {
			t.Errorf("the light block does not define %s", name)
			continue
		}
		c := parseHex(t, hex)
		for sn, s := range surfaces {
			bgs := map[string]rgb{sn: s}
			if sn != "selected row" {
				bgs[sn+" + 14% tint"] = mixRGB(c, s, 0.14)
			}
			for what, bg := range bgs {
				r := contrast(c, bg)
				t.Logf("%s %s on %s: %.2f:1", name, hex, what, r)
				if r < 4.5 {
					t.Errorf("%s %s on %s: %.2f:1, want at least 4.5:1", name, hex, what, r)
				}
			}
		}
	}
}
