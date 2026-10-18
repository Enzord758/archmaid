package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The decision dialogs: one dialog per open prompt (widedialog.go: a wide
// AdwDialog for the confirms, the pick list and the report, an
// AdwAlertDialog for the passwords), kept in w.dialogs by prompt ID until it is answered or the engine withdraws it
// (PromptClosed → ForceClose). Every answer goes through answer, which
// asks the model to claim the ID first: a stale ID (withdrawn, replaced by
// a newer prompt, already answered) is dropped there and never reaches the
// engine.

// askpassPromptMax is how much of sudo's prompt the askpass dialog shows,
// as the TUI's popup does.
const askpassPromptMax = 48

// prompt opens the dialog for p.
func (w *Window) prompt(p runview.Prompt) {
	var d *amDialog
	switch {
	case p.Confirm != nil:
		d = w.confirmDialog(*p.Confirm)
	case p.Password != nil:
		d = w.passwordDialog(*p.Password)
	case p.Selection != nil:
		d = w.selectionDialog(*p.Selection)
	case p.Report != nil:
		d = w.reportDialog(*p.Report)
	default:
		return
	}
	w.dialogs[p.ID] = d
	d.show(w.win)
}

// answer sends the answer to prompt id that cmd builds, if id is still the
// open prompt; cmd isn't called otherwise. It also forgets the dialog.
func (w *Window) answer(id engine.PromptID, cmd func() engine.Command) {
	if _, open := w.dialogs[id]; !open {
		return // withdrawn by the engine (ForceClose), or answered
	}
	delete(w.dialogs, id)
	if !w.model.Answer(id) {
		return
	}
	w.send(cmd())
}

// confirmDialog: the engine's YesLabel ("Yes" without one) and "Cancel",
// or "Skip" for an Optional step (Model.ConfirmLabels). For a destructive
// step the no button is the default whatever Default says, and yes is red;
// otherwise Default picks the default and yes is the suggested action.
// Closing the dialog is no. A gate confirm shows the plan it stands for.
//
// Restyled (P8 row 13, spec 1.3): a dry run's banner first, then the body,
// the lists, and a gate's plan as a numbered rail with its tags and notes.
// Wide (task #95, widedialog.go): the gate's steps beside its notes, the
// orphans beside what goes with them, as wide as that needs; one over the
// other under the dialog's breakpoint.
func (w *Window) confirmDialog(p engine.NeedsConfirm) *amDialog {
	heading, body := p.Title, p.Body
	if heading == "" {
		heading = p.Msg
	}
	if len(p.Items) > 0 && len(p.Direct) == 0 && len(p.Cascade) == 0 {
		// The items are listed below: the body (the TUI's text, the
		// first names and "…and N more") says the rest only.
		body = runview.BodyWithoutItems(body, p.Items)
	}
	// The orphans' cascade is pacman's "name version" lines, the orphans
	// included: what goes with them is the rest, by name.
	var extra []string
	for _, c := range p.Cascade {
		if name, _, _ := strings.Cut(c, " "); !slices.Contains(p.Direct, name) {
			extra = append(extra, c)
		}
	}
	width := wideText
	switch {
	case len(p.Plan) > 0:
		width = wideGate
	case len(p.Direct) > 0 && len(extra) > 0:
		width = wideLists
	case len(p.Direct) > 0 || len(p.Items) > 0:
		width = wideList
	}
	d := newWideDialog(heading, width)
	yes, no := w.model.ConfirmLabels(p)
	d.AddResponse("no", no)
	d.AddResponse("yes", yes)
	d.SetCloseResponse("no")
	switch {
	case p.Destructive:
		d.SetDefaultResponse("no")
		d.SetResponseAppearance("yes", adw.ResponseDestructive)
	case p.Default:
		d.SetDefaultResponse("yes")
		d.SetResponseAppearance("yes", adw.ResponseSuggested)
	default:
		d.SetDefaultResponse("no")
		d.SetResponseAppearance("yes", adw.ResponseSuggested)
	}
	if w.drvDry {
		d.add(dryBanner())
	}
	if body != "" {
		d.add(dialogText(body, "am-dialog-body"))
	}
	switch {
	case len(p.Direct) > 0 && len(extra) > 0:
		// Orphans: what was found, beside what goes with it.
		d.add(d.columns(d.itemList("Orphaned", p.Direct, p.Destructive), d.itemList("Removed with them", extra, p.Destructive), orphansColumn))
	case len(p.Direct) > 0 || len(p.Cascade) > 0:
		d.add(d.itemList("Orphaned", p.Direct, p.Destructive))
		if len(extra) > 0 {
			d.add(d.itemList("Removed with them", extra, p.Destructive))
		}
	case len(p.Items) > 0:
		d.add(d.itemList("", p.Items, p.Destructive))
	}
	if len(p.Plan) > 0 {
		steps := planSteps(d, p.Plan)
		if notes := gateNotesFor(w.opts, p.Plan, w.drvDry); len(notes) > 0 {
			d.add(d.columns(steps, gateNotes(notes), notesColumn))
		} else {
			d.add(steps)
		}
	}
	d.ConnectResponse(func(resp string) {
		w.answer(p.ID, func() engine.Command { return engine.Confirm{Prompt: p.ID, Yes: resp == "yes"} })
	})
	return d
}

// The right columns' widths: the gate's notes (the mockup's 330 px), and
// the orphans' "Removed with them", one "name version" per line: 430 px
// holds ~48 monospace characters on a line (the 333 px half it had
// wrapped "python-dependency-of-orphan-123 0.123.0-2", Dev 2's #96);
// "Orphaned", names only, gets the rest (357 px).
const (
	notesColumn   = 330
	orphansColumn = 430
)

// dryBanner says up front that the run is a dry run: the Driver was made
// with dry run on (the Settings switch, or Dry run first).
func dryBanner() gtk.Widgetter {
	l := gtk.NewLabel(dryBannerText)
	l.AddCSSClass("am-dry-banner")
	l.SetWrap(true)
	l.SetJustify(gtk.JustifyCenter)
	return l
}

// dryBannerText is the dry-run tag's text (spec 1.11).
const dryBannerText = "DRY RUN · NOTHING ON THE SYSTEM CHANGES"

// dialogText is a dialog's text as a plain label of its extra child:
// never markup, wrapped, left-aligned. Not focusable (labels aren't), so
// it never scrolls the dialog's title away.
func dialogText(text, class string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetXAlign(0)
	l.AddCSSClass(class)
	return l
}

// planSteps is a gate's plan (spec 1.3): the kicker and the steps as a
// numbered rail with their asks and privilege tags (runview.PrivTag:
// "root", or "sudo" for topgrade, which gets root through its own sudo).
// Each step's title and tags are a tagLine: the tags at the line's end,
// or under the title below the dialog's breakpoint, as every narrow row
// has them (Dev 5's VM round b6: the 380 px bottom sheet kept them at the
// end). The rail is always whole, never a scroller of its own: a long
// plan scrolls with the dialog's middle, so it gets the height the window
// has (a 20-step rail stopped at 364 px with 400 px to spare at 1460x930,
// and shrank to three steps in a low window, Dev 2's #96).
func planSteps(d *amDialog, plan []engine.PlanStep) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	kicker := gtk.NewLabel(runview.GateKicker(plan))
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	box.Append(kicker)
	rail := gtk.NewBox(gtk.OrientationVertical, 0)
	rail.AddCSSClass("am-rail")
	var lines []*tagLine
	for i, s := range plan {
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		row.AddCSSClass("am-rail-step")
		num := gtk.NewLabel(fmt.Sprintf("%02d", i+1))
		num.AddCSSClass("am-step-num")
		num.SetVAlign(gtk.AlignBaselineCenter)
		row.Append(num)
		title := gtk.NewLabel(s.Title)
		title.SetXAlign(0)
		title.SetWrap(true)
		title.SetWrapMode(pango.WrapWordChar) // a long package name breaks inside the word
		var tags []gtk.Widgetter
		if s.Confirm {
			tags = append(tags, tag("asks", "am-tag-asks"))
		}
		if t := privTag(runview.PrivTag(s.NeedsPriv, s.Verb)); t != nil {
			tags = append(tags, t)
		}
		line := newTagLine(title, 8, tags...)
		line.tags.SetVisible(len(tags) > 0)
		line.box.SetHExpand(true)
		line.box.SetVAlign(gtk.AlignBaselineCenter)
		lines = append(lines, line)
		row.Append(line.box)
		rail.Append(row)
	}
	d.onTight(func(tight bool) {
		for _, l := range lines {
			l.setUnder(tight)
		}
	})
	box.Append(rail)
	return box
}

// gateNotes is the gate's "Before you start" column: the notes computed
// from the plan, each in full.
func gateNotes(notes []string) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	kicker := gtk.NewLabel("Before you start")
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	box.Append(kicker)
	for _, n := range notes {
		l := dialogText(n, "am-note")
		// Its natural width under the column's (notesColumn), so the
		// column is that wide, measured and drawn alike, and the steps
		// get the rest.
		l.SetMaxWidthChars(20)
		box.Append(l)
	}
	return box
}

// listMaxHeight is how tall a dialog's list of names grows before it
// scrolls: about 25 lines (the VM's 24 cascade lines whole). In a window
// too low for that the list takes its whole height and the dialog's
// middle scrolls instead (capList).
const listMaxHeight = 460

// itemList is a titled list of package or file names, scrolling past
// listMaxHeight (capList); red text when they are about to be removed.
func (d *amDialog) itemList(title string, items []string, destructive bool) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	if title != "" {
		h := gtk.NewLabel(fmt.Sprintf("%s (%d)", title, len(items)))
		h.SetXAlign(0)
		h.AddCSSClass("heading")
		box.Append(h)
	}
	l := gtk.NewLabel(strings.Join(items, "\n"))
	l.SetXAlign(0)
	l.SetSelectable(true)
	// Selectable, but never focused when the dialog opens: the dialog
	// focuses its first focusable widget, and focusing this label scrolled
	// the dialog's content down to it, so in a short window the title went
	// out of view (the lists scroll; the dialog's top must stay). The text
	// can still be selected and copied with the mouse, and the default
	// response keeps the focus.
	l.SetFocusable(false)
	// Wrapped, not cut: one name per line, a long one broken inside the
	// word (a scrolled window that never scrolls sideways asks for the
	// label's whole width otherwise).
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.AddCSSClass("monospace")
	if destructive {
		l.AddCSSClass("error")
	}
	sc := gtk.NewScrolledWindow()
	d.capList(sc, listMaxHeight)
	sc.SetChild(l)
	box.Append(sc)
	return box
}

// KeptNote is what a password dialog adds on the real Driver, which keeps
// the run's first password (driver.Deps.KeepPassword): the spec's wording
// (1.11), unchanged.
const KeptNote = "archmaid keeps this password in memory for this run only. It answers topgrade's and yay's sudo and archmaid's own polkit prompts. Any other program that asks shows this dialog with its name."

// KeptOffNote is what the dialog says instead of KeptNote when the Driver
// keeps passwords but this prompt's password will not be kept (the PATH
// guard tripped, or the kept one was refused or ran out): why, in one line
// (NeedsPassword.KeepOff).
func KeptOffNote(why string) string {
	if why = sanitize.Clip(sanitize.OneLine(why), 120); why == "" {
		why = "the kept password was refused or has run out"
	}
	return "archmaid is not keeping your password for this run (" + why + "), so every password prompt asks you."
}

// keptNoteFor is the note under the entry for p on a Driver that keeps the
// password: KeptNote only when the Driver said p's password will be kept.
func keptNoteFor(p engine.NeedsPassword) string {
	if p.Keeps {
		return KeptNote
	}
	return KeptOffNote(p.KeepOff)
}

// passwordDialog asks for the sudo password: up front (with the gate it
// stands in for as its heading), or for sudo's askpass mid-step (naming
// who asked). "Cancel" is CancelPrompt.
//
// Restyled (P8 row 13, spec 1.4): the extra child holds, in order, the
// caller line, sudo's prompt and the error, the entry, and the kept-password
// note; the dialog's own body stays empty. Its message area must not
// scroll the title away when the entry takes the focus, in a 630x372
// window too (keepTitle).
//
// It stays an AdwAlertDialog (task #95, the owner's option A): the
// password path's order (response before the wipe at closed) is
// libadwaita's, unchanged. Wide where the window is wide enough
// (prefer-wide-layout: libadwaita's wide cap); its labels report their
// one-line width (wrapNatural), or the dialog would take their longest
// word's.
func (w *Window) passwordDialog(p engine.NeedsPassword) *amDialog {
	heading, caller, body := passwordText(p)
	d := adw.NewAlertDialog(heading, "")
	d.SetPreferWideLayout(true)
	// style.css: in a wide window its buttons are wide enough that
	// libadwaita lets the dialog grow to its wide cap (it caps a
	// prefer-wide dialog at its button row's width).
	d.AddCSSClass("am-password")
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("ok", "Authenticate")
	d.SetResponseAppearance("ok", adw.ResponseSuggested)
	d.SetDefaultResponse("ok")
	d.SetCloseResponse("cancel")
	d.SetResponseEnabled("ok", false)

	entry := adw.NewPasswordEntryRow()
	w.pwEntry = entry // the harness types into it
	entry.SetTitle("Password")
	entry.ConnectChanged(func() { d.SetResponseEnabled("ok", entry.TextLength() > 0) })
	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	list.Append(entry)
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	w.pwCaller = nil
	if caller != "" {
		// Who asks, on a line of its own: plain text (whatever ran sudo
		// names itself), wrapped, never ellipsized or dimmed.
		l := gtk.NewLabel(caller)
		l.SetUseMarkup(false)
		wrapNatural(l)
		l.SetEllipsize(pango.EllipsizeNone)
		l.SetXAlign(0)
		l.AddCSSClass("am-caller") // style.css: as prominent as the body
		w.pwCaller = l
		box.Append(l)
	}
	if body != "" {
		l := dialogText(body, "am-dialog-body") // sudo's prompt, the error
		wrapNatural(l)
		box.Append(l)
	}
	box.Append(list)
	if w.opts.KeepsPassword {
		// Not muted: what the kept password answers, and that any other
		// program asking shows this dialog, is part of the check.
		l := dialogText(keptNoteFor(p), "am-kept")
		wrapNatural(l)
		box.Append(l)
	}
	d.SetExtraChild(box)
	keepTitle(box)

	forget := func() {
		if w.pwEntry == entry {
			w.pwEntry = nil
		}
	}
	respond := func(resp string) {
		defer forget()
		if resp != "ok" {
			wipeEditable(entry)
			w.answer(p.ID, func() engine.Command { return engine.CancelPrompt{Prompt: p.ID} })
			return
		}
		w.answer(p.ID, func() engine.Command {
			pw := editableBytes(entry)
			wipeEditable(entry)
			// engine.Secret is a string by the engine's contract, so this
			// is the one Go string the password lives in: immutable, it
			// can't be zeroed and stays in the heap until the GC reuses
			// it. Everything before it (GTK's secure buffer, pw) is
			// wiped. An accepted residual risk: docs/HANDOVER.md →
			// "The GUI password's last copy".
			cmd := engine.SubmitPassword{Prompt: p.ID, Password: engine.Secret(pw)}
			clear(pw)
			return cmd
		})
	}
	d.ConnectResponse(respond)
	// However it closes — answered, or withdrawn by the engine
	// (PromptClosed, the engine stopping: ForceClose with no response) —
	// the entry is wiped and forgotten. Not at "closed" itself: a clicked
	// button closes the dialog first and emits its response after
	// (libadwaita's AdwAlertDialog), so wiping there sent an empty
	// password for every click on Authenticate. The response, if any,
	// comes in the same call, before this idle runs; respond wipes too.
	d.ConnectClosed(func() {
		post(func() {
			wipeEditable(entry)
			forget()
		})
	})
	entry.ConnectEntryActivated(func() {
		if entry.TextLength() == 0 {
			return
		}
		respond("ok")
		d.ForceClose()
	})
	return alertDialog(d)
}

// keepTitle stops a dialog's message area from scrolling to the focused entry:
// the area (heading, then the extra child) is a GtkScrolledWindow whose
// viewport scrolls to its focus child, so in a short window focusing the
// entry scrolled the title out of view (Dev 5, 630x372). The entry keeps
// the focus; the area starts at its top, and a short window scrolls it by
// hand. Found from box (the extra child) once it is in the dialog.
func keepTitle(box *gtk.Box) {
	box.ConnectRealize(func() {
		// The nearest viewport above the extra child, inside the dialog.
		for p, n := box.Parent(), 0; p != nil && n < 8; p, n = gtk.BaseWidget(p).Parent(), n+1 {
			if vp, ok := p.(*gtk.Viewport); ok {
				vp.SetScrollToFocus(false)
				return
			}
		}
	})
}

// PolkitTitle is the title of polkit's password dialog (its agent asks for
// archmaid's root helper), as the P8 spec has it.
const PolkitTitle = "Authorise archmaid's Root Steps"

// passwordText is the password dialog's heading, the caller line (who
// asks; "" up front) and the body. The kind and the title come from the
// prompt's data (Kind, Polkit), never from the Requester string, which
// for sudo is the name of whatever ran it.
func passwordText(p engine.NeedsPassword) (heading, caller, body string) {
	switch {
	case p.Kind == engine.PasswordAskpass && p.Polkit:
		// The action id verbatim, on the caller line. polkit's own prompt
		// (PAM's "Password:") isn't shown: the entry says it.
		heading = PolkitTitle
		caller = "Asked by polkit for archmaid's root helper."
		if p.Action != "" {
			caller = "Asked by polkit for " + sanitize.OneLine(p.Action) + "."
		}
	case p.Kind == engine.PasswordAskpass:
		heading = "sudo Asks for Your Password"
		caller = "A running step needs root."
		if p.Requester != "" {
			caller = "Asked by " + p.Requester + "."
		}
		if p.Prompt != "" {
			// sudo's prompt comes from whatever asked (sudo -p sets it):
			// one line, at most 48 runes, as the TUI shows it, and quoted
			// as sudo's, so it can't pass for our caller line above it
			// (sudo -A -p "Asked by /usr/bin/topgrade.").
			body = `sudo says: "` + sanitize.Clip(sanitize.OneLine(p.Prompt), askpassPromptMax) + `"`
		}
	default:
		heading = "Password Required"
		if p.Gate != "" {
			heading = p.Gate
		}
		body = "Your password lets archmaid run the steps that need root."
	}
	if p.Error != "" {
		body += "\n\n" + p.Error
	}
	// The engine's "Incorrect password — 2 attempts left" says it already;
	// a lockout notice in its place doesn't.
	if p.AttemptsLeft > 0 && p.Error != "" && !strings.Contains(p.Error, "attempt") {
		body += "\n" + plural.N(p.AttemptsLeft, "attempt", "attempts") + " left."
	}
	return heading, caller, strings.TrimPrefix(body, "\n\n")
}

// selectionDialog is a checkbox list; "Continue" sends the checked items
// (in the engine's order), "Cancel" cancels. A pick with rows (Clear
// caches) shows each row's label with its note under it (the package
// cache's warning stays visible), a disabled row has no way to be ticked
// and says why, and the go button is named by what it does ("Clear") and
// drawn destructive: the pick is the confirm.
func (w *Window) selectionDialog(p engine.NeedsSelection) *amDialog {
	title := p.Title
	if title == "" {
		title = "Choose"
	}
	d := newWideDialog(title, wideText)
	d.AddResponse("cancel", "Cancel")
	// A destructive go button is never the default response: Enter on the
	// dialog (or on a row) must not clear anything, so Cancel is the default.
	def := "ok"
	if len(p.Rows) == len(p.Items) && p.Go != "" {
		d.AddResponse("ok", strings.ToUpper(p.Go[:1])+p.Go[1:])
		d.SetResponseAppearance("ok", adw.ResponseDestructive)
		def = "cancel"
	} else {
		d.AddResponse("ok", "Continue")
		d.SetResponseAppearance("ok", adw.ResponseSuggested)
	}
	d.SetDefaultResponse(def)
	d.SetCloseResponse("cancel")
	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	checks := make([]*gtk.CheckButton, len(p.Items))
	for i, item := range p.Items {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(item)
		checks[i] = gtk.NewCheckButton()
		if len(p.Rows) == len(p.Items) {
			r := p.Rows[i]
			row.SetTitle(r.Label)
			row.SetSubtitle(r.Note)
			row.SetTitleLines(0)
			row.SetSubtitleLines(0)
			if r.Disabled {
				checks[i].SetSensitive(false)
				row.SetSensitive(false)
			}
		}
		row.AddPrefix(checks[i])
		row.SetActivatableWidget(checks[i])
		list.Append(row)
	}
	sc := gtk.NewScrolledWindow()
	d.capList(sc, listMaxHeight)
	sc.SetChild(list)
	d.add(sc)
	d.ConnectResponse(func(resp string) {
		w.answer(p.ID, func() engine.Command {
			if resp != "ok" {
				return engine.CancelPrompt{Prompt: p.ID}
			}
			picked := []string{}
			for i, c := range checks {
				if c.Active() {
					picked = append(picked, p.Items[i])
				}
			}
			return engine.Select{Prompt: p.ID, Picked: picked}
		})
	})
	w.checks = checks // the harness ticks them
	return d
}

// reportMaxHeight is how tall the audit report's findings grow before
// they scroll (capList).
const reportMaxHeight = 360

// reportDialog shows a report (the AUR audit's findings, high ones
// first-class in red). "Accept" (only when CanAccept) records the flagged
// changes as checked: Confirm(yes); "Close" dismisses: Confirm(no).
func (w *Window) reportDialog(p engine.OutputReport) *amDialog {
	heading, body := "Report", p.Text
	var extra *gtk.ScrolledWindow
	canAccept := false
	if a := p.Audit; a != nil {
		heading = "AUR Audit"
		high := 0
		for _, f := range a.Findings {
			if f.High {
				high++
			}
		}
		body = fmt.Sprintf("%s checked: %d flagged, %d high.", plural.N(a.Total, "foreign package", "foreign packages"), len(a.Findings), high)
		if a.CanAccept {
			body += "\nAccept records the high findings as checked, so they aren't flagged again."
		}
		canAccept = a.CanAccept
		if len(a.Findings) > 0 {
			list := gtk.NewListBox()
			list.AddCSSClass("boxed-list")
			list.SetSelectionMode(gtk.SelectionNone)
			for _, f := range a.Findings {
				row := adw.NewActionRow()
				row.SetUseMarkup(false)
				row.SetTitle(f.Name)
				sub := strings.Join(f.Flags, " · ")
				if f.Reason != "" {
					sub = f.Reason + "\n" + sub
				}
				row.SetSubtitle(sub)
				icon := gtk.NewImageFromIconName("dialog-information-symbolic")
				if f.High {
					icon = gtk.NewImageFromIconName("dialog-warning-symbolic")
					icon.AddCSSClass("error")
				}
				row.AddPrefix(icon)
				list.Append(row)
			}
			extra = gtk.NewScrolledWindow()
			extra.SetChild(list)
		}
	}
	width := wideText
	if extra != nil {
		width = wideList
	}
	d := newWideDialog(heading, width)
	d.AddResponse("close", "Close")
	d.SetCloseResponse("close")
	d.SetDefaultResponse("close")
	if canAccept {
		d.AddResponse("accept", "Accept")
		d.SetResponseAppearance("accept", adw.ResponseSuggested)
	}
	if body != "" {
		d.add(dialogText(body, "am-dialog-body"))
	}
	if extra != nil {
		d.capList(extra, reportMaxHeight)
		d.add(extra)
	}
	d.ConnectResponse(func(resp string) {
		w.answer(p.ID, func() engine.Command {
			return engine.Confirm{Prompt: p.ID, Yes: canAccept && resp == "accept"}
		})
	})
	return d
}

// gateNotesFor is the gate's notes for the auth mode the window's runs
// really use (Options.Engine.Auth) and its password handling: the real GUI
// is polkit with a kept password, and the stub plays the polkit plan.
//
// When the Driver keeps the password, the sheet asks Options.KeepPreview
// whether the PATH guard would turn that off, and words itself from the
// answer. It is a preview: the guard at step start decides.
func gateNotesFor(o Options, plan []engine.PlanStep, dry bool) []string {
	keepOff := ""
	if o.KeepsPassword && o.KeepPreview != nil && !dry {
		keepOff = o.KeepPreview()
	}
	return runview.GateNotes(plan, o.Stub || o.Engine.Auth == engine.AuthPolkit, o.KeepsPassword, keepOff, dry)
}
