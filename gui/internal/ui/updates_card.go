package ui

import (
	"context"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/actions"
)

// The Overview's Updates card (task #252): what updates-cache.json says
// (the timer's and the widget's file), "Check for updates", which runs the
// timer's own check (Options.RefreshUpdates = driver.RefreshUpdates: a user
// unit, no root, no password), and "Run topgrade" when updates wait. The
// card model is overview.UpdatesCard; this file is the widgets and the clock.

// updatesPollMS is how often the cache is looked at while a check runs.
const updatesPollMS = 1000

// updatesView is the card as last drawn, so a redraw that changes nothing
// costs a compare.
type updatesView struct {
	v                 overview.CardView
	busy, canCheck    bool
	checking, hasCall bool
	link              string // the held link's text, "" hides it
}

// updatesWidgets are the card's widgets.
type updatesWidgets struct {
	icon          *gtk.Image
	title, detail *gtk.Label
	held          *gtk.Button // "N held ›": opens the held updates sheet
	tag           *gtk.Label
	meta          *gtk.Box
	spin          *adw.Spinner
	check, topg   *gtk.Button
	shown         *updatesView
}

// newUpdatesCard is the card: kicker, the headline with its tag, the detail
// line and the two buttons (a FlowBox, so they wrap when narrow).
func (w *Window) newUpdatesCard() gtk.Widgetter {
	u := &updatesWidgets{}
	w.upd = u
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("am-cell")
	box.AddCSSClass("am-updates")
	kicker := gtk.NewLabel("UPDATES")
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	u.icon = gtk.NewImage()
	u.title = gtk.NewLabel("")
	u.title.SetXAlign(0)
	u.title.SetWrap(true)
	u.title.SetHExpand(true)
	u.title.SetMaxWidthChars(24)
	u.title.AddCSSClass("am-cell-title")
	u.spin = adw.NewSpinner()
	u.spin.SetVisible(false)
	u.tag = tag("", "am-tag-neutral")
	head.Append(u.icon)
	head.Append(u.title)
	head.Append(u.spin)
	// The tag leads the detail line, wide or narrow: the tag beside the
	// title was squeezed to 52 of its 66 px in a 380 px window.
	u.tag.SetVAlign(gtk.AlignStart)
	u.detail = gtk.NewLabel("")
	u.detail.SetXAlign(0)
	u.detail.SetWrap(true)
	u.detail.SetMaxWidthChars(30)
	u.detail.AddCSSClass("am-cell-desc")

	u.check = gtk.NewButtonWithLabel("Check for updates")
	u.check.ConnectClicked(w.checkUpdates)
	u.topg = gtk.NewButtonWithLabel("Run topgrade")
	u.topg.SetTooltipText("Updates the system with topgrade, after the AUR audit")
	u.topg.ConnectClicked(func() { w.startAction(actions.IDTopgrade) })
	buttons := gtk.NewFlowBox()
	buttons.SetSelectionMode(gtk.SelectionNone)
	buttons.SetFocusable(false)
	buttons.SetColumnSpacing(8)
	buttons.SetRowSpacing(8)
	buttons.SetHAlign(gtk.AlignStart)
	buttons.AddCSSClass("am-card-buttons")
	buttons.SetMaxChildrenPerLine(2)
	buttons.Append(u.check)
	buttons.Append(u.topg)
	for c := buttons.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		gtk.BaseWidget(c).SetFocusable(false)
	}

	box.Append(kicker)
	box.Append(head)
	w.topRows.head.AddWidget(head)
	u.meta = gtk.NewBox(gtk.OrientationHorizontal, 8)
	w.topRows.line.AddWidget(u.meta)
	u.meta.Append(u.tag)
	// The held link: accent, always underlined, a pointer (#280). A flat
	// button (the "link" class, our CSS), not a LinkButton: it opens a sheet.
	u.held = gtk.NewButtonWithLabel("")
	u.held.AddCSSClass("link")
	u.held.AddCSSClass("am-held-link")
	u.held.SetVAlign(gtk.AlignStart)
	u.held.SetCursorFromName("pointer")
	u.held.SetTooltipText("Show held updates")
	u.held.ConnectClicked(w.openHeld)
	u.meta.Append(u.held)
	u.detail.SetHExpand(true)
	u.meta.Append(u.detail)
	box.Append(u.meta)
	box.Append(buttons)
	w.readUpdates()
	w.renderUpdates()
	return box
}

// readUpdates rereads the cache (not while a check waits for it: Poll
// reads it then).
func (w *Window) readUpdates() {
	w.updU, w.updMod = overview.ReadUpdatesStamp(w.opts.StateDir)
	w.readHeld()
}

// renderUpdates draws the card from w.updU, the card state and the busy flag.
func (w *Window) renderUpdates() {
	u := w.upd
	if u == nil {
		return
	}
	v := w.updCard.View(w.updU, time.Now())
	link := w.held.LinkText(w.updU, time.Now())
	st := updatesView{v: v, busy: w.busy, canCheck: !w.busy && w.opts.RefreshUpdates != nil && !w.updCard.Checking(),
		checking: w.updCard.Checking(), hasCall: w.opts.RefreshUpdates != nil, link: link}
	if u.shown != nil && *u.shown == st {
		return
	}
	u.shown = &st
	u.title.SetText(v.Title)
	detail := v.Detail
	if link != "" && strings.HasPrefix(detail, "checked ") {
		detail = "· " + detail // "4 held › · checked 3 min ago"
	}
	u.detail.SetText(detail)
	u.detail.SetVisible(v.Detail != "")
	u.held.SetLabel(link)
	setShown(u.held, link != "")
	if link != "" {
		// What a screen reader says: the label alone ends in an arrow.
		gtk.BaseWidget(u.held).UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue("Show held updates (" + strings.TrimSuffix(link, " ›") + ")")})
	}
	for _, c := range []string{"ok", "warn", "danger", "neutral"} {
		u.tag.RemoveCSSClass("am-tag-" + c)
		u.icon.RemoveCSSClass("am-tag-" + c)
	}
	icon := "software-update-available-symbolic"
	switch v.Tone {
	case "ok":
		icon = "emblem-ok-symbolic"
	case "danger":
		icon = "dialog-error-symbolic"
	case "neutral", "":
		icon = "content-loading-symbolic"
	}
	tone := v.Tone
	if tone == "" {
		tone = "neutral"
	}
	u.icon.SetFromIconName(icon)
	u.icon.AddCSSClass("am-tag-" + tone)
	u.tag.AddCSSClass("am-tag-" + tone)
	u.tag.SetText(v.Tag)
	u.tag.SetVisible(v.Tag != "")
	u.meta.SetVisible(v.Tag != "" || v.Detail != "" || link != "")
	setShown(u.spin, v.Spinner)
	u.check.SetLabel("Check for updates")
	u.check.SetSensitive(st.canCheck)
	if !st.hasCall {
		u.check.SetTooltipText("Not available in this build")
	} else {
		u.check.SetTooltipText("Runs the timer's check now: no root, no password")
	}
	setShown(u.topg, true) // always: the cell that had it is System health now (#270)
	u.topg.SetSensitive(!w.busy)
}

// checkUpdates is the button: start the timer's check, then watch the cache
// for a new result (pollUpdates) for overview.CheckTimeout.
func (w *Window) checkUpdates() {
	fn := w.opts.RefreshUpdates
	if fn == nil || w.busy || w.closed || w.updCard.Checking() {
		return
	}
	w.readUpdates()
	w.updCard.Timeout = w.updTimeout
	w.updCard.Begin(w.updU, w.updMod, time.Now())
	w.renderUpdates()
	ctx, cancel := context.WithCancel(context.Background())
	w.updCancel = cancel
	gen := w.updGen + 1
	w.updGen = gen
	go func() {
		err := fn(ctx)
		if err == nil {
			return
		}
		post(func() {
			if w.closed || gen != w.updGen || !w.updCard.Checking() {
				return
			}
			w.updCard.Fail(err)
			w.updGen++
			w.renderUpdates()
		})
	}()
	poll := uint(updatesPollMS)
	if w.updPoll > 0 {
		poll = w.updPoll
	}
	coreglib.TimeoutAdd(poll, func() bool {
		if w.closed || gen != w.updGen {
			return false
		}
		return !w.pollUpdates()
	})
}

// pollUpdates looks at the cache; done when the check is over.
func (w *Window) pollUpdates() (done bool) {
	u, mod := overview.ReadUpdatesStamp(w.opts.StateDir)
	if !w.updCard.Poll(u, mod, time.Now()) {
		return false
	}
	w.updU, w.updMod = u, mod
	w.readHeld()
	w.updGen++
	w.renderUpdates()
	return true
}

// refreshUpdates rereads the cache after a run or when the Overview is
// shown, unless a check is waiting for it.
func (w *Window) refreshUpdates() {
	if w.updCard.Checking() {
		return
	}
	w.updCard.Clear()
	w.readUpdates()
	w.renderUpdates()
}
