// Command archmaid-gui is archmaid's GTK4/libadwaita front end. It is a
// walking skeleton: it drives the engine's stub Driver, not the real one.
package main

import (
	"os"

	"github.com/Enzord758/archmaid/gui/internal/ui"
)

func main() {
	// Before anything initializes GTK: cairo unless the owner picked a
	// renderer (docs/TESTING.md → GUI measurements).
	ui.DefaultRenderer()
	os.Exit(ui.Main(os.Args))
}
