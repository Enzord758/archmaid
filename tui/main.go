// Command archmaid is a Bubble Tea TUI for Arch Linux maintenance. It drives
// package-cache cleanup, orphan/broken-package handling, log vacuuming, service
// pruning, and topgrade updates from one menu, with live output, in-TUI sudo,
// and a --dryrun mode.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

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

	if err := parseArgs(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid:", err)
		os.Exit(2)
	}

	pruneLogs() // keep only the most recent logKeepRuns run directories

	srv := startAskpassServer()
	defer srv.close()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	go func() { // deliver sudo password requests to the model
		for req := range srv.reqs {
			p.Send(req)
		}
	}()

	_, err := p.Run()
	// An aborted downloads deletion finishes its current entry and logs what
	// it deleted; don't cut that short by exiting.
	waitForDownloads(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// parseArgs applies the command-line flags: --dryrun, and --downloads <dir>
// (or --downloads=<dir>), the folder "Delete old downloads" cleans. Anything
// else is ignored.
func parseArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dryrun":
			dryRun = true
		case a == "--downloads":
			if i+1 >= len(args) || args[i+1] == "" {
				return errors.New("--downloads needs a folder")
			}
			i++
			downloadsFlag = args[i]
		case strings.HasPrefix(a, "--downloads="):
			if downloadsFlag = strings.TrimPrefix(a, "--downloads="); downloadsFlag == "" {
				return errors.New("--downloads needs a folder")
			}
		}
	}
	return nil
}
