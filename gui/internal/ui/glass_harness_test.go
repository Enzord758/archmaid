package ui

// The glass skin (task #254), on the headless display. TestGUIGlass drives
// the Settings switch and the blur row of a real window and checks the
// classes, the stylesheet, the content blur and, from screenshots, that the
// window really is translucent (no opaque libadwaita layer over it) and
// that the corner marks are gone. TestGUIGlassShots takes the four pages
// in the default look and in glass, light and dark.
//
// Broadway has no compositor blur: the translucency shows against
// Broadway's own background, which is enough for structure.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

const (
	glassSchemeEnv   = "ARCHMAID_GUI_GLASS_SCHEME"   // "dark": force the dark theme
	glassRendererEnv = "ARCHMAID_GUI_GLASS_RENDERER" // stands in for the window's renderer
	glassRegistryEnv = "ARCHMAID_GUI_GLASS_REGISTRY" // "has": the compositor advertises ext-background-effect
	glassModeEnv     = "ARCHMAID_GUI_GLASS_MODE"     // cov-glass-shots: "default" or "glass"
)

func init() {
	covScenarios["cov-glass"] = covGlass
	covScenarios["cov-glass-shots"] = covGlassShots
	covOptions["cov-glass-shots"] = func(o *Options, _ *report) {
		pkgFix = newPkgEnv()
		o.Packages = pkgFix.wiring()
	}
}

// glassSetup applies the scenario's scheme and renderer from the
// environment, which the parent test set.
func glassSetup(w *Window) {
	if os.Getenv(glassSchemeEnv) == "dark" {
		adw.StyleManagerGetDefault().SetColorScheme(adw.ColorSchemeForceDark)
	} else {
		adw.StyleManagerGetDefault().SetColorScheme(adw.ColorSchemeForceLight)
	}
	// The registry answer is faked in every scenario: Broadway has none.
	registryHas = func() (bool, bool) {
		if os.Getenv(glassRegistryEnv) == "has" {
			return true, true
		}
		return false, os.Getenv(glassRegistryEnv) == "lacks"
	}
	if r := os.Getenv(glassRendererEnv); r != "" {
		rendererName = func(*Window) string { return r }
	}
}

// windowImage is the window as GTK draws it now, alpha kept.
func windowImage(w *Window) (image.Image, error) {
	dir, err := os.MkdirTemp("", "glass")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "w.png")
	if err := savePNG(w.win, path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(b))
}

func alphaAt(img image.Image, x, y int) int {
	_, _, _, a := img.At(x, y).RGBA()
	return int(a >> 8)
}

func lumaAt(img image.Image, x, y int) int {
	r, g, b, _ := img.At(x, y).RGBA()
	return int((r*299 + g*587 + b*114) / 1000 >> 8)
}

// heroMarks says whether the Overview's hero draws the corner marks: its
// render node holds the marks' 10 by 1 px gradient strokes. ok is false
// when there is no hero or no render node.
func heroMarks(w *Window) (marks, ok bool) {
	eachWidget(w.win, func(wd gtk.Widgetter) {
		if b := gtk.BaseWidget(wd); !ok && b.HasCSSClass("am-hero") {
			text, err := nodeText(b)
			ok = err == nil
			marks = strokeRE.MatchString(text)
		}
	})
	return
}

var (
	sheetSolidRE = regexp.MustCompile(`rgba\([^)]*, ?0\.94\)`)
	sheetThinRE  = regexp.MustCompile(`rgba\([^)]*, ?0\.(72|78)\)`)
)

var strokeRE = regexp.MustCompile(`bounds: [-\d.]+ [-\d.]+ 10 1;`)

// covGlass: the Settings switch and the blur row, live.
func covGlass(c *covEnv) {
	w := c.w
	c.quiet()
	glassSetup(w)
	cairoShots := shotsBuilt
	blurSeen := false
	// What Auto should give: blur where the registry advertises the
	// protocol or Hyprland's variable is set.
	blurAuto := os.Getenv(settings.HyprlandEnv) != "" || os.Getenv(glassRegistryEnv) == "has"
	renderer := os.Getenv(glassRendererEnv)
	gpu := renderer != "" && renderer != "GskCairoRenderer"

	// look checks the window against the skin it should show: the classes,
	// the provider, and (with screenshots) the window's alpha at the header
	// and the sidebar and the corner marks of the hero.
	look := func(where string, mode glassMode) {
		c.ok(w.win.HasCSSClass(glassClass) == (mode != glassOff), "%s: window has %s: %v, want %v", where, glassClass, w.win.HasCSSClass(glassClass), mode != glassOff)
		c.ok(w.win.HasCSSClass(glassFBClass) == (mode == glassFallback), "%s: window has %s: %v, want %v", where, glassFBClass, w.win.HasCSSClass(glassFBClass), mode == glassFallback)
		c.ok((glassProvider != nil) == (mode != glassOff), "%s: glass.css loaded: %v, want %v", where, glassProvider != nil, mode != glassOff)
		styleMu.Lock()
		c.ok(len(styleErrors) == 0, "%s: stylesheet parse errors: %q", where, styleErrors)
		styleMu.Unlock()
		// GTK sends no blur region to the compositor unless the window has a
		// backdrop-filter: the blur skin has one (a blur node of radius 40,
		// twice the CSS's 20 px, at the top of the window's render node), the
		// fallback and the default look must not.
		if text, err := nodeText(w.win); err == nil {
			c.ok(strings.Contains(text, "blur: 40;") == (mode == glassBlur), "%s: the window's backdrop blur is %v, want %v", where, strings.Contains(text, "blur: 40;"), mode == glassBlur)
		}
		marks, ok := heroMarks(w)
		c.ok(ok, "%s: no hero on the Overview", where)
		c.ok(marks == (mode == glassOff), "%s: the hero's corner marks: %v, want %v (default look only)", where, marks, mode == glassOff)
		if !cairoShots {
			return
		}
		img, err := windowImage(w)
		if err != nil {
			c.fail("%s: %v", where, err)
			return
		}
		width := w.win.Width()
		head, side := alphaAt(img, width*3/10, 14), alphaAt(img, 20, 300)
		switch mode {
		case glassOff:
			c.ok(head == 255 && side == 255, "%s: default look is translucent: header alpha %d, sidebar %d", where, head, side)
		case glassBlur:
			// 0.60 light / 0.76 dark at the header; the sidebar's own layer on top.
			c.ok(head > 120 && head < 215, "%s: header alpha %d, want the window's 0.60..0.76 (an opaque layer over it?)", where, head)
			c.ok(side > head && side < 245, "%s: sidebar alpha %d (header %d)", where, side, head)
		case glassFallback:
			c.ok(head >= 240 && head < 255, "%s: fallback header alpha %d, want about 0.96", where, head)
			c.ok(side >= 240, "%s: fallback sidebar alpha %d, want near-opaque", where, side)
		}
	}
	// prompt opens a confirm (a wide dialog) and a password question (an
	// alert dialog) and checks their classes and the content blur.
	prompt := func(where string, mode glassMode) {
		blurred := func() bool { return w.shell.split.HasCSSClass(underClass) }
		c.feed(engine.NeedsConfirm{ID: 71, Title: "Go on?", Items: []string{"one.txt"}})
		d := w.dialogs[71]
		if d == nil {
			c.fail("%s: no dialog", where)
			return
		}
		c.ok(d.HasCSSClass(glassClass) == (mode != glassOff) && d.HasCSSClass(glassFBClass) == (mode == glassFallback),
			"%s: the wide dialog's classes %v: glass %v fb %v", where, d.CSSClasses(), mode != glassOff, mode == glassFallback)
		c.ok(d.HasCSSClass(scopeClass), "%s: the dialog lost %s", where, scopeClass)
		c.ok(blurred() == (mode == glassBlur && gpu), "%s: content blur %v, want %v (mode %d, renderer %q)", where, blurred(), mode == glassBlur && gpu, mode, renderer)
		c.ok(len(w.openDialogs) == 1, "%s: %d dialogs open", where, len(w.openDialogs))
	}
	// shut withdraws the dialog; before it, with screenshots, the page's
	// render node must hold GSK's blur exactly when the class says so: that
	// filter: blur() parses and draws on this GTK.
	shut := func() {
		if text, err := nodeText(w.win); err == nil {
			has := strings.Contains(text, "blur: 10;")
			c.ok(has == w.shell.split.HasCSSClass(underClass), "the page's render node has a blur: %v, but the class is %v", has, w.shell.split.HasCSSClass(underClass))
			if has {
				blurSeen = true
			}
			// The sheet's resolved alpha: near-opaque (0.94) unless the page
			// under it is blurred, then 0.72 light / 0.78 dark.
			if glassNow != glassOff {
				solid := sheetSolidRE.MatchString(text)
				thin := sheetThinRE.MatchString(text)
				blurPath := glassNow == glassBlur && gpu
				c.ok(solid == !blurPath && thin == blurPath, "the sheet's alpha: 0.94 %v, 0.72/0.78 %v; blur path %v", solid, thin, blurPath)
			}
		}
		c.feed(engine.PromptClosed{ID: 71}) // the engine withdraws it
	}
	closed := func(where string) {
		c.ok(len(w.openDialogs) == 0, "%s: %d dialogs still counted open", where, len(w.openDialogs))
		c.ok(!w.shell.split.HasCSSClass(underClass), "%s: the content is still blurred after the dialog closed", where)
	}
	pw := func(where string, mode glassMode) {
		c.feed(engine.NeedsPassword{ID: 72, Kind: engine.PasswordUpFront})
		d := w.dialogs[72]
		if d == nil {
			c.fail("%s: no password dialog", where)
			return
		}
		c.ok(d.HasCSSClass(glassClass) == (mode != glassOff), "%s: the alert dialog's classes %v", where, d.CSSClasses())
		c.ok(w.shell.split.HasCSSClass(underClass) == (mode == glassBlur && gpu), "%s: content blur under the alert dialog", where)
	}
	pwShut := func() { c.feed(engine.PromptClosed{ID: 72}) }

	c.ok(w.set.glass != nil && w.set.blur != nil, "the Settings page has no glass switch and blur row")
	c.ok(!w.set.glass.Active() && !w.set.blur.Sensitive(), "glass is on at the start, or the blur row is usable without it")
	onMode := glassFallback
	if blurAuto {
		onMode = glassBlur
	}
	step := []func(){
		func() { look("start", glassOff) },
		func() { prompt("default dialog", glassOff) },
		func() { shut() },
		func() { closed("default dialog"); w.set.glass.SetActive(true) },
		func() {
			look("glass on", onMode)
			c.ok(w.set.blur.Sensitive(), "the blur row stays off with glass on")
			s, err := settings.Load(w.set.path)
			c.ok(err == nil && s.Glass && s.BlurMode() == settings.BlurAuto, "gui.json after the switch: %+v %v", s, err)
		},
		func() { prompt("glass dialog", onMode) },
		func() { shut() },
		func() { closed("glass dialog"); pw("glass password", onMode) },
		func() { pwShut() },
		func() { closed("glass password"); w.set.blur.SetSelected(2) }, // Off
		func() {
			look("blur off", glassFallback)
			s, _ := settings.Load(w.set.path)
			c.ok(s.Blur == settings.BlurOff, "gui.json blur %q, want off", s.Blur)
		},
		func() { prompt("fallback dialog", glassFallback) },
		func() { shut() },
		func() { closed("fallback dialog"); w.set.blur.SetSelected(1) }, // On
		func() { look("blur on", glassBlur) },
		func() { prompt("forced blur dialog", glassBlur) },
		func() { shut() },
		func() { closed("forced blur dialog"); w.set.blur.SetSelected(0) }, // Auto
		func() { look("blur auto", onMode) },
		func() { w.set.glass.SetActive(false) },
		func() {
			look("glass off again", glassOff)
			c.ok(!w.set.blur.Sensitive(), "the blur row is usable with glass off")
			s, _ := settings.Load(w.set.path)
			c.ok(!s.Glass && s.BlurMode() == settings.BlurAuto, "gui.json after switching off: %+v", s)
		},
		func() { prompt("after off", glassOff) },
		func() { shut() },
		func() { closed("after off") },
		func() {
			c.ok(blurSeen == (shotsBuilt && onMode == glassBlur && gpu), "the blur node was seen: %v (blur skin %v, gpu %v)", blurSeen, onMode == glassBlur, gpu)
		},
	}
	c.seq(350, step...)
}

// TestGUIGlass: the Settings round trip and the skin on a real window,
// with Auto picking blur under Hyprland and the fallback elsewhere (the
// session variable faked in the child's environment), and the content blur
// on a GPU renderer (stood in for) and not on cairo.
func TestGUIGlass(t *testing.T) {
	needDisplay(t)
	for _, c := range []struct {
		name string
		env  []string
	}{
		{"light, no hyprland, cairo", []string{glassSchemeEnv + "=light"}},
		{"dark, hyprland, gpu renderer", []string{glassSchemeEnv + "=dark", settings.HyprlandEnv + "=sig", glassRendererEnv + "=GskNglRenderer"}},
		{"light, registry advertises the protocol, no hyprland", []string{glassSchemeEnv + "=light", glassRegistryEnv + "=has", glassRendererEnv + "=GskNglRenderer"}},
		{"dark, registry lacks it, no hyprland", []string{glassSchemeEnv + "=dark", glassRegistryEnv + "=lacks"}},
		{"light, hyprland, cairo", []string{glassSchemeEnv + "=light", settings.HyprlandEnv + "=sig", glassRendererEnv + "=GskCairoRenderer"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := t.TempDir()
			child(t, "cov-glass", append([]string{"XDG_CONFIG_HOME=" + cfg}, c.env...)...)
			// The switch was written to gui.json and switched off again.
			s, err := settings.Load(filepath.Join(cfg, "archmaid", "gui.json"))
			if err != nil || s.Glass {
				t.Errorf("gui.json at the end: %+v, %v", s, err)
			}
		})
	}
}

// covGlassShots: Overview, Packages, a confirm sheet and the Run page.
func covGlassShots(c *covEnv) {
	w, e := c.w, pkgFix
	c.quiet()
	glassSetup(w)
	tag := fmt.Sprintf("%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv))
	p := w.pkgs
	pkgSeq(c,
		func() { shoot(w, "glass", tag+"-overview"); w.stack.SetVisibleChildName("packages") },
		func() { e.typeQuery(p, "firefox"); e.advance(120) },
		func() {
			e.advance(400)
			p.results.Emit("row-activated", rowByName(p.results, "firefox"))
		},
		func() {
			// The package's hero is on its stat grid's outer edges (#261): the
			// grid's own in the default look, the cells' in glass.
			var hero, grid gtk.Widgetter
			eachWidget(p.detail, func(wd gtk.Widgetter) {
				b := gtk.BaseWidget(wd)
				if hero == nil && b.HasCSSClass("am-hero") {
					hero = wd
				}
				if grid == nil && b.HasCSSClass("am-grid") {
					grid = wd
				}
			})
			if hero == nil || grid == nil {
				c.fail("the package detail has no hero or no stat grid")
			} else {
				hx, _, hr, _, _ := alignBounds(w, hero)
				want, wantR := 0.0, 0.0
				if os.Getenv(glassModeEnv) == "glass" {
					first := grid.(*gtk.FlowBox).ChildAtIndex(0).Child()
					second := grid.(*gtk.FlowBox).ChildAtIndex(1).Child()
					fx, _, _, _, _ := alignBounds(w, first)
					_, _, sr, _, _ := alignBounds(w, second)
					want, wantR = fx, sr
				} else {
					want, _, wantR, _, _ = alignBounds(w, grid)
				}
				c.ok(math.Abs(hx-want) <= 0.5 && math.Abs(hr-wantR) <= 0.5, "the package hero spans %.1f..%.1f, its stat grid %.1f..%.1f", hx, hr, want, wantR)
				// Its buttons start on that edge, and its text where the stat
				// cells' text starts (#262).
				var install *gtk.Button
				eachWidget(p.detail, func(wd gtk.Widgetter) {
					if b, ok := wd.(*gtk.Button); ok && install == nil && b.Label() == "Install" {
						install = b
					}
				})
				if install == nil {
					c.fail("no Install button")
				} else {
					ix, _, _, _, _ := alignBounds(w, install)
					c.ok(math.Abs(ix-want) <= 0.5, "Install starts at %.1f, the stat grid's edge is %.1f", ix, want)
				}
				var heroKick, cellLabel gtk.Widgetter
				eachWidget(hero, func(wd gtk.Widgetter) {
					if heroKick == nil && gtk.BaseWidget(wd).HasCSSClass("am-kicker") {
						heroKick = wd
					}
				})
				eachWidget(grid.(*gtk.FlowBox).ChildAtIndex(0).Child(), func(wd gtk.Widgetter) {
					if _, ok := wd.(*gtk.Label); ok && cellLabel == nil {
						cellLabel = wd
					}
				})
				if heroKick != nil && cellLabel != nil {
					kx, _, _, _, _ := alignBounds(w, heroKick)
					lx, _, _, _, _ := alignBounds(w, cellLabel)
					c.ok(math.Abs(kx-lx) <= 0.5, "the package hero's text starts at %.1f, the stat cells' at %.1f", kx, lx)
				}
			}
			shoot(w, "glass", tag+"-packages")
			w.stack.SetVisibleChildName("overview")
			c.feed(engine.NeedsConfirm{ID: 81, Title: "Remove these orphans?", Msg: "Nothing else needs them.", Items: []string{"python-foo", "libbar", "qux-git"}, Default: true, YesLabel: "Remove"})
		},
		func() {}, func() {}, func() {}, func() {}, func() {}, // the dialog's opening animation
		func() {
			shoot(w, "glass", tag+"-confirm")
			if d := w.dialogs[81]; d != nil {
				c.press(d, "no")
			}
		},
		func() {}, func() {}, func() {}, func() {}, // the dialog closes
		func() {
			w.current = actions.IDFullCleanup
			c.feed(
				engine.QueueStarted{Action: actions.IDFullCleanup, Started: time.Now(), Steps: []engine.PlanStep{
					{ID: "one", Title: "Audit the AUR packages"}, {ID: "two", Title: "Update the system"}, {ID: "three", Title: "Trim the package cache"}}},
				engine.StepStarted{ID: "one"},
				engine.OutputChunk{ID: "one", Lines: []string{"checked 14 packages", "nothing suspicious"}},
				engine.StepDone{ID: "one", Status: steps.OK},
				engine.StepStarted{ID: "two"},
				engine.OutputChunk{ID: "two", Lines: []string{":: Synchronizing package databases...", " core is up to date", " warning: a package was skipped", "error: failed to commit transaction"}},
			)
			w.stack.SetVisibleChildName("run")
		},
		func() { shoot(w, "glass", tag+"-run"); w.stack.SetVisibleChildName("settings") },
		func() {},
		func() { shoot(w, "glass", tag+"-settings") },
	)
}

// TestGUIGlassShots takes the pages in both looks and both themes. With
// ARCHMAID_GUI_SHOTS set the PNGs stay there (glass-<look>-<scheme>-<page>.png).
func TestGUIGlassShots(t *testing.T) {
	needDisplay(t)
	needShots(t)
	dir := os.Getenv(shotsEnv)
	if dir == "" {
		dir = t.TempDir()
	}
	t.Setenv(shotsEnv, dir)
	for _, look := range []string{"default", "glass"} {
		for _, scheme := range []string{"light", "dark"} {
			cfg := t.TempDir()
			if look == "glass" {
				if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
					t.Fatal(err)
				}
			}
			child(t, "cov-glass-shots", "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"="+scheme, glassModeEnv+"="+look)
			for _, page := range []string{"overview", "packages", "confirm", "run", "settings"} {
				b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("glass-%s-%s-%s.png", look, scheme, page)))
				if err != nil {
					t.Errorf("%s %s %s: %v", look, scheme, page, err)
					continue
				}
				if w, h, ok := pngSize(b); !ok || w < 300 || h < 200 {
					t.Errorf("%s %s %s: not a window-sized PNG", look, scheme, page)
				}
			}
		}
	}
}
