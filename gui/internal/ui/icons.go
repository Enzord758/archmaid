package ui

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/gui/internal/overview"
)

// archmaid's own symbolic icons (#270): the System health cell's pulse
// line, which the Adwaita theme has no symbolic icon for. A symbolic icon
// follows the text colour (and turns warn when the cell is emphasised), so
// it is a "-symbolic.svg" in an icon theme folder: written once to the
// user's cache folder (0700/0600, only when it differs) and added to the
// icon theme's search path.

//go:embed icons/archmaid-health-symbolic.svg
var healthIconSVG []byte

// ownIcons are the icon names archmaid supplies and their files.
var ownIcons = map[string][]byte{overview.IconHealth: healthIconSVG}

// installIcons makes the icons above findable by name. A failure (no cache
// folder, read-only home) leaves them out: iconName falls back.
func installIcons() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	base, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(base) {
		return
	}
	root := filepath.Join(base, "archmaid", "icons")
	dir := filepath.Join(root, "hicolor", "scalable", "status")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	for name, svg := range ownIcons {
		path := filepath.Join(dir, name+".svg")
		if cur, err := os.ReadFile(path); err == nil && string(cur) == string(svg) {
			continue
		}
		if err := os.WriteFile(path, svg, 0o600); err != nil {
			return
		}
	}
	gtk.IconThemeGetForDisplay(display).AddSearchPath(root)
}

// iconName is name if the icon theme has it, else fallback.
func iconName(name, fallback string) string {
	display := gdk.DisplayGetDefault()
	if display != nil && gtk.IconThemeGetForDisplay(display).HasIcon(name) {
		return name
	}
	return fallback
}
