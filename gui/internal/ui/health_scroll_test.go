package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
)

// manyChecks is a report long enough to scroll the sheet, with every row
// type that carries a button: LOOK with a command, LOOK with a link,
// Unknown, and compact OK rows.
func manyChecks() healthview.Report {
	cs := manyChecksList()
	return healthview.Report{Checks: cs}
}

// longCommand is far longer than a tooltip or a toast shows.
var longCommand = "sudo pacman -S --needed " + strings.Repeat("some-long-package-name ", 30) + "last-one"

func manyChecksList() []healthview.Check {
	var cs []healthview.Check
	for i := 1; i <= 3; i++ {
		cs = append(cs, healthview.Check{ID: fmt.Sprintf("look%d", i), Title: fmt.Sprintf("Look row %d", i), State: healthview.Look, Group: "system",
			Detail: "something worth a look", Copy: fmt.Sprintf("journalctl -b -u unit%d.service", i)})
	}
	cs[2].Copy = longCommand
	cs = append(cs, healthview.Check{ID: "news", Title: "Arch news", State: healthview.Look, Group: "packages",
		Detail: "1 new since your last update: Manual intervention for pacman 7.0.0 and local repositories required",
		Link:   "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"})
	for i := 1; i <= 2; i++ {
		cs = append(cs, healthview.Check{ID: fmt.Sprintf("unk%d", i), Title: fmt.Sprintf("Unknown row %d", i), State: healthview.Unknown, Group: "packages",
			Detail: "can't reach it", Copy: "ping -c1 archlinux.org"})
	}
	for i := 1; i <= 4; i++ {
		cs = append(cs, healthview.Check{ID: fmt.Sprintf("ok%d", i), Title: fmt.Sprintf("OK row %d", i), State: healthview.OK, Group: "hardware",
			Detail: "all fine", Copy: "systemctl status thing"})
	}
	return cs
}

// covHealthScroll: in a sheet tall enough to scroll, no row button (LOOK,
// Unknown, compact OK) sits under the scrollbar (#275).
func covHealthScroll(c *covEnv) {
	w, f := c.w, healthFake
	c.quiet()
	glassSetup(w)
	f.set(manyChecks())
	tagn := func(s string) string {
		return fmt.Sprintf("health-%s-%s-%s", os.Getenv(glassModeEnv), os.Getenv(glassSchemeEnv), s)
	}
	c.seq(300,
		func() { w.win.SetDefaultSize(1440, 900) },
		func() { w.refreshHealth() },
		func() { w.openHealth() },
		func() {
			sc := w.health.scroll
			if sc == nil {
				c.fail("no scrolled list in the sheet")
				return
			}
			adj := sc.VAdjustment()
			c.ok(adj.Upper() > adj.PageSize()+20, "the sheet does not scroll (content %.0f, page %.0f)", adj.Upper(), adj.PageSize())
			adj.SetValue((adj.Upper() - adj.PageSize()) / 2) // the middle
		},
		func() {
			sc := w.health.scroll
			bar := sc.VScrollbar()
			c.ok(gtk.BaseWidget(bar).Mapped(), "the scrollbar is not shown")
			sbBox, ok := gtk.BaseWidget(bar).ComputeBounds(sc)
			if !ok {
				c.fail("no bounds for the scrollbar")
				return
			}
			left := float64(sbBox.X())
			n := 0
			eachWidget(&w.health.sheet.Widget, func(wd gtk.Widgetter) {
				b, isBtn := wd.(*gtk.Button)
				if !isBtn || !gtk.BaseWidget(b).Mapped() || !gtk.BaseWidget(b).IsAncestor(sc) {
					return
				}
				r, has := gtk.BaseWidget(b).ComputeBounds(sc)
				if !has {
					return
				}
				if float64(r.Y()+r.Height()) < 0 || float64(r.Y()) > float64(sc.Height()) {
					return // scrolled out of view
				}
				n++
				right := float64(r.X() + r.Width())
				c.ok(right <= left+0.5, "button %q ends at %.1f, under the scrollbar, which starts at %.1f", b.Label(), right, left)
			})
			c.ok(n >= 4, "only %d row buttons were visible in the middle of the list", n)
			// A Copy click: the toast, then the shot with both.
			var cp *gtk.Button
			eachWidget(&w.health.sheet.Widget, func(wd gtk.Widgetter) {
				if b, isBtn := wd.(*gtk.Button); isBtn && cp == nil && (b.Label() == "Copy command" || b.Label() == "Copy") && gtk.BaseWidget(b).Mapped() {
					cp = b
				}
			})
			if cp != nil {
				cp.Emit("clicked")
			}
		},
		func() { shoot(w, "health", tagn("sheet-scrolled")) },
		func() {
			// A very long command: whole on the clipboard, cut in the toast and tooltip.
			var long *gtk.Button
			eachWidget(&w.health.sheet.Widget, func(wd gtk.Widgetter) {
				if gtk.BaseWidget(wd).Name() == "health-look3" {
					eachWidget(wd, func(x gtk.Widgetter) {
						if b, ok := x.(*gtk.Button); ok && long == nil {
							long = b
						}
					})
				}
			})
			if long == nil {
				c.fail("no button on the long-command row")
				return
			}
			long.Emit("clicked")
			toast := w.toasted[len(w.toasted)-1]
			c.ok(w.copied == longCommand, "the clipboard got %d bytes, not the whole command (%d)", len(w.copied), len(longCommand))
			c.ok(strings.HasSuffix(toast, "…”. Paste it in a terminal.") && len([]rune(toast)) < 100, "the toast for a long command: %q", toast)
			tip := long.TooltipText()
			c.ok(strings.HasSuffix(tip, "…") && len([]rune(tip)) <= 300 && strings.HasPrefix(tip, "sudo pacman -S --needed some-long"), "the tooltip for a long command: %q", tip)
		},
		func() { w.health.sheet.ForceClose() },
	)
}
