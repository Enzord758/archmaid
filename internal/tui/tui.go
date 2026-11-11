// Package tui is archmaid's terminal front end: a Bubble Tea model on an
// engine.Driver, with its view, styles and what the actions get from it:
// the shared audit and downloads glue (internal/frontend) on the TUI's
// settings, the log store, and sudo with the askpass wrapper. It is the
// only package that may import Bubble Tea,
// lipgloss or bubbles (scripts/check.sh, layering); cmd/archmaid is a thin
// main around the functions in this file.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/ttyguard"
)

// Configure applies the command line: --dryrun and --downloads' folder
// ("" for none). Call it on main's goroutine before anything else here.
func Configure(dry bool, downloads string) {
	dryRun, downloadsFlag = dry, downloads
}

// StartFullCleanup makes the TUI open Full Cleanup at once (-fc,
// --full-cleanup), as if it had been picked in the menu: its gate shows first
// and nothing runs before the user answers. Declining lands on the menu. Call
// it with Configure, before Run.
func StartFullCleanup(on bool) {
	startAction = ""
	if on {
		startAction = actions.IDFullCleanup
	}
}

// startAction is the menu action Init starts at once ("" for none).
var startAction string

// ActionEnv is what the actions get from the TUI; main builds the Session
// with it and checks it with Validate. See actionEnv for what it reads when.
func ActionEnv() actions.Env { return actionEnv() }

// StartAskpass starts the session's askpass Server and wires its wrapper
// up for the sudo steps; nil when askpass can't be wired up (sudo steps
// then run -n). Stop it with StopAskpass after the Driver is closed.
func StartAskpass() *privilege.Server { return startAskpassServer() }

// StopAskpass closes s and unwires its wrapper; nil is a no-op.
func StopAskpass(s *privilege.Server) { stopAskpassServer(s) }

// NewDriver builds the Driver the binary runs on, around s; askpass is the
// askpass Server's requests, nil when it isn't wired up. Call it after
// Configure and StartAskpass, before Run.
func NewDriver(s *engine.Session, askpass <-chan *privilege.Request) engine.Driver {
	return newDriver(s, askpass)
}

// Run runs the TUI on drv, on the alt screen, until the user quits. It
// doesn't close drv: main does, then calls Finish.
//
// While it runs, the terminal it reads keys from (stdin) is held exclusive
// (internal/ttyguard): a step's child that opens /dev/tty — git or ssh
// asking for credentials — is refused instead of reading the same keys as
// the TUI. Bubble Tea reads stdin, which is already open; with a stdin that
// isn't a terminal Bubble Tea opens /dev/tty itself, and nothing is held.
func Run(drv engine.Driver) error {
	g, err := ttyguard.Hold(os.Stdin)
	if err != nil {
		driverReports.add(fmt.Errorf("tui: can't keep step commands off the terminal: %w", err))
	}
	termGuard.Store(g)
	defer func() {
		termGuard.Store(nil)
		if err := g.Release(); err != nil {
			driverReports.add(fmt.Errorf("tui: can't release the terminal: %w", err))
		}
	}()
	m := initialModel()
	m.eng = newEngineState(drv)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go forwardEvents(drv.Events(), p.Send)
	// Bubble Tea ends the program on SIGINT and SIGTERM; SIGHUP (the
	// terminal closing, or a kill -HUP) quits it too, instead of killing
	// the process, so main's exit path (Finish, DropSudoTicket) still runs.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer func() {
		signal.Stop(hup) // first: no signal is sent on the closed channel
		close(hup)
	}()
	go func() {
		if _, ok := <-hup; ok {
			p.Quit()
		}
	}()
	_, err = p.Run()
	return err
}

// DropSudoTicket runs `sudo -k` once the TUI is done, however it ended
// (q, a signal, an error), so the ticket it primed and kept alive doesn't
// outlive it: the next sudo in that terminal asks again (the owner's
// decision M1). It never prompts and never holds the exit up (see
// privilege.DropTicket); a failure is only reported. A dry run primed no
// ticket and runs no sudo, so it leaves the ticket alone.
func DropSudoTicket(w io.Writer) {
	if dryRun {
		return
	}
	if err := privilege.DropTicket(context.Background()); err != nil {
		fmt.Fprintln(w, "archmaid: couldn't drop the sudo ticket (sudo -k):", err)
	}
}

// termGuard is the terminal Run holds, for editConfig to pause around the
// editor; nil outside Run.
var termGuard atomic.Pointer[ttyguard.Guard]

// DisableCoreDumps marks the process non-dumpable (PR_SET_DUMPABLE 0):
// the password typed into the TUI lives in Go strings until the GC reuses
// their memory (docs/HANDOVER.md), and a crash would otherwise write it to
// a core file the user can read back (systemd-coredump, coredumpctl).
// Being non-dumpable also makes the process's /proc entries root's, so
// other processes of the user can't ptrace it or read its memory.
//
// main calls it once it knows it runs the TUI, never in the --askpass
// relay: the askpass Server reads its client's /proc/<pid>/exe to verify
// it, which only works while the client stays dumpable. Nothing reads the
// TUI's own /proc from another unprivileged process (the Server reads its
// clients', sudo's and the step's; children are dumpable again after
// exec), and the process can still read its own /proc/self.
func DisableCoreDumps() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// Finish runs once the program has ended and the Driver is closed: it
// writes what the Driver reported to w (the alt screen owned the terminal
// until now), then waits for an aborted downloads deletion to finish its
// current entry and log what it deleted.
func Finish(w io.Writer) {
	driverReports.flush(w)
	waitForDownloads(w)
}
