package ui

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
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

// signals carries quitSignals for one Run: the first is Stop and Quit
// without asking (quitOnSignal); a second, while the window is still
// closing, exits at once, but only once the first's aborted record is
// written (or after recordWait). stop ends it; code is 128 + the first
// signal's number once one came, else 0.
type signals struct {
	ch       chan os.Signal
	recorded chan (<-chan struct{}) // the closing Driver's Aborted, once known
	done     chan struct{}
	code     atomic.Int32
}

// catchSignals starts catching quitSignals for app's window *w (nil until
// activated). Call stop once app.Run has returned.
func catchSignals(app *adw.Application, w **Window) *signals {
	s := &signals{
		ch:       make(chan os.Signal, 2),
		recorded: make(chan (<-chan struct{}), 1),
		done:     make(chan struct{}),
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
					s.recorded <- closedCh()
					app.Quit()
					return
				}
				s.recorded <- abortedOf(win.drv)
				win.quitOnSignal() // a no-op if it is closing already
			})
		}
	}
}

// exitNow is the second signal: exit as soon as the first's record is
// written, with the second signal's status.
func (s *signals) exitNow(sig os.Signal) {
	t := time.NewTimer(recordWait)
	defer t.Stop()
	select {
	case rec := <-s.recorded:
		select {
		case <-rec:
		case <-t.C:
		}
	case <-t.C:
	}
	code := 1
	if n, ok := sig.(syscall.Signal); ok {
		code = 128 + int(n)
	}
	os.Exit(code)
}

// stop stops catching signals and returns 128 + the first one's number,
// or 0 when none came.
func (s *signals) stop() int {
	signal.Stop(s.ch)
	close(s.done)
	return int(s.code.Load())
}

// abortedOf is d's Aborted, or an already-closed channel for a Driver
// without one (the stub writes no record).
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
