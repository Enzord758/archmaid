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

	"github.com/Enzord758/archmaid/internal/privilege"
)

func main() {
	// SUDO_ASKPASS helper mode: sudo runs the wrapper, which execs us with
	// --askpass <sock> [prompt]. Relay to the running TUI and exit.
	if len(os.Args) >= 3 && os.Args[1] == "--askpass" {
		prompt := ""
		if len(os.Args) >= 4 {
			prompt = os.Args[3]
		}
		os.Exit(privilege.RunClient(os.Args[2], prompt))
	}

	if err := parseArgs(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid:", err)
		os.Exit(2)
	}

	pruneLogs() // keep only the most recent logKeepRuns run directories

	srv := startAskpassServer() // nil: askpass isn't wired, sudo steps run -n
	defer stopAskpassServer(srv)

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if srv != nil {
		go func() { // deliver sudo password requests to the model
			for req := range srv.Requests() {
				p.Send(askpassMsg{req: req})
			}
		}()
	}

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
//
// A --downloads value starting with "-" is refused rather than taken as the
// folder: in `archmaid --downloads --dryrun` the user meant two flags, and
// swallowing --dryrun as a folder name would turn a dry run into a real one.
func parseArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dryrun":
			dryRun = true
		case a == "--downloads":
			if i+1 >= len(args) {
				return errors.New("--downloads needs a folder")
			}
			i++
			if err := setDownloadsFlag(args[i]); err != nil {
				return err
			}
		case strings.HasPrefix(a, "--downloads="):
			if err := setDownloadsFlag(strings.TrimPrefix(a, "--downloads=")); err != nil {
				return err
			}
		}
	}
	return nil
}

// setDownloadsFlag vets and sets --downloads' folder.
func setDownloadsFlag(v string) error {
	switch {
	case v == "":
		return errors.New("--downloads needs a folder")
	case strings.HasPrefix(v, "-"):
		return fmt.Errorf("--downloads needs a folder, not the option %q "+
			"(write a folder whose name starts with \"-\" as %q)", v, "./"+v)
	}
	downloadsFlag = v
	return nil
}
