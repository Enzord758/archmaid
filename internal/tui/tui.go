// Package tui is archmaid's terminal front end: a Bubble Tea model on an
// engine.Driver, with its view, styles and the glue the actions get from
// it (the AUR audit, the downloads delete, the log store, sudo with the
// askpass wrapper). It is the only package that may import Bubble Tea,
// lipgloss or bubbles (scripts/check.sh, layering); cmd/archmaid is a thin
// main around the functions in this file.
package tui

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
)

// Configure applies the command line: --dryrun and --downloads' folder
// ("" for none). Call it on main's goroutine before anything else here.
func Configure(dry bool, downloads string) {
	dryRun, downloadsFlag = dry, downloads
}

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
func Run(drv engine.Driver) error {
	m := initialModel()
	m.eng = newEngineState(drv)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go forwardEvents(drv.Events(), p.Send)
	_, err := p.Run()
	return err
}

// Finish runs once the program has ended and the Driver is closed: it
// writes what the Driver reported to w (the alt screen owned the terminal
// until now), then waits for an aborted downloads deletion to finish its
// current entry and log what it deleted.
func Finish(w io.Writer) {
	driverReports.flush(w)
	waitForDownloads(w)
}
