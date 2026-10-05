// Command archmaid-gui is archmaid's GTK4/libadwaita front end. It drives
// the real engine Driver on the polkit path; only --stub runs it on the stub
// Driver instead, which touches nothing (no environment variable does).
package main

import (
	"fmt"
	"os"

	"github.com/Enzord758/archmaid/gui/internal/realdriver"
	"github.com/Enzord758/archmaid/gui/internal/ui"
)

func main() {
	// SUDO_ASKPASS helper mode, first and before GTK: topgrade's sudo runs
	// the wrapper, which execs us with --askpass <sock> [prompt]. Relay to
	// the running window and exit (the TUI's main does the same).
	if code, relayed := realdriver.Relay(os.Args[1:]); relayed {
		os.Exit(code)
	}
	// The GUI may hold the run's password: a crash must write no core.
	// (Not PR_SET_DUMPABLE: the desktop portal needs us dumpable.)
	if err := ui.NoCoreFiles(); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid-gui: can't turn core files off:", err)
	}
	// Before anything initializes GTK: cairo unless the owner picked a
	// renderer (docs/TESTING.md → GUI measurements).
	ui.DefaultRenderer()
	os.Exit(ui.Main(os.Args))
}
