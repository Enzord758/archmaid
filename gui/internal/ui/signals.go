package ui

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/Enzord758/archmaid/internal/engine"
)

// quitSignals end archmaid-gui the way Stop and Quit does: ctrl+c in the
// terminal it was started from, a plain kill, the terminal closing.
var quitSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// recordWait bounds how long a second signal waits for the run's aborted
// record before exiting.
const recordWait = 5 * time.Second

// aborter is a Driver that says when Close has aborted the run and
// written its last-run record (driver.Driver.Aborted).
type aborter interface{ Aborted() <-chan struct{} }

// killer is a Driver that can SIGKILL, at once, everything its run
// started (driver.Driver.KillNow).
type killer interface{ KillNow() }

// cleanupWait bounds how long the second signal's exit waits for
// atExit: the forced exit must not hang on it.
const cleanupWait = time.Second

// signals carries quitSignals for one Run: the first is Stop and Quit
// without asking (quitOnSignal); a second, while the window is still
// closing, exits at once: as soon as the first's aborted record is written
// (or after recordWait) it SIGKILLs what the run started, runs atExit and
// exits. stop ends it; code is 128 + the first signal's number once one
// came, else 0.
type signals struct {
	ch      chan os.Signal
	closing chan engine.Driver // the Driver the first signal closes; nil: none
	done    chan struct{}
	code    atomic.Int32
	// atExit is what the forced exit must still undo, since no deferred
	// cleanup runs past os.Exit (Options.AtForcedExit: the askpass
	// Server's directory); nil for nothing.
	atExit func()
	exit   func(int) // os.Exit; a test's stand-in
}

// catchSignals starts catching quitSignals for app's window *w (nil until
// activated). atExit runs on the second signal's forced exit. Call stop
// once app.Run has returned.
func catchSignals(app *adw.Application, w **Window, atExit func()) *signals {
	s := &signals{
		ch:      make(chan os.Signal, 2),
		closing: make(chan engine.Driver, 1),
		done:    make(chan struct{}),
		atExit:  atExit,
		exit:    os.Exit,
	}
	signal.Notify(s.ch, quitSignals...)
	go s.run(app, w)
	return s
}

func (s *signals) run(app *adw.Application, w **Window) {
	first := true
	for {
		select {
		case <-s.done:
			return
		case sig := <-s.ch:
			if !first {
				s.exitNow(sig)
				return
			}
			first = false
			if n, ok := sig.(syscall.Signal); ok {
				s.code.Store(128 + int32(n))
			}
			post(func() {
				win := *w
				if win == nil { // not activated yet: nothing runs
					s.closing <- nil
					app.Quit()
					return
				}
				s.closing <- win.drv
				win.quitOnSignal() // a no-op if it is closing already
			})
		}
	}
}

// exitNow is the second signal: once the first's record is written, it
// SIGKILLs everything the run started, without Stop's grace, so nothing is
// left running (KillNow also wipes the kept password), runs atExit (for at
// most cleanupWait) and exits with the second signal's status.
func (s *signals) exitNow(sig os.Signal) {
	t := time.NewTimer(recordWait)
	defer t.Stop()
	select {
	case d := <-s.closing:
		select {
		case <-abortedOf(d):
		case <-t.C:
		}
		if k, ok := d.(killer); ok {
			k.KillNow()
		}
	case <-t.C:
	}
	if s.atExit != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.atExit()
		}()
		select {
		case <-done:
		case <-time.After(cleanupWait):
		}
	}
	code := 1
	if n, ok := sig.(syscall.Signal); ok {
		code = 128 + int(n)
	}
	s.exit(code)
}

// stop stops catching signals and returns 128 + the first one's number,
// or 0 when none came.
func (s *signals) stop() int {
	signal.Stop(s.ch)
	close(s.done)
	return int(s.code.Load())
}

// abortedOf is d's Aborted, or an already-closed channel for a Driver
// without one (the stub writes no record; nil is no Driver).
func abortedOf(d any) <-chan struct{} {
	if a, ok := d.(aborter); ok {
		return a.Aborted()
	}
	return closedCh()
}

func closedCh() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// quitOnSignal is Stop and Quit without the question: open dialogs close,
// and the window closes as closeRequest does once the user has confirmed,
// aborting a run in progress (the Driver's Close writes its aborted
// record) and stopping its children.
func (w *Window) quitOnSignal() {
	if w.closed {
		return
	}
	w.quitting = true
	if d := w.quitDialog; d != nil {
		w.quitDialog = nil
		d.ForceClose()
	}
	w.closeRequest()
}
