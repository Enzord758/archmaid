package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/settings"
)

// The Settings page: the downloads folder and dry run, kept in
// $XDG_CONFIG_HOME/archmaid/gui.json (internal/settings). A change applies
// to the next Start: startAction makes a fresh Driver for every Start,
// with w.settings.

type settingsPage struct {
	path      string
	downloads *adw.EntryRow
	dryRun    *adw.SwitchRow
	// dlError is the downloads folder's inline error; problem the file's
	// (unreadable, unsaved). Both hidden when there is nothing to say.
	dlError, problem *gtk.Label
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
		w.settings = settings.Settings{DryRun: s.DryRun}
	} else {
		w.settings = settings.Settings{DryRun: s.DryRun, Downloads: folder}
	}

	page := adw.NewPreferencesPage()
	runs := adw.NewPreferencesGroup()
	runs.SetTitle("Runs")
	runs.SetDescription("Changes apply to the next run.")

	p.dryRun = adw.NewSwitchRow()
	p.dryRun.SetTitle("Dry run")
	p.dryRun.SetSubtitle("Show what each action would do and change nothing")
	p.dryRun.SetActive(s.DryRun)
	p.dryRun.NotifyProperty("active", func() {
		next := w.settings
		next.DryRun = p.dryRun.Active()
		w.saveSettings(next)
	})
	runs.Add(p.dryRun)
	page.Add(runs)

	dl := adw.NewPreferencesGroup()
	dl.SetTitle("Delete Old Downloads")
	dl.SetDescription("The folder it cleans. Leave it empty to find it: $ARCHMAID_DOWNLOADS, " +
		"XDG_DOWNLOAD_DIR, ~/Downloads, ~/Download. Never your home folder or a folder above it.")
	p.downloads = adw.NewEntryRow()
	p.downloads.SetTitle("Downloads folder")
	p.downloads.SetShowApplyButton(true)
	p.downloads.SetText(s.Downloads)
	p.downloads.ConnectApply(w.applyDownloads)
	p.downloads.ConnectChanged(func() { p.showDownloadsError("") })
	dl.Add(p.downloads)
	p.dlError = errorLabel()
	dl.Add(p.dlError)
	p.showDownloadsError(dlErr)
	page.Add(dl)

	p.problem = errorLabel()
	file := adw.NewPreferencesGroup()
	file.SetDescription("Saved in " + p.path)
	file.Add(p.problem)
	page.Add(file)
	if loadErr != nil {
		p.showProblem("Your settings can't be read, so the defaults apply: " + loadErr.Error())
	}
	return page
}

func errorLabel() *gtk.Label {
	l := gtk.NewLabel("")
	l.AddCSSClass("error")
	l.SetXAlign(0)
	l.SetWrap(true)
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
	w.settings = s
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
