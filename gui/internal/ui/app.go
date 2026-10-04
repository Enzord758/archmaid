// Package ui is the GTK layer: the only package in the GUI that imports
// gotk4.
package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
)

// AppID is the application id (D-Bus name, .desktop file, icon).
const AppID = "io.github.enzord758.archmaid"

// Main runs the application and returns its exit status.
func Main(args []string) int {
	app := adw.NewApplication(AppID, gio.ApplicationDefaultFlags)
	app.ConnectActivate(func() {
		win := adw.NewApplicationWindow(&app.Application)
		win.SetTitle("archmaid")
		win.SetDefaultSize(960, 640)
		win.Present()
	})
	return app.Run(args)
}
