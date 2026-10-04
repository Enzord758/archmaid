// Command archmaid is a Bubble Tea TUI for Arch Linux maintenance. It drives
// package-cache cleanup, orphan/broken-package handling, log vacuuming, service
// pruning, and topgrade updates from one menu, with live output, in-TUI sudo,
// and a --dryrun mode.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
)

func main() {
	if code, start := startup(os.Args[1:], os.Stdout, os.Stderr); !start {
		os.Exit(code)
	}

	// A missing piece of the actions' wiring would otherwise panic mid-action.
	if err := actionEnv().Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid:", err)
		os.Exit(1)
	}

	srv := startAskpassServer() // nil: askpass isn't wired, sudo steps run -n
	defer stopAskpassServer(srv)

	// The TUI is a front end to the engine: the Driver owns the Session and
	// takes the askpass requests itself.
	var reqs <-chan *privilege.Request
	if srv != nil {
		reqs = srv.Requests()
	}
	drv := newDriver(engine.New(engine.Options{DryRun: dryRun, Env: actionEnv()}), reqs)
	m := initialModel()
	m.eng = newEngineState(drv)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go forwardEvents(drv.Events(), p.Send)

	_, err := p.Run()
	drv.Close() // stops what runs and waits for its workers
	driverReports.flush(os.Stderr)
	// An aborted downloads deletion finishes its current entry and logs what
	// it deleted; don't cut that short by exiting.
	waitForDownloads(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// usage is what --help prints.
const usage = `Usage: archmaid [--dryrun] [--downloads <folder>]

A terminal UI for Arch Linux maintenance.

Options:
  --dryrun              log what each step would run instead of running it
  --downloads <folder>  the folder "Delete old downloads" cleans
                        (also --downloads=<folder>)
  -h, --help            show this help and exit
`

// errHelp is parseArgs' answer to --help or -h: print usage, exit 0.
var errHelp = errors.New("help requested")

// startup is everything main does before the TUI: the askpass relay and the
// command line. It returns start=false with the exit code when the process
// must stop there. It writes nothing under the state dir and deletes nothing:
// history is only rotated when a real run writes its first log entry (see
// logKeepRuns), so --help, a bad flag, the askpass relay or a dry run leave
// ~/.local/state/archmaid as they found it.
func startup(args []string, stdout, stderr io.Writer) (code int, start bool) {
	// SUDO_ASKPASS helper mode: sudo runs the wrapper, which execs us with
	// --askpass <sock> [prompt]. Relay to the running TUI and exit.
	if len(args) >= 2 && args[0] == "--askpass" {
		prompt := ""
		if len(args) >= 3 {
			prompt = args[2]
		}
		return privilege.RunClient(args[1], prompt), false
	}

	switch err := parseArgs(args); {
	case errors.Is(err, errHelp):
		fmt.Fprint(stdout, usage)
		return 0, false
	case err != nil:
		fmt.Fprintln(stderr, "archmaid:", err)
		fmt.Fprintln(stderr, "Try 'archmaid --help'.")
		return 2, false
	}
	return 0, true
}

// parseArgs applies the command-line flags: --dryrun, and --downloads <dir>
// (or --downloads=<dir>), the folder "Delete old downloads" cleans. --help
// and -h return errHelp. Anything else is an error: an ignored typo used to
// start the TUI, and a mistyped --dryrun would make that a real run.
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
		case a == "--help" || a == "-h":
			return errHelp
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown option %q", a)
		default:
			return fmt.Errorf("unexpected argument %q", a)
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
