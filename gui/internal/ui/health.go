package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/healthview"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// System health (#270): the Overview's cell, its sheet, and the news
// callout of the Run topgrade and Full Cleanup confirms. The checks are
// Options.Health (read-only, no root, no password); nil turns the feature
// quiet (a calm cell, no callout).

// healthEveryMS is how often the checks run while the window is open (30
// min); healthStale how old the last run may be when the Overview is shown;
// healthRunTimeout the longest a run may take; newsWait how long a confirm
// waits for the news before it opens without. Variables: the tests shorten
// them.
var (
	healthEveryMS    uint = 30 * 60 * 1000
	healthStale           = 5 * time.Minute
	healthRunTimeout      = 20 * time.Second
	newsWait              = 1500 * time.Millisecond
)

// healthState is the window's System health.
type healthState struct {
	src    healthview.Source
	run    healthview.Runner
	again  bool // a run was asked for while one went: run once more
	report *healthview.Report

	cell    *gtk.Button
	icon    *gtk.Image
	look    *gtk.Label // the corner tag
	attn    *gtk.Label // the third line
	sheet   *amDialog
	fill    func()              // redraws the open sheet
	scroll  *gtk.ScrolledWindow // the open sheet list
	spinner *adw.Spinner
}

// initHealth wires the cell's widgets (newCell) and starts the schedule.
func (w *Window) initHealth(cell *gtk.Button, icon *gtk.Image, look, attn *gtk.Label) {
	h := &w.health
	h.cell, h.icon, h.look, h.attn = cell, icon, look, attn
	h.src = w.opts.Health
	w.renderHealthCell()
}

// startHealth runs the first check and the 30 minute timer (newWindow).
func (w *Window) startHealth() {
	w.refreshHealth()
	coreglib.TimeoutAdd(healthEveryMS, func() bool {
		if w.closed {
			return false
		}
		w.refreshHealth()
		return true
	})
}

// refreshHealth runs the checks off the main thread, never two at once: a
// request while one runs makes it run once more when it ends (its answer
// may predate what changed).
func (w *Window) refreshHealth() {
	h := &w.health
	if h.src == nil || w.closed {
		return
	}
	if !h.run.Begin() {
		h.again = true
		return
	}
	h.again = false
	w.renderHealthCell()
	if h.fill != nil {
		h.fill()
	}
	src := h.src
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), healthRunTimeout)
		defer cancel()
		r := src.Run(ctx)
		post(func() { w.healthDone(r) })
	}()
}

func (w *Window) healthDone(r healthview.Report) {
	h := &w.health
	h.run.End(time.Now())
	if w.closed {
		return
	}
	h.report = &r
	w.renderHealthCell()
	if h.fill != nil {
		h.fill()
	}
	if h.again {
		w.refreshHealth()
	}
}

// refreshHealthIfStale is the Overview being shown: run when the last run
// is over healthStale old.
func (w *Window) refreshHealthIfStale() {
	if w.health.src != nil && w.health.run.Stale(time.Now(), healthStale) {
		w.refreshHealth()
	}
}

// renderHealthCell draws the cell from the report: calm like the others,
// or emphasised (warn tint and edge, a look tag, the third line).
func (w *Window) renderHealthCell() {
	h := &w.health
	if h.cell == nil {
		return
	}
	c := healthview.CellFor(h.report, h.run.Running())
	if c.Emphasised {
		h.cell.AddCSSClass("am-cell-attn")
		h.icon.AddCSSClass("am-attn-icon")
	} else {
		h.cell.RemoveCSSClass("am-cell-attn")
		h.icon.RemoveCSSClass("am-attn-icon")
	}
	h.look.SetText(c.Tag)
	h.look.SetVisible(c.Tag != "")
	h.attn.SetText(c.Line)
	h.attn.SetVisible(c.Line != "")
	if c.Emphasised {
		h.attn.RemoveCSSClass("am-cell-checking")
		h.attn.AddCSSClass("am-cell-attn-line")
	} else {
		h.attn.AddCSSClass("am-cell-checking")
		h.attn.RemoveCSSClass("am-cell-attn-line")
	}
}

// ---- the sheet ----

// openHealth is the cell's click: the sheet over the window.
func (w *Window) openHealth() {
	h := &w.health
	if h.sheet != nil {
		return
	}
	d := newWideDialog("System health", wideText)
	d.AddResponse("close", "Close")
	d.SetCloseResponse("close")
	d.SetDefaultResponse("close")
	sub := gtk.NewLabel("")
	sub.SetXAlign(0)
	sub.SetWrap(true)
	sub.AddCSSClass("am-note")
	d.add(sub)
	rows := gtk.NewBox(gtk.OrientationVertical, 0)
	rows.AddCSSClass("am-health-rows")
	sc := gtk.NewScrolledWindow()
	sc.SetChild(rows)
	// A scrollbar of its own beside the rows, not GTK's overlay one: that
	// covers the right edge, and the row buttons are there (#275).
	sc.SetOverlayScrolling(false)
	h.scroll = sc
	d.capList(sc, healthMaxHeight)
	d.add(sc)

	again := gtk.NewButtonWithLabel("Check again")
	again.AddCSSClass("am-health-again")
	spin := adw.NewSpinner()
	spin.SetVisible(false)
	h.spinner = spin
	again.ConnectClicked(func() { w.refreshHealth() })
	d.buttons.Prepend(again)

	h.fill = func() {
		sub.SetText(healthview.Subtitle(h.report, time.Now()))
		for c := gtk.BaseWidget(rows).FirstChild(); c != nil; c = gtk.BaseWidget(rows).FirstChild() {
			rows.Remove(c)
		}
		running := h.run.Running()
		again.SetSensitive(!running && h.src != nil)
		again.SetLabel(map[bool]string{true: "Checking…", false: "Check again"}[running])
		if running {
			spin.SetVisible(true)
			rows.Append(spin)
		}
		switch {
		case h.report == nil && !running:
			rows.Append(dialogText("Nothing to check in this build.", "am-note"))
		case h.report != nil:
			for _, sec := range h.report.Sections() {
				head := gtk.NewLabel(strings.ToUpper(sec.Header))
				head.SetXAlign(0)
				head.SetName("section-" + sec.Header)
				head.AddCSSClass("am-kicker")
				head.AddCSSClass("am-health-section")
				if sec.Header == healthview.AttentionHeader {
					head.AddCSSClass("am-tone-warn")
				}
				rows.Append(head)
				for _, c := range sec.Checks {
					rows.Append(w.healthRow(c))
				}
			}
		}
	}
	d.ConnectClosed(func() { h.sheet, h.fill, h.scroll = nil, nil, nil })
	h.sheet = d
	h.fill()
	d.show(w)
}

// How much of a command the Copy command button's tooltip and toast show
// (healthview.Shown); the clipboard gets all of it.
const (
	healthTipMax   = 300
	healthToastMax = 60
)

// healthMaxHeight is how tall the sheet's rows grow before they scroll.
const healthMaxHeight = 440

// healthRow is one check: its state tag, the title, the detail on one
// line, and a Copy or Read/Open button. OK rows are single compact lines.
func (w *Window) healthRow(c healthview.Check) gtk.Widgetter {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.AddCSSClass("am-health-row")
	row.SetName("health-" + c.ID)
	var tagText, class string
	switch c.State {
	case healthview.Look:
		tagText, class = "LOOK", "am-tag-warn"
		row.AddCSSClass("am-health-look")
	case healthview.Unknown:
		tagText, class = "?", "am-tag-neutral"
	default:
		tagText, class = "OK", "am-tag-ok"
		row.AddCSSClass("am-health-ok")
	}
	t := tag(tagText, class)
	t.SetVAlign(gtk.AlignStart)
	t.SetSizeRequest(48, -1)
	t.SetXAlign(0.5)
	row.Append(t)

	title := gtk.NewLabel(c.Title)
	title.SetXAlign(0)
	title.SetUseMarkup(false)
	title.AddCSSClass("am-health-title")
	detail := gtk.NewLabel(c.Detail)
	detail.SetXAlign(0)
	detail.SetUseMarkup(false)
	detail.SetEllipsize(pango.EllipsizeEnd)
	detail.AddCSSClass("monospace")
	detail.AddCSSClass("am-health-detail")
	detail.SetTooltipText(c.Detail)
	var text gtk.Widgetter
	if c.State == healthview.OK {
		// Compact: title and detail on one line.
		line := gtk.NewBox(gtk.OrientationHorizontal, 10)
		line.Append(title)
		detail.SetHExpand(true)
		line.Append(detail)
		text = line
	} else {
		col := gtk.NewBox(gtk.OrientationVertical, 2)
		col.Append(title)
		col.Append(detail)
		text = col
	}
	gtk.BaseWidget(text).SetHExpand(true)
	row.Append(text)

	switch {
	case c.Copy != "":
		cp := gtk.NewButtonWithLabel("Copy command")
		cp.SetVAlign(gtk.AlignCenter)
		cp.SetTooltipText(healthview.Shown(c.Copy, healthTipMax))
		cmd := c.Copy
		cp.ConnectClicked(func() {
			w.copyText(cmd, "Copied “"+healthview.Shown(cmd, healthToastMax)+"”. Paste it in a terminal.")
		})
		row.Append(cp)
	case healthview.SafeLink(c.Link):
		label := "Open"
		if c.ID == "news" {
			label = "Read on archlinux.org"
		}
		op := gtk.NewButtonWithLabel(label)
		op.SetVAlign(gtk.AlignCenter)
		link := c.Link
		op.ConnectClicked(func() { w.openLink(link) })
		row.Append(op)
	}
	return row
}

// copyText puts text on the clipboard (it never runs it) and says so.
func (w *Window) copyText(text, toast string) {
	w.copied = text
	w.win.Clipboard().SetText(text)
	w.toast(toast)
}

// openLink opens an archlinux.org page in the desktop's browser. Only
// https://archlinux.org/… is ever opened, whatever a source says.
func (w *Window) openLink(link string) {
	if !healthview.SafeLink(link) {
		return
	}
	if err := w.launcher.OpenURI(link); err != nil {
		w.toast("Couldn't open it: " + err.Error())
	}
}

// ---- the news callout of the confirms ----

// wantsNews: the confirm is Run topgrade's or Full Cleanup's gate, and a
// source is there to ask.
func (w *Window) wantsNews(p engine.NeedsConfirm) bool {
	if w.health.src == nil || len(p.Plan) == 0 {
		return false
	}
	return w.current == actions.IDTopgrade || w.current == actions.IDFullCleanup
}

// confirmWithNews opens the confirm for p with the unread Arch news at its
// top. It waits at most newsWait for the news: a slow feed shows the
// confirm without. The news never blocks Run.
func (w *Window) confirmWithNews(p runview.Prompt) {
	conf := *p.Confirm
	id := p.ID
	shown := false
	show := func(n *healthview.Unread) {
		if shown || w.closed {
			return
		}
		shown = true
		if w.model.Prompt == nil || w.model.Prompt.ID != id {
			return // withdrawn while the news was fetched
		}
		d := w.confirmDialog(conf, n)
		w.dialogs[id] = d
		d.show(w)
	}
	src := w.health.src
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), newsWait)
		defer cancel()
		u, err := src.UnreadNews(ctx)
		post(func() {
			if err == nil && len(u.Items) > 0 {
				show(&u)
			} else {
				show(nil)
			}
		})
	}()
	coreglib.TimeoutAdd(uint(newsWait.Milliseconds())+100, func() bool {
		show(nil)
		return false
	})
}

// newsCallout is the block at the top of a confirm (NewsGate): a NEWS tag
// and "Read before updating", the newest unread item, how many more, when
// it was posted, and a link. Plain text throughout.
func (w *Window) newsCallout(u healthview.Unread) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.AddCSSClass("am-news")
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t := tag("NEWS", "am-tag-asks")
	t.SetVAlign(gtk.AlignCenter)
	head.Append(t)
	h := gtk.NewLabel("Read before updating")
	h.AddCSSClass("am-news-head")
	head.Append(h)
	box.Append(head)

	newest := u.Items[0]
	title := dialogText(newest.Title, "am-news-title")
	box.Append(title)
	if n := len(u.Items) - 1; n > 0 {
		box.Append(dialogText(fmt.Sprintf("and %d more", n), "am-note"))
	}
	meta := "archlinux.org · posted " + healthview.Ago(newest.Date, time.Now()) + " · after your last update"
	if u.FromCache && u.CacheAge > 0 {
		meta += " · cached " + healthview.Ago(time.Now().Add(-u.CacheAge), time.Now())
	}
	box.Append(dialogText(meta, "am-note"))
	if healthview.SafeLink(newest.Link) {
		link := gtk.NewButtonWithLabel("Open on archlinux.org")
		link.AddCSSClass("flat")
		link.AddCSSClass("am-news-link")
		link.SetHAlign(gtk.AlignStart)
		l := newest.Link
		link.ConnectClicked(func() { w.openLink(l) })
		box.Append(link)
	}
	return box
}
