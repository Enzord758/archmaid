package ui

import (
	"context"
	"errors"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Launcher opens files and folders in the desktop's apps and asks for a
// folder (P8: Open folder, Open in editor, Choose…). It is a seam: the
// window uses Options.Launcher, which the headless harness replaces with a
// recorder that opens nothing, and gtkLauncher otherwise.
type Launcher interface {
	// OpenFile opens path in the desktop's default app for it.
	OpenFile(path string) error
	// OpenFolder opens the folder path in the file manager.
	OpenFolder(path string) error
	// OpenURI opens a web address in the desktop's browser (only ever
	// https://archlinux.org/…: health.go checks before it asks).
	OpenURI(uri string) error
	// ChooseFolder asks for a folder, starting at start ("" for the
	// dialog's default), and calls done on the main thread with the chosen
	// path, or an error (cancelled included).
	ChooseFolder(start string, done func(string, error))
}

// gtkLauncher is the real Launcher: gtk.FileLauncher and gtk.FileDialog,
// parented to the window. Opening is asynchronous: an error that comes
// back later goes to report (the window's toast).
type gtkLauncher struct {
	parent *gtk.Window
	report func(error)
}

var errNoPath = errors.New("no path to open")

func (l gtkLauncher) launch(path string) error {
	if path == "" {
		return errNoPath
	}
	fl := gtk.NewFileLauncher(gio.NewFileForPath(path))
	fl.Launch(context.Background(), l.parent, func(res gio.AsyncResulter) {
		if err := fl.LaunchFinish(res); err != nil && l.report != nil {
			l.report(err)
		}
	})
	return nil
}

func (l gtkLauncher) OpenURI(uri string) error {
	if uri == "" {
		return errNoPath
	}
	ul := gtk.NewURILauncher(uri)
	ul.Launch(context.Background(), l.parent, func(res gio.AsyncResulter) {
		if err := ul.LaunchFinish(res); err != nil && l.report != nil {
			l.report(err)
		}
	})
	return nil
}

func (l gtkLauncher) OpenFile(path string) error   { return l.launch(path) }
func (l gtkLauncher) OpenFolder(path string) error { return l.launch(path) }

func (l gtkLauncher) ChooseFolder(start string, done func(string, error)) {
	fd := gtk.NewFileDialog()
	if start != "" {
		fd.SetInitialFolder(gio.NewFileForPath(start))
	}
	fd.SelectFolder(context.Background(), l.parent, func(res gio.AsyncResulter) {
		f, err := fd.SelectFolderFinish(res)
		if err != nil {
			done("", err)
			return
		}
		done(f.Path(), nil)
	})
}
