package ui

import (
	_ "embed"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// archmaid's own symbolic icons (#270): the System health cell's pulse
// line, which the Adwaita theme has no symbolic icon for. A symbolic icon
// follows the text colour (and turns warn when the cell is emphasised).
//
// The icons live in a GResource that is embedded in the binary, registered
// from memory and added to the icon theme as a resource path, so nothing is
// written to disk at run time. icons/icons.gresource is compiled from
// icons/icons.gresource.xml by
//
//	glib-compile-resources --sourcedir=icons --target=icons/icons.gresource icons/icons.gresource.xml
//
// (a developer step, not part of the Go build); a test checks that the
// compiled resource holds the SVG that is checked in.

//go:generate glib-compile-resources --sourcedir=icons --target=icons/icons.gresource icons/icons.gresource.xml

//go:embed icons/icons.gresource
var iconsGResource []byte

//go:embed icons/archmaid-health-symbolic.svg
var healthIconSVG []byte

// iconsResourcePath is the gresource prefix of the icon folders.
const iconsResourcePath = "/org/archmaid/icons"

// loadIconResource parses the embedded gresource.
func loadIconResource() (*gio.Resource, error) {
	return gio.NewResourceFromData(glib.NewBytes(iconsGResource))
}

// installIcons makes the icons above findable by name. A failure leaves
// them out: iconName falls back.
func installIcons() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	res, err := loadIconResource()
	if err != nil {
		return
	}
	gio.ResourcesRegister(res)
	gtk.IconThemeGetForDisplay(display).AddResourcePath(iconsResourcePath)
}

// iconName is name if the icon theme has it, else fallback.
func iconName(name, fallback string) string {
	display := gdk.DisplayGetDefault()
	if display != nil && gtk.IconThemeGetForDisplay(display).HasIcon(name) {
		return name
	}
	return fallback
}
