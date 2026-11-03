package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// The decision dialogs' frame (task #95, the owner: "wider and less tall
// when there's space"). The confirms, the pick list and the report are an
// AdwDialog laid out here: the heading at the start in a top bar, the
// content in the middle (the only part that scrolls), the buttons at the
// end in a bottom bar. It is as wide as its content needs (wideGate,
// wideLists, wideText), stacks its columns under the dialog's own narrow
// breakpoint, and in a narrow window is a bottom sheet (libadwaita's auto
// presentation) with the buttons across it. The password dialogs and the
// quit question stay AdwAlertDialogs (alertDialog).
//
// Both kinds are a *amDialog with AdwAlertDialog's response API, so the
// window and the headless harness treat them alike: responses with labels,
// a default and a close response, ConnectResponse.

// The wide dialogs' content widths: the gate's two columns, the orphans'
// two lists, a single list (downloads, the audit report), a question or a
// pick list.
const (
	wideGate  = 880
	wideLists = 880
	wideList  = 720
	wideText  = 640
)

// wideNarrow is the dialog's own breakpoint: under it the columns stack
// and the buttons share the width.
const wideNarrow = "max-width: 640sp"

// wideTight is the second, lower one: under it a plan's tags go under
// their step's title. Between the two (a half-screen window, the VM's
// 570 px sheet) the columns are stacked but a step is still one line with
// its tags at its end; stacked with every tag under its title the gate's
// rail grew ~120 px and its notes ran below the dialog (#176).
const wideTight = "max-width: 500sp"

// wideMinW, wideMinH are a wide dialog's minimum size. A dialog with
// breakpoints has none of its own (libadwaita asks for one, as for an
// AdwBreakpointBin); this fits the smallest window libadwaita allows.
const (
	wideMinW = 300
	wideMinH = 200
)

// amDialog is one prompt's dialog, or the quit question: a wide AdwDialog
// (newWideDialog) or an AdwAlertDialog (alertDialog).
type amDialog struct {
	*adw.Dialog
	// alert is the AdwAlertDialog this is, or nil for a wide one; every
	// response call goes to it.
	alert *adw.AlertDialog

	// A wide dialog's own responses.
	heading    string
	responses  []*dialogResponse
	defaultID  string
	closeID    string
	handlers   []func(string)
	answered   bool
	body       *gtk.Box // the middle's content
	buttons    *gtk.Box // the bottom bar
	narrowFns  []func(narrow bool)
	tightFns   []func(tight bool)
	breakpoint *adw.Breakpoint
	parts      []gtk.Widgetter // the heading, the middle, the button bar (fitHeight)

	// The middle's lists (capList) and whether they are uncapped, at
	// which window size (fitLists); win is the window it is shown over.
	lists      []cappedList
	uncapped   bool
	uncappedAt [2]int
	win        *adw.ApplicationWindow
}

// cappedList is a list in a wide dialog's middle: its scroller and the
// height it scrolls past (capList).
type cappedList struct {
	sc  *gtk.ScrolledWindow
	cap int
}

// dialogResponse is one of a wide dialog's buttons.
type dialogResponse struct {
	id, label  string
	appearance adw.ResponseAppearance
	button     *gtk.Button
}

// alertDialog makes a an archmaid dialog (scoped): its response calls go
// to libadwaita's own, so its order (closed, then the response) stays.
func alertDialog(a *adw.AlertDialog) *amDialog {
	scoped(a)
	return &amDialog{Dialog: &a.Dialog, alert: a}
}

// newWideDialog is an empty wide dialog with heading, width px wide
// where the window allows; content goes into body.
func newWideDialog(heading string, width int) *amDialog {
	d := &amDialog{Dialog: adw.NewDialog(), heading: heading}
	d.AddCSSClass(scopeClass)
	d.AddCSSClass("am-dialog") // style.css: the sheet's corner marks
	d.AddCSSClass("am-wide")
	d.SetTitle(heading)
	d.SetContentWidth(width)
	d.SetSizeRequest(wideMinW, wideMinH)

	head := gtk.NewLabel(heading)
	head.SetUseMarkup(false)
	head.SetWrap(true)
	head.SetWrapMode(pango.WrapWordChar)
	head.SetXAlign(0)
	head.AddCSSClass("title-2")
	head.AddCSSClass("am-wide-heading")

	d.body = gtk.NewBox(gtk.OrientationVertical, 14)
	d.body.AddCSSClass("am-wide-body")
	// Only the middle scrolls: the heading and the buttons stay in view
	// in a short window (what keepTitle does for an AdwAlertDialog).
	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sc.SetPropagateNaturalHeight(true)
	sc.SetChild(d.body)

	d.buttons = gtk.NewBox(gtk.OrientationHorizontal, 10)
	d.buttons.SetHomogeneous(true)
	d.buttons.SetHAlign(gtk.AlignEnd)
	d.buttons.SetHExpand(true)
	bar := gtk.NewBox(gtk.OrientationHorizontal, 0)
	bar.AddCSSClass("am-wide-buttons") // its hairline spans the dialog
	bar.Append(d.buttons)

	view := adw.NewToolbarView()
	view.SetTopBarStyle(adw.ToolbarFlat)
	view.SetBottomBarStyle(adw.ToolbarFlat)
	view.AddTopBar(head)
	view.SetContent(sc)
	view.AddBottomBar(bar)
	d.SetChild(view)
	d.parts = []gtk.Widgetter{head, sc, bar}

	// The dialog's height is its content's at the width it has. Left to
	// libadwaita it is measured once, at the content width, so a bottom
	// sheet (narrower, its columns stacked) kept the wide layout's height
	// and scrolled with room to spare, and the wide layout got a gap under
	// its columns. Whenever the middle's allocation changes, the content
	// height is set to the content's natural height at the width it got;
	// that height depends only on the width, so this settles at once. The
	// window still caps it, and the middle scrolls beyond that.
	fit := func() { post(func() { d.fitHeight(view) }) }
	sc.VAdjustment().ConnectChanged(fit)
	view.ConnectMap(fit) // the first allocation can come before the map

	// Under the breakpoint the buttons share the dialog's width (a bottom
	// sheet's, in a narrow window) and the columns stack (columns).
	d.onNarrow(func(narrow bool) {
		if narrow {
			d.buttons.SetHAlign(gtk.AlignFill)
		} else {
			d.buttons.SetHAlign(gtk.AlignEnd)
		}
	})
	d.breakpoint = adw.NewBreakpoint(adw.BreakpointConditionParse(wideNarrow))
	// libadwaita applies one breakpoint at a time, so the lower one also
	// stands for narrow: bp is the one in force, "" for none.
	bp := ""
	set := func(on, name string) {
		if on != "" {
			bp = on
		} else if bp == name {
			bp = ""
		}
		d.setNarrow(bp != "")
		d.setTight(bp == "tight")
	}
	d.breakpoint.ConnectApply(func() { set("narrow", "narrow") })
	d.breakpoint.ConnectUnapply(func() { set("", "narrow") })
	d.AddBreakpoint(d.breakpoint)
	tight := adw.NewBreakpoint(adw.BreakpointConditionParse(wideTight))
	tight.ConnectApply(func() { set("tight", "tight") })
	tight.ConnectUnapply(func() { set("", "tight") })
	d.AddBreakpoint(tight)

	// Escape, the window's close and a ForceClose (the engine withdrew
	// the prompt) close an AdwDialog with nothing but its closed signal:
	// that is the close response, unless a button answered first. So
	// every wide dialog answers exactly once, or the engine would wait
	// for it forever.
	d.ConnectClosed(func() { d.respond(d.closeID) })
	return d
}

// show presents the dialog over win. A wide one first asks for the
// window's whole height: libadwaita's own measure is at the content
// width, and in a narrower dialog the heading and the buttons (wrapped)
// can need more than that, which left the middle a negative height.
// fitHeight shrinks it to its content once it has its width.
func (d *amDialog) show(w *Window) {
	win := w.win
	d.win = win
	w.dialogOpened(d)
	if d.alert == nil {
		if h := win.Height(); h > 0 {
			d.SetContentHeight(h)
		}
	}
	d.Present(win)
}

// fitHeight sets the dialog's content height to its parts' natural
// heights at the width view has now (newWideDialog): the heading, the
// middle and the buttons, each measured at that width. Not the toolbar
// view's own measure: it measures its bars at their natural width, one
// line, so a heading or a button label that wraps (a long path, "Clear
// everything under that folder" at 360 px) was left out, and the middle
// got a negative height.
//
// It also keeps one scroller in the middle (Dev 2's #96). A list (capList)
// scrolls by itself past its cap only while the dialog, its lists capped,
// gets its whole height; in a window too low for that the lists take
// their whole height and only the middle scrolls. A capped list's own
// minimum is a few rows, so in a low window the lists shrank instead of
// the middle scrolling (the orphans' two lists to 110 px each at
// 620x500): a scroller inside a scroller that never scrolled. Each new
// window size is tried capped first.
func (d *amDialog) fitHeight(view *adw.ToolbarView) {
	width := view.Width()
	if width <= 0 || !view.Mapped() {
		return
	}
	total := d.naturalHeight(width)
	if len(d.lists) > 0 && d.win != nil {
		size := [2]int{d.win.Width(), d.win.Height()}
		if d.uncapped && size != d.uncappedAt {
			d.setCapped(true)
			d.uncapped = false
			total = d.naturalHeight(width)
		}
		// view has the height the window lets the dialog have: show
		// asked for the window's, and an uncapped dialog for more.
		if !d.uncapped && total > view.Height()+1 {
			d.setCapped(false)
			d.uncapped, d.uncappedAt = true, size
			total = d.naturalHeight(width)
		}
	}
	if total > 0 && total != d.ContentHeight() {
		d.SetContentHeight(total)
	}
}

// naturalHeight is the sum of the dialog's parts' natural heights at
// width (fitHeight).
func (d *amDialog) naturalHeight(width int) int {
	total := 0
	for _, part := range d.parts {
		b := gtk.BaseWidget(part)
		w := width
		if minW, _, _, _ := b.Measure(gtk.OrientationHorizontal, -1); w < minW {
			w = minW // asking for less than the minimum is a GTK warning
		}
		_, nat, _, _ := b.Measure(gtk.OrientationVertical, w)
		total += nat
	}
	return total
}

// capList makes sc one of the middle's lists: its content's height up
// to maxH, scrolling past that, while the dialog gets its whole height
// (fitHeight).
func (d *amDialog) capList(sc *gtk.ScrolledWindow, maxH int) {
	sc.SetPropagateNaturalHeight(true)
	d.lists = append(d.lists, cappedList{sc: sc, cap: maxH})
	d.setCappedOne(d.lists[len(d.lists)-1], !d.uncapped)
}

// setCapped caps the middle's lists (each scrolls past its cap), or lets
// each take its whole height.
func (d *amDialog) setCapped(on bool) {
	for _, l := range d.lists {
		d.setCappedOne(l, on)
	}
}

func (d *amDialog) setCappedOne(l cappedList, on bool) {
	if on {
		l.sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		l.sc.SetMaxContentHeight(l.cap)
		return
	}
	l.sc.SetMaxContentHeight(-1)
	l.sc.SetPolicy(gtk.PolicyNever, gtk.PolicyNever)
}

// onNarrow runs f with the dialog's breakpoint state whenever it changes.
func (d *amDialog) onNarrow(f func(narrow bool)) { d.narrowFns = append(d.narrowFns, f) }

// onTight runs f with the lower breakpoint's state whenever it changes.
func (d *amDialog) onTight(f func(tight bool)) { d.tightFns = append(d.tightFns, f) }

func (d *amDialog) setTight(tight bool) {
	for _, f := range d.tightFns {
		f(tight)
	}
}

// setNarrow lays the dialog out for under its breakpoint, or above it.
func (d *amDialog) setNarrow(narrow bool) {
	for _, f := range d.narrowFns {
		f(narrow)
	}
}

// add appends w to the dialog's content.
func (d *amDialog) add(w gtk.Widgetter) { d.body.Append(w) }

// columns is left and right side by side, a hairline between them, right
// rightW px wide and left the rest; under the dialog's breakpoint, one
// over the other at the full width.
func (d *amDialog) columns(left, right gtk.Widgetter, rightW int) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 22)
	box.AddCSSClass("am-columns")
	sep := gtk.NewSeparator(gtk.OrientationVertical)
	sep.AddCSSClass("am-column-rule")
	gtk.BaseWidget(left).SetHExpand(true)
	gtk.BaseWidget(left).SetVAlign(gtk.AlignStart)
	gtk.BaseWidget(right).SetVAlign(gtk.AlignStart)
	box.Append(left)
	box.Append(sep)
	box.Append(right)
	d.onNarrow(func(narrow bool) {
		if narrow {
			box.SetOrientation(gtk.OrientationVertical)
			box.SetSpacing(10)
			sep.SetOrientation(gtk.OrientationHorizontal)
			gtk.BaseWidget(right).SetSizeRequest(-1, -1)
			return
		}
		box.SetOrientation(gtk.OrientationHorizontal)
		box.SetSpacing(22)
		sep.SetOrientation(gtk.OrientationVertical)
		gtk.BaseWidget(right).SetSizeRequest(rightW, -1)
	})
	gtk.BaseWidget(right).SetSizeRequest(rightW, -1)
	return box
}

// Heading is the dialog's heading.
func (d *amDialog) Heading() string {
	if d.alert != nil {
		return d.alert.Heading()
	}
	return d.heading
}

// AddResponse adds a button, after the ones added before.
func (d *amDialog) AddResponse(id, label string) {
	if d.alert != nil {
		d.alert.AddResponse(id, label)
		return
	}
	r := &dialogResponse{id: id, label: label, button: gtk.NewButtonWithLabel(label)}
	// A long label ("Clear everything under that folder") wraps rather
	// than make the button row wider than a narrow window; one line
	// wherever it fits.
	if l, ok := r.button.Child().(*gtk.Label); ok {
		wrapNatural(l)
		l.SetJustify(gtk.JustifyCenter)
	}
	r.button.ConnectClicked(func() {
		if d.respond(id) {
			d.ForceClose()
		}
	})
	d.responses = append(d.responses, r)
	d.buttons.Append(r.button)
}

// response is the wide dialog's response id, or nil.
func (d *amDialog) response(id string) *dialogResponse {
	for _, r := range d.responses {
		if r.id == id {
			return r
		}
	}
	return nil
}

// ResponseLabel is response id's button label.
func (d *amDialog) ResponseLabel(id string) string {
	if d.alert != nil {
		return d.alert.ResponseLabel(id)
	}
	if r := d.response(id); r != nil {
		return r.label
	}
	return ""
}

// SetResponseAppearance makes response id's button suggested or
// destructive.
func (d *amDialog) SetResponseAppearance(id string, a adw.ResponseAppearance) {
	if d.alert != nil {
		d.alert.SetResponseAppearance(id, a)
		return
	}
	r := d.response(id)
	if r == nil {
		return
	}
	r.appearance = a
	r.button.RemoveCSSClass("suggested-action")
	r.button.RemoveCSSClass("destructive-action")
	switch a {
	case adw.ResponseSuggested:
		r.button.AddCSSClass("suggested-action")
	case adw.ResponseDestructive:
		r.button.AddCSSClass("destructive-action")
	}
}

// ResponseAppearance is response id's appearance.
func (d *amDialog) ResponseAppearance(id string) adw.ResponseAppearance {
	if d.alert != nil {
		return d.alert.ResponseAppearance(id)
	}
	if r := d.response(id); r != nil {
		return r.appearance
	}
	return adw.ResponseDefault
}

// SetDefaultResponse makes response id the default: Enter activates it,
// and it has the focus when the dialog opens.
func (d *amDialog) SetDefaultResponse(id string) {
	if d.alert != nil {
		d.alert.SetDefaultResponse(id)
		return
	}
	d.defaultID = id
	if r := d.response(id); r != nil {
		d.SetDefaultWidget(r.button)
		d.SetFocus(r.button)
	}
}

// DefaultResponse is the default response's id.
func (d *amDialog) DefaultResponse() string {
	if d.alert != nil {
		return d.alert.DefaultResponse()
	}
	return d.defaultID
}

// SetCloseResponse is the response a close without a button sends
// (Escape, the window's close, a ForceClose).
func (d *amDialog) SetCloseResponse(id string) {
	if d.alert != nil {
		d.alert.SetCloseResponse(id)
		return
	}
	d.closeID = id
}

// ConnectResponse runs f with each response: a button's, or the close
// response.
func (d *amDialog) ConnectResponse(f func(string)) {
	if d.alert != nil {
		d.alert.ConnectResponse(f)
		return
	}
	d.handlers = append(d.handlers, f)
}

// respond sends response id to the handlers, once per dialog: false when
// the dialog had answered already.
func (d *amDialog) respond(id string) bool {
	if d.answered {
		return false
	}
	d.answered = true
	for _, f := range d.handlers {
		f(id)
	}
	return true
}
