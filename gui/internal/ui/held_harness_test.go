package ui

// Held updates (#280): the Updates card's "N held ›" link and the "Held
// updates" sheet, on a display. A scratch state folder holds
// updates-cache.json (v3, with held AUR entries) and audit-state.json; the
// card's check (Options.RefreshUpdates) is a fake that counts its calls and
// rewrites the cache without the package that was updated. "Review and
// update" runs through the stub engine's Packages scripts (the demo AUR
// catalog's paru-bin and risky-bin).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/pkgdb"
)

type heldEnvT struct {
	mu    sync.Mutex
	dir   string
	calls int
}

var heldEnv *heldEnvT

// heldSizeEnv is the window size the scroll scenario runs at ("1425x898").
const heldSizeEnv = "ARCHMAID_HELD_SIZE"

// heldPkgs are the cache's held AUR entries: with an age, with a small one
// and without any.
const heldPkgsJSON = `[
 {"name":"linux","from":"7.3.1-1","to":"7.3.2-1","src":"repo"},
 {"name":"zen-browser-bin","from":"1.17.2-1","to":"1.17.3-1","src":"aur","held":false,"age":"3d4h"},
 {"name":"paru-bin","from":"1.2.74-1","to":"1.2.76-1","src":"aur","held":true,"age":"2d4h"},
 {"name":"risky-bin","from":"3.1.58-1","to":"3.1.59-1","src":"aur","held":true,"age":"1d9h"},
 {"name":"noage-pkg","from":"1.1-6","to":"1.1-7","src":"aur","held":true}]`

func (e *heldEnvT) writeCache(held int, pkgs string, at time.Time) {
	if pkgs == "" {
		pkgs = "[]"
	}
	b := fmt.Sprintf(`{"v":3,"count":1,"repo":0,"aur":1,"aur_held":%d,"checked":%q,"packages":%s}`, held, at.Format(time.RFC3339), pkgs)
	p := filepath.Join(e.dir, "updates-cache.json")
	tmp := p + ".tmp"
	if os.WriteFile(tmp, []byte(b), 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}

func (e *heldEnvT) writeAudit(findings ...audit.StateFinding) {
	p := filepath.Join(e.dir, audit.StateFile)
	if len(findings) == 0 {
		_ = os.Remove(p)
		return
	}
	_ = audit.WriteState(p, audit.State{V: audit.StateVersion, Checked: time.Now(), High: findings})
}

// refresh is the card's check: it counts, and writes the cache again as the
// check would after paru-bin was updated.
func (e *heldEnvT) refresh(context.Context) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	time.AfterFunc(300*time.Millisecond, func() {
		e.writeCache(2, `[
 {"name":"risky-bin","from":"3.1.58-1","to":"3.1.59-1","src":"aur","held":true,"age":"1d9h"},
 {"name":"noage-pkg","from":"1.1-6","to":"1.1-7","src":"aur","held":true}]`, time.Now())
	})
	return nil
}

func (e *heldEnvT) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

var daux = audit.StateFinding{Pkg: "dux-search-bin", Reason: "new co-maintainer · not accepted yet"}

func init() {
	setup := func(withPackages bool) func(*Options, *report) {
		return func(o *Options, _ *report) {
			dir, err := os.MkdirTemp("", "archmaid-held-")
			if err != nil {
				panic(err)
			}
			heldEnv = &heldEnvT{dir: dir}
			heldEnv.writeCache(3, heldPkgsJSON, time.Now())
			heldEnv.writeAudit(daux)
			o.StateDir = dir
			o.RefreshUpdates = heldEnv.refresh
			if withPackages {
				o.Packages = demoWiring()
			}
		}
	}
	for s, f := range map[string]func(*covEnv){"cov-held-card": covHeldCard, "cov-held-sheet": covHeldSheet,
		"cov-held-scroll": covHeldScroll, "cov-held-review": covHeldReview, "cov-held-audit": covHeldAudit} {
		covScenarios[s] = f
		covOptions[s] = setup(s == "cov-held-review" || s == "cov-held-audit")
	}
}

// TestGUIHeld runs the held updates scenarios; the card and the sheet in
// the default look (light) and in glass (dark).
func TestGUIHeld(t *testing.T) {
	needDisplay(t)
	cfg := t.TempDir()
	if err := settings.Save(filepath.Join(cfg, "archmaid", "gui.json"), settings.Settings{Glass: true, Blur: settings.BlurOn}); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	for _, sc := range []string{"cov-held-card", "cov-held-sheet"} {
		child(t, sc, "XDG_CONFIG_HOME="+plain, glassSchemeEnv+"=light", glassModeEnv+"=default")
		child(t, sc, "XDG_CONFIG_HOME="+cfg, glassSchemeEnv+"=dark", glassModeEnv+"=glass")
	}
	for _, size := range []string{"1425x898", "1280x720"} {
		child(t, "cov-held-scroll", "XDG_CONFIG_HOME="+plain, glassSchemeEnv+"=light", glassModeEnv+"=default", heldSizeEnv+"="+size)
	}
	child(t, "cov-held-review")
	child(t, "cov-held-audit")
}

// heldLink is the card's link.
func heldLink(w *Window) *gtk.Button { return w.upd.held }

func covHeldCard(c *covEnv) {
	w, e := c.w, heldEnv
	c.quiet()
	glassSetup(w)
	tagn := func(s string) string {
		return fmt.Sprintf("held-%s-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv), s)
	}
	reread := func() { w.refreshUpdates() }
	c.seq(300,
		func() { w.win.SetDefaultSize(1440, 900) },
		func() {
			l := heldLink(w)
			// 3 held by the cooldown (the cache's count) and 1 the audit holds.
			c.ok(l.IsVisible() && l.Label() == "4 held ›", "the link is %q (shown %v), want '4 held ›'", l.Label(), l.IsVisible())
			c.ok(l.HasCSSClass("link") && l.HasCSSClass("am-held-link"), "the link has no link class")
			c.ok(l.TooltipText() == "Show held updates", "tooltip %q", l.TooltipText())
			cur := l.Cursor()
			c.ok(cur != nil && cur.Name() == "pointer", "the link has no pointer cursor")
			c.ok(strings.HasPrefix(w.upd.detail.Text(), "· checked "), "the detail %q does not follow the link", w.upd.detail.Text())
			// Always underlined, in the accent colour: the CSS says so, in both looks.
			c.ok(strings.Contains(styleCSS, "button.am-held-link label { text-decoration: underline; }"), "the link is not always underlined")
			c.ok(strings.Contains(styleCSS, "color: @accent_color;"), "the link is not in the accent colour")
			shoot(w, "held", tagn("card"))
			// Only the cooldown, or only the audit: still a link.
			e.writeAudit()
			reread()
		},
		func() {
			c.ok(heldLink(w).IsVisible() && heldLink(w).Label() == "3 held ›", "cooldown only: %q (%v)", heldLink(w).Label(), heldLink(w).IsVisible())
			e.writeCache(0, "", time.Now())
			e.writeAudit(daux, audit.StateFinding{Pkg: "other-bin", Reason: "maintainer alice → mallory"})
			reread()
		},
		func() {
			c.ok(heldLink(w).IsVisible() && heldLink(w).Label() == "2 held ›", "audit only: %q (%v)", heldLink(w).Label(), heldLink(w).IsVisible())
			e.writeAudit()
			reread()
		},
		func() {
			// Nothing held: no link, and the line has no leading dot.
			c.ok(!heldLink(w).IsVisible(), "nothing held but the link is shown: %q", heldLink(w).Label())
			c.ok(strings.HasPrefix(w.upd.detail.Text(), "checked "), "the detail %q", w.upd.detail.Text())
			shoot(w, "held", tagn("card-none"))
			// A stale cache holds nothing back that it can vouch for.
			e.writeCache(3, heldPkgsJSON, time.Now().Add(-3*24*time.Hour))
			reread()
		},
		func() {
			c.ok(!heldLink(w).IsVisible(), "a stale cache shows its held count: %q", heldLink(w).Label())
			w.upd.held.Emit("clicked") // hidden, but a click still opens the sheet: it says nothing is held
			c.ok(w.heldSheet != nil, "the link did not open the sheet")
			c.ok(strings.Contains(dialogLabels(w.heldSheet, nil), "Nothing is held back now."), "the empty sheet says %q", dialogLabels(w.heldSheet, nil))
			w.closeHeld()
		},
	)
}

// heldRowButton is the button of the row called id in the held sheet.
func heldRowButton(w *Window, id string) *gtk.Button {
	var found *gtk.Button
	eachWidget(&w.heldSheet.Widget, func(wd gtk.Widgetter) {
		if gtk.BaseWidget(wd).Name() == id {
			eachWidget(wd, func(x gtk.Widgetter) {
				if b, ok := x.(*gtk.Button); ok && found == nil {
					found = b
				}
			})
		}
	})
	return found
}

func covHeldSheet(c *covEnv) {
	w, e := c.w, heldEnv
	c.quiet()
	glassSetup(w)
	tagn := func(s string) string {
		return fmt.Sprintf("held-%s-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv), s)
	}
	c.seq(300,
		func() { w.win.SetDefaultSize(1440, 900) },
		func() { w.upd.held.Emit("clicked") },
		func() {
			c.ok(w.heldSheet != nil && w.heldSheet.Heading() == "Held updates", "no sheet")
			txt := dialogLabels(w.heldSheet, nil)
			for _, want := range []string{
				"These AUR updates are held back on purpose. Updating one now skips that safety window, so you review its PKGBUILD first.",
				"COOLDOWN · NEWER THAN 72 H", "HELD BY THE AUR AUDIT",
				"held 2d4h", "paru-bin", "1.2.74-1 → 1.2.76-1 · free in 20 h",
				"held 1d9h", "risky-bin", "3.1.58-1 → 3.1.59-1 · free in 1 d 15 h",
				"noage-pkg", "1.1-6 → 1.1-7 · free soon", // held without an age is held
				"audit", "dux-search-bin", "new co-maintainer · not accepted yet",
			} {
				c.ok(strings.Contains(txt, want), "the sheet lacks %q:\n%s", want, txt)
			}
			c.ok(!strings.Contains(txt, "zen-browser-bin") && !strings.Contains(txt, "linux"), "a package that is not held is listed:\n%s", txt)
			c.ok(heldRowButton(w, "held-paru-bin").Label() == "Review and update" && heldRowButton(w, "held-noage-pkg").Label() == "Review and update", "the cooldown rows' buttons")
			c.ok(heldRowButton(w, "audit-dux-search-bin").Label() == "Open audit", "the audit row's button")
			shoot(w, "held", tagn("sheet"))
			// No audit hold: that section is hidden.
			e.writeAudit()
			w.readUpdates()
		},
		func() {
			txt := dialogLabels(w.heldSheet, nil)
			c.ok(!strings.Contains(txt, "HELD BY THE AUR AUDIT") && strings.Contains(txt, "COOLDOWN"), "no audit holds, but the sheet says:\n%s", txt)
			// Only the audit holds something: the cooldown section is hidden.
			e.writeCache(0, "", time.Now())
			e.writeAudit(daux)
			w.readUpdates()
		},
		func() {
			txt := dialogLabels(w.heldSheet, nil)
			c.ok(strings.Contains(txt, "HELD BY THE AUR AUDIT") && !strings.Contains(txt, "COOLDOWN"), "only the audit holds, but the sheet says:\n%s", txt)
			e.writeAudit()
			w.readUpdates()
		},
		func() {
			c.ok(strings.Contains(dialogLabels(w.heldSheet, nil), "Nothing is held back now."), "empty: %q", dialogLabels(w.heldSheet, nil))
			c.press(w.heldSheet, "close")
		},
		func() { c.ok(w.heldSheet == nil, "Close left the sheet") },
	)
}

// covHeldScroll: a sheet that scrolls, at several window sizes: no scroller's
// scrollbar is over a row button (#275's rule).
func covHeldScroll(c *covEnv) {
	w, e := c.w, heldEnv
	c.quiet()
	glassSetup(w)
	width, height := 1425, 898
	if s := os.Getenv(heldSizeEnv); s != "" {
		_, _ = fmt.Sscanf(s, "%dx%d", &width, &height)
	}
	var many []string
	for i := 1; i <= 14; i++ {
		many = append(many, fmt.Sprintf(`{"name":"pkg-%d","from":"1-1","to":"2-1","src":"aur","held":true,"age":"%dh"}`, i, i))
	}
	e.writeCache(14, "["+strings.Join(many, ",")+"]", time.Now())
	e.writeAudit(daux)
	var scrolling []*gtk.ScrolledWindow
	c.seq(300,
		func() { w.win.SetDefaultSize(width, height) },
		func() { w.readUpdates(); w.upd.held.Emit("clicked") },
		func() {},
		func() {
			eachWidget(&w.heldSheet.Widget, func(wd gtk.Widgetter) {
				if sc, ok := wd.(*gtk.ScrolledWindow); ok {
					if adj := sc.VAdjustment(); adj.Upper() > adj.PageSize()+1 {
						scrolling = append(scrolling, sc)
					}
				}
			})
			c.ok(len(scrolling) > 0, "nothing scrolls at %dx%d", width, height)
			for _, sc := range scrolling {
				adj := sc.VAdjustment()
				adj.SetValue((adj.Upper() - adj.PageSize()) / 2)
			}
		},
		func() {
			total := 0
			for _, sc := range scrolling {
				c.ok(!sc.OverlayScrolling(), "a scroller in the held sheet has an overlay scrollbar over the buttons")
				sb, ok := gtk.BaseWidget(sc.VScrollbar()).ComputeBounds(sc)
				if !ok {
					c.fail("no bounds for the scrollbar")
					continue
				}
				eachWidget(&w.heldSheet.Widget, func(wd gtk.Widgetter) {
					b, isBtn := wd.(*gtk.Button)
					if !isBtn || !gtk.BaseWidget(b).Mapped() || !gtk.BaseWidget(b).IsAncestor(sc) {
						return
					}
					r, has := gtk.BaseWidget(b).ComputeBounds(sc)
					if !has || float64(r.Y()+r.Height()) < 0 || float64(r.Y()) > float64(sc.Height()) {
						return
					}
					total++
					c.ok(float64(r.X()+r.Width()) <= float64(sb.X())+0.5, "button %q ends at %.1f, under the scrollbar at %.1f (%dx%d)", b.Label(), r.X()+r.Width(), sb.X(), width, height)
				})
			}
			c.ok(total >= 4, "only %d buttons were checked", total)
			shoot(w, "held", fmt.Sprintf("sheet-scrolled-%dx%d", width, height))
			w.closeHeld()
		},
	)
}

// covHeldReview: "Review and update" opens the AUR install review of exactly
// that package, with the PKGBUILD box that has to be ticked, leaves the
// page's own list alone, and checks for updates again after the run.
func covHeldReview(c *covEnv) {
	w, e := c.w, heldEnv
	c.quiet()
	p := w.pkgs
	steps := []func() bool{
		once(func() {
			// The page's own list holds something else; it must come out the same.
			c.ok(p.ctl.Add(pkgdb.Package{Name: "list-item-bin", Version: "1-1", Repo: pkgdb.RepoAUR}, pkgview.ModeInstall) == nil, "adding to the list")
			w.upd.held.Emit("clicked")
		}, func() bool { return w.heldSheet != nil }),
		until(func() bool { return heldRowButton(w, "held-paru-bin") != nil }),
		once(func() { heldRowButton(w, "held-paru-bin").Emit("clicked") }, func() bool { return p.sheet != nil }),
		until(sheetIs(p, pkgview.SheetReview)),
		once(func() {
			c.ok(w.heldSheet == nil, "the held sheet stayed open over the review")
			txt := dialogLabels(p.sheet, nil)
			for _, want := range []string{"PKGBUILD review", "paru-bin", "I read the PKGBUILD"} {
				c.ok(strings.Contains(txt, want), "the review lacks %q:\n%s", want, txt)
			}
			c.ok(!strings.Contains(txt, "list-item-bin") && !strings.Contains(txt, "risky-bin"), "the review is for more than the one package:\n%s", txt)
			c.ok(len(p.sheetChecks) >= 1 && !p.sheetYes.Sensitive(), "Install is on before the PKGBUILD box is ticked (%d boxes)", len(p.sheetChecks))
			c.ok(e.callCount() == 0, "the check ran before the install")
			p.sheetChecks[0].SetActive(true)
		}, nil),
		until(func() bool { return !p.dirty }),
		once(func() {
			c.ok(p.sheetYes.Sensitive(), "Install is off with the box ticked")
			c.press(p.sheet, "yes")
		}, func() bool { return w.stack.VisibleChildName() == "run" }),
		until(runEnded(c)),
		// After the run: the card's check runs again and the held list follows.
		until(func() bool { return e.callCount() == 1 }),
		until(func() bool {
			for _, h := range w.held.Cooldown {
				if h.Name == "paru-bin" {
					return false
				}
			}
			return len(w.held.Cooldown) == 2
		}),
		once(func() {
			c.ok(p.ctl.List.Len() == 1 && p.ctl.List.Has("list-item-bin"), "the page's list is %v after the held update's run", p.ctl.List.Names())
			c.ok(w.upd.held.Label() == "3 held ›", "the card says %q after the check", w.upd.held.Label())
		}, nil),
	}
	flowSeq(c, steps...)
}

// covHeldAudit: "Open audit" starts the existing Audit AUR packages action;
// and a Review and update while a run goes only says so.
func covHeldAudit(c *covEnv) {
	w := c.w
	c.quiet()
	flowSeq(c,
		once(func() { w.upd.held.Emit("clicked") }, func() bool { return w.heldSheet != nil }),
		until(func() bool { return heldRowButton(w, "audit-dux-search-bin") != nil }),
		once(func() { heldRowButton(w, "audit-dux-search-bin").Emit("clicked") }, func() bool { return w.current == actions.IDAURAudit }),
		once(func() {
			c.ok(w.heldSheet == nil, "the held sheet stayed open")
			w.send(engine.Abort{}) // the audit is the existing action; this only ends it
		}, nil),
		until(runEnded(c)),
	)
}
