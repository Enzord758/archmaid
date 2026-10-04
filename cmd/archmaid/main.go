// Command archmaid is a Bubble Tea TUI for Arch Linux maintenance. It drives
// package-cache cleanup, orphan/broken-package handling, log vacuuming, service
// pruning, and topgrade updates from one menu, with live output, in-TUI sudo,
// and a --dryrun mode.
//
// This is a thin main: the command line, the askpass relay, building the
// Session and the Driver, and running the TUI on them. The TUI itself is
// internal/tui, the only package that may import Bubble Tea, lipgloss or
// bubbles (scripts/check.sh, layering).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/tui"
)

func main() {
	f, code, start := startup(os.Args[1:], os.Stdout, os.Stderr)
	if !start {
		os.Exit(code)
	}
	tui.Configure(f.dryRun, f.downloads)

	// A missing piece of the actions' wiring would otherwise panic mid-action.
	env := tui.ActionEnv()
	if err := env.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "archmaid:", err)
		os.Exit(1)
	}

	srv := tui.StartAskpass() // nil: askpass isn't wired, sudo steps run -n
	defer tui.StopAskpass(srv)

	// The TUI is a front end to the engine: the Driver owns the Session and
	// takes the askpass requests itself.
	var reqs <-chan *privilege.Request
	if srv != nil {
		reqs = srv.Requests()
	}
	drv := tui.NewDriver(engine.New(engine.Options{DryRun: f.dryRun, Env: env}), reqs)
	err := tui.Run(drv)
	drv.Close() // stops what runs and waits for its workers
	// What the Driver reported, then an aborted downloads deletion finishing
	// its current entry and logging what it deleted: don't cut that short.
	tui.Finish(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		tui.StopAskpass(srv) // os.Exit skips the defer: remove the socket and wrapper
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

// flags is the command line: --dryrun and --downloads' folder ("" for none).
type flags struct {
	dryRun    bool
	downloads string
}

// startup is everything main does before the TUI: the askpass relay and the
// command line. It returns start=false with the exit code when the process
// must stop there. It writes nothing under the state dir and deletes nothing:
// history is only rotated when a real run writes its first log entry (see
// internal/tui's logKeepRuns), so --help, a bad flag, the askpass relay or a
// dry run leave ~/.local/state/archmaid as they found it.
func startup(args []string, stdout, stderr io.Writer) (f flags, code int, start bool) {
	// SUDO_ASKPASS helper mode: sudo runs the wrapper, which execs us with
	// --askpass <sock> [prompt]. Relay to the running TUI and exit.
	if len(args) >= 2 && args[0] == "--askpass" {
		prompt := ""
		if len(args) >= 3 {
			prompt = args[2]
		}
		return flags{}, privilege.RunClient(args[1], prompt), false
	}

	f, err := parseArgs(args)
	switch {
	case errors.Is(err, errHelp):
		fmt.Fprint(stdout, usage)
		return flags{}, 0, false
	case err != nil:
		fmt.Fprintln(stderr, "archmaid:", err)
		fmt.Fprintln(stderr, "Try 'archmaid --help'.")
		return flags{}, 2, false
	}
	return f, 0, true
}

// parseArgs reads the command-line flags: --dryrun, and --downloads <dir>
// (or --downloads=<dir>), the folder "Delete old downloads" cleans. --help
// and -h return errHelp. Anything else is an error: an ignored typo used to
// start the TUI, and a mistyped --dryrun would make that a real run.
//
// A --downloads value starting with "-" is refused rather than taken as the
// folder: in `archmaid --downloads --dryrun` the user meant two flags, and
// swallowing --dryrun as a folder name would turn a dry run into a real one.
func parseArgs(args []string) (flags, error) {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dryrun":
			f.dryRun = true
		case a == "--downloads":
			if i+1 >= len(args) {
				return flags{}, errors.New("--downloads needs a folder")
			}
			i++
			if err := checkDownloadsFlag(args[i]); err != nil {
				return flags{}, err
			}
			f.downloads = args[i]
		case strings.HasPrefix(a, "--downloads="):
			v := strings.TrimPrefix(a, "--downloads=")
			if err := checkDownloadsFlag(v); err != nil {
				return flags{}, err
			}
			f.downloads = v
		case a == "--help" || a == "-h":
			return flags{}, errHelp
		case strings.HasPrefix(a, "-"):
			return flags{}, fmt.Errorf("unknown option %q", a)
		default:
			return flags{}, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return f, nil
}

// checkDownloadsFlag vets --downloads' folder.
func checkDownloadsFlag(v string) error {
	switch {
	case v == "":
		return errors.New("--downloads needs a folder")
	case strings.HasPrefix(v, "-"):
		return fmt.Errorf("--downloads needs a folder, not the option %q "+
			"(write a folder whose name starts with \"-\" as %q)", v, "./"+v)
	}
	return nil
}
