// Command archmaid is a Bubble Tea TUI for Arch Linux maintenance. It drives
// package-cache cleanup, orphan/broken-package handling, log vacuuming, service
// pruning, and topgrade updates from one menu, with live output, in-TUI sudo,
// and a --dryrun mode.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// SUDO_ASKPASS helper mode: sudo runs the wrapper, which execs us with
	// --askpass <sock> [prompt]. Relay to the running TUI and exit.
	if len(os.Args) >= 3 && os.Args[1] == "--askpass" {
		prompt := ""
		if len(os.Args) >= 4 {
			prompt = os.Args[3]
		}
		os.Exit(runAskpass(os.Args[2], prompt))
	}

	for _, a := range os.Args[1:] {
		if a == "--dryrun" {
			dryRun = true
		}
	}

	pruneLogs() // drop run logs older than a week

	srv := startAskpassServer()
	defer srv.close()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	go func() { // deliver sudo password requests to the model
		for req := range srv.reqs {
			p.Send(req)
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
