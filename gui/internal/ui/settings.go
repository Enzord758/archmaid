package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/Enzord758/archmaid/gui/internal/settings"
	"github.com/Enzord758/archmaid/internal/frontend"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/version"
)

// The Settings page (P8 row 15): cards for dry run and the downloads
// folder, kept in $XDG_CONFIG_HOME/archmaid/gui.json (internal/settings),
// and for topgrade's config and the log folder, which open through the
// Launcher. A setting applies to the next Start: startAction makes a fresh
// Driver for every Start, with w.settings.

type settingsPage struct {
	path      string
	downloads *adw.EntryRow
	dryRun    *adw.SwitchRow
	// glass is the Appearance card's "Glass look" switch, blur its
	// "Blur behind window" row (Auto, On, Off: blurOptions).
	glass *adw.SwitchRow
	blur  *adw.ComboRow
	// dlError is the downloads folder's inline error; problem the file's
	// (unreadable, unsaved); configError the topgrade card's. All hidden
	// when there is nothing to say.
	dlError, problem, configError *gtk.Label
	// choose is Choose…, editor Open in editor, logFolder the log card's
	// Open folder (the harness presses them).
	choose, editor, logFolder *gtk.Button
	// about is the About line: "About: archmaid 2.0.0-3-g6ba8744".
	about *gtk.Label
}

func (w *Window) newSettings() gtk.Widgetter {
	p := &settingsPage{path: w.opts.SettingsPath}
	if p.path == "" {
		p.path = settings.DefaultPath()
	}
	w.set = p
	s, loadErr := settings.Load(p.path)
	// A folder from the file is vetted as one typed in is: a refused one
	// is shown with its reason and not used ("": discover), and the file
	// is left as it is.
	dlErr := ""
	if folder, err := settings.CheckDownloads(s.Downloads, settings.ResolverOptions("")); err != nil {
		dlErr = "Not used, so the folder is found instead: " + err.Error()
		w.settings = settings.Settings{DryRun: s.DryRun, Glass: s.Glass, Blur: s.Blur}
	} else {
		w.settings = settings.Settings{DryRun: s.DryRun, Downloads: folder, Glass: s.Glass, Blur: s.Blur}
	}

	// Four cards (P8 row 15): Runs, the downloads folder, topgrade's
	// config, the log folder; two to a line, one under settingsNarrow.
	cards := gtk.NewFlowBox()
	cards.SetSelectionMode(gtk.SelectionNone)
	cards.SetHomogeneous(true)
	cards.SetMinChildrenPerLine(2)
	cards.SetMaxChildrenPerLine(2)
	cards.SetColumnSpacing(18)
	cards.SetRowSpacing(18)
	cards.SetVAlign(gtk.AlignStart)

	p.dryRun = adw.NewSwitchRow()
	p.dryRun.SetTitle("Dry run")
	p.dryRun.SetSubtitle("Show what each action would do and change nothing")
	p.dryRun.SetActive(s.DryRun)
	p.dryRun.NotifyProperty("active", func() {
		next := w.settings
		next.DryRun = p.dryRun.Active()
		w.saveSettings(next)
	})
	cards.Append(card("Runs", "Dry run",
		"Logs everything to dryrun/ and asks for no password or authorisation. Changes apply to the next run.",
		boxedList(p.dryRun)))

	p.downloads = adw.NewEntryRow()
	p.downloads.SetTitle("Downloads folder")
	p.downloads.SetShowApplyButton(true)
	p.downloads.SetText(s.Downloads)
	p.downloads.ConnectApply(w.applyDownloads)
	p.downloads.ConnectChanged(func() { p.showDownloadsError("") })
	p.choose = gtk.NewButtonWithLabel("Choose…")
	p.choose.SetVAlign(gtk.AlignCenter)
	p.choose.ConnectClicked(w.chooseDownloads)
	p.downloads.AddSuffix(p.choose)
	p.dlError = errorLabel()
	cards.Append(card("Delete old downloads", "Downloads folder",
		"The folder it cleans. Leave it empty to find it: $ARCHMAID_DOWNLOADS, "+
			"XDG_DOWNLOAD_DIR, ~/Downloads, ~/Download. Never your home folder or a folder above it.",
		boxedList(p.downloads), p.dlError))
	p.showDownloadsError(dlErr)

	cards.Append(w.topgradeCard())
	cards.Append(w.logFolderCard())
	cards.Append(w.appearanceCard(s))

	p.problem = errorLabel()
	saved := gtk.NewLabel("Saved in " + p.path)
	saved.AddCSSClass("dim-label")
	saved.SetXAlign(0)
	saved.SetWrap(true)
	saved.SetWrapMode(pango.WrapWordChar)

	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.SetMarginTop(24)
	box.SetMarginBottom(24)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	box.Append(cards)
	box.Append(saved)
	p.about = gtk.NewLabel(aboutText())
	p.about.AddCSSClass("dim-label")
	p.about.SetXAlign(0)
	p.about.SetSelectable(true)
	box.Append(p.about)
	box.Append(p.problem)
	clamp := adw.NewClamp()
	clamp.SetMaximumSize(960)
	clamp.SetChild(box)
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetChild(clamp)
	if loadErr != nil {
		p.showProblem("Your settings can't be read, so the defaults apply: " + loadErr.Error())
	}
	bin := adw.NewBreakpointBin()
	bin.SetSizeRequest(300, 200)
	bin.SetChild(scroll)
	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(adw.BreakpointConditionMaxWidth, settingsNarrow, adw.LengthUnitSp))
	bp.AddSetter(cards, "min-children-per-line", uint(1))
	bp.AddSetter(cards, "max-children-per-line", uint(1))
	bin.AddBreakpoint(bp)
	return bin
}

// aboutText is the Settings page's About line: which build this is.
func aboutText() string { return "About: archmaid " + version.String() }

// settingsNarrow is the width under which the Settings cards stack.
const settingsNarrow = 720

// card is one Settings card: a kicker, a title, a line of help, then its
// widgets.
func card(kicker, title, help string, children ...gtk.Widgetter) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.AddCSSClass("card")
	inner := gtk.NewBox(gtk.OrientationVertical, 8)
	inner.SetMarginTop(18)
	inner.SetMarginBottom(18)
	inner.SetMarginStart(18)
	inner.SetMarginEnd(18)
	k := gtk.NewLabel(strings.ToUpper(kicker))
	k.AddCSSClass("caption-heading")
	k.AddCSSClass("accent")
	k.SetXAlign(0)
	t := gtk.NewLabel(title)
	t.AddCSSClass("title-3")
	t.SetXAlign(0)
	h := gtk.NewLabel(help)
	h.AddCSSClass("dim-label")
	h.SetXAlign(0)
	h.SetWrap(true)
	h.SetWrapMode(pango.WrapWordChar)
	// A wrapping label's natural width is its whole text on one line: cap
	// it, so two cards fit side by side where there is room.
	h.SetMaxWidthChars(36)
	inner.Append(k)
	inner.Append(t)
	inner.Append(h)
	for _, c := range children {
		inner.Append(c)
	}
	box.Append(inner)
	return box
}

// boxedList holds rows the way libadwaita draws them in a card.
func boxedList(rows ...gtk.Widgetter) gtk.Widgetter {
	l := gtk.NewListBox()
	l.AddCSSClass("boxed-list")
	l.SetSelectionMode(gtk.SelectionNone)
	for _, r := range rows {
		l.Append(r)
	}
	return l
}

// pathLabel shows a path whole: monospace, wrapped, never cut.
func pathLabel(path string) *gtk.Label {
	l := gtk.NewLabel(path)
	l.AddCSSClass("monospace")
	l.SetXAlign(0)
	l.SetSelectable(true)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetMaxWidthChars(36)
	return l
}

// topgradeCard: Open in editor creates ~/.config/topgrade.toml with the
// defaults when it is missing (frontend.EnsureTopgradeConfig, as the TUI
// does) and hands it to the desktop's default app through the Launcher;
// also in a dry run (decision Q7: editing a config isn't a run). Show in
// folder is the way round a missing default app.
func (w *Window) topgradeCard() gtk.Widgetter {
	p := w.set
	path, pathErr := frontend.TopgradeConfigPath()
	p.editor = gtk.NewButtonWithLabel("Open in editor")
	p.editor.AddCSSClass("suggested-action")
	p.editor.ConnectClicked(func() { w.openTopgradeConfig(path) })
	folder := gtk.NewButtonWithLabel("Show in folder")
	folder.ConnectClicked(func() {
		if err := w.launcher.OpenFolder(filepath.Dir(path)); err != nil {
			p.showConfigError("Couldn't open the folder: " + err.Error())
		}
	})
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	buttons.Append(p.editor)
	buttons.Append(folder)
	p.configError = errorLabel()
	shown := path
	if pathErr != nil {
		shown = "~/.config/topgrade.toml"
		p.editor.SetSensitive(false)
		folder.SetSensitive(false)
	}
	c := card("Topgrade", "Config file", "Created with defaults the first time you open it.",
		pathLabel(shown), buttons, p.configError)
	if pathErr != nil {
		p.showConfigError(pathErr.Error())
	}
	return c
}

func (w *Window) openTopgradeConfig(path string) {
	p := w.set
	if _, err := frontend.EnsureTopgradeConfig(path); err != nil {
		p.showConfigError("Couldn't create it: " + err.Error())
		return
	}
	if err := w.launcher.OpenFile(path); err != nil {
		p.showConfigError("Couldn't open it: " + err.Error())
		return
	}
	p.showConfigError("")
}

func (p *settingsPage) showConfigError(msg string) { showError(p.configError, msg) }

// logFolderCard: where the run logs are, and Open folder.
func (w *Window) logFolderCard() gtk.Widgetter {
	root := logstore.New(logstore.Options{Dir: w.opts.StateDir}).Root()
	open := gtk.NewButtonWithLabel("Open folder")
	open.SetHAlign(gtk.AlignStart)
	open.SetSensitive(root != "")
	open.ConnectClicked(func() {
		if err := w.launcher.OpenFolder(root); err != nil {
			w.toast("Couldn't open the folder: " + err.Error())
		}
	})
	w.set.logFolder = open
	return card("Logs", "Log folder",
		fmt.Sprintf("One folder per run. The newest %d are kept.", logstore.KeepRuns), pathLabel(root), open)
}

// blurOptions are the "Blur behind window" row's choices, in the order of
// settings.BlurAuto, BlurOn, BlurOff.
var blurOptions = []string{"Auto", "On", "Off"}

func blurIndex(mode string) uint {
	switch mode {
	case settings.BlurOn:
		return 1
	case settings.BlurOff:
		return 2
	}
	return 0
}

func blurValue(i uint) string {
	switch i {
	case 1:
		return settings.BlurOn
	case 2:
		return settings.BlurOff
	}
	return settings.BlurAuto
}

// appearanceCard: the glass look (glass.go). The switch and the blur row
// apply at once, without a restart: GTK restyles every widget when the
// stylesheet is added or removed. The blur is the compositor's; GTK only
// makes the window translucent.
func (w *Window) appearanceCard(s settings.Settings) gtk.Widgetter {
	p := w.set
	p.glass = adw.NewSwitchRow()
	p.glass.SetTitle("Glass look")
	p.glass.SetSubtitle("A translucent window with softer corners")
	p.glass.SetActive(s.Glass)
	p.blur = adw.NewComboRow()
	p.blur.SetTitle("Blur behind window")
	p.blur.SetModel(gtk.NewStringList(blurOptions))
	p.blur.SetSelected(blurIndex(s.BlurMode()))
	p.blur.SetSensitive(s.Glass)
	p.glass.NotifyProperty("active", func() {
		next := w.settings
		next.Glass = p.glass.Active()
		p.blur.SetSensitive(next.Glass)
		w.saveSettings(next)
		w.applyGlass()
	})
	p.blur.NotifyProperty("selected", func() {
		next := w.settings
		next.Blur = blurValue(p.blur.Selected())
		if next.Blur == w.settings.BlurMode() {
			return
		}
		w.saveSettings(next)
		w.applyGlass()
	})
	return card("Appearance", "Glass look",
		"Applies at once. The blur comes from the compositor (Hyprland's decoration:blur). "+
			"Auto turns it on under Hyprland only; without blur the glass stays nearly opaque.",
		boxedList(p.glass, p.blur))
}

// chooseDownloads is Choose…: the Launcher's folder dialog, starting at
// the folder in the row; the choice is applied (and vetted) as a typed
// one is. A cancel changes nothing.
func (w *Window) chooseDownloads() {
	p := w.set
	start := strings.TrimSpace(p.downloads.Text())
	if start == "" {
		start = w.settings.Downloads
	}
	w.launcher.ChooseFolder(start, func(path string, err error) {
		if err != nil || path == "" || w.closed {
			return
		}
		p.downloads.SetText(path)
		w.applyDownloads()
	})
}

func errorLabel() *gtk.Label {
	l := gtk.NewLabel("")
	l.AddCSSClass("error")
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar) // a path with no break in it wraps inside it
	l.SetMarginTop(6)
	l.SetVisible(false)
	return l
}

// applyDownloads is the folder row's apply: vet the folder as the TUI's
// --downloads is, then keep it, or say why not under the row.
func (w *Window) applyDownloads() {
	p := w.set
	folder, err := settings.CheckDownloads(p.downloads.Text(), settings.ResolverOptions(""))
	if err != nil {
		p.showDownloadsError(err.Error())
		return
	}
	if folder != p.downloads.Text() {
		p.downloads.SetText(folder)
	}
	p.showDownloadsError("")
	next := w.settings
	next.Downloads = folder
	w.saveSettings(next)
}

// saveSettings keeps s for the next Start and writes it. A failed write
// still applies until archmaid-gui closes, and says so.
func (w *Window) saveSettings(s settings.Settings) {
	if w.settings.DryRun && !s.DryRun && !w.busy {
		// Dry run switched off while idle: the sidebar status no longer
		// says DRY RUN about the run before.
		w.dryOff = true
		defer w.setStatus()
	}
	w.settings = s
	if s.DryRun {
		w.dryOff = false
	}
	if err := settings.Save(w.set.path, s); err != nil {
		w.set.showProblem("Not saved (it applies until you close archmaid): " + err.Error())
		return
	}
	w.set.showProblem("")
}

func (p *settingsPage) showDownloadsError(msg string) {
	showError(p.dlError, msg)
	if msg != "" {
		p.downloads.AddCSSClass("error")
	} else {
		p.downloads.RemoveCSSClass("error")
	}
}

func (p *settingsPage) showProblem(msg string) { showError(p.problem, msg) }

func showError(l *gtk.Label, msg string) {
	l.SetText(msg)
	l.SetVisible(msg != "")
}
