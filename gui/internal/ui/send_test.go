package ui

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
)

// recDriver records the Commands it accepts. It refuses the first refuse
// Sends (a full queue; a negative refuse: every Send, as a dead Driver's
// does), and its Events end when it is closed. It sends no Event.
type recDriver struct {
	events    chan engine.Event
	closeOnce sync.Once
	mu        sync.Mutex
	refuse    int
	got       []string
}

func newRecDriver(refuse int) *recDriver {
	return &recDriver{events: make(chan engine.Event), refuse: refuse}
}

func (d *recDriver) Events() <-chan engine.Event { return d.events }
func (d *recDriver) Close()                      { d.closeOnce.Do(func() { close(d.events) }) }

func (d *recDriver) Send(c engine.Command) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != 0 {
		d.refuse--
		return false
	}
	d.got = append(d.got, fmt.Sprintf("%#v", c))
	return true
}

func (d *recDriver) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.got...)
}

// sendWindow is a Window with no widgets, only what send and the Driver
// swap use; its Drivers are drivers, in order. It runs no display, so the
// plain `go test` runs it.
func sendWindow(drivers ...engine.Driver) *Window {
	w := &Window{model: runview.NewModel()}
	w.opts.NewDriver = func(engine.Options) engine.Driver {
		d := drivers[0]
		drivers = drivers[1:]
		return d
	}
	w.newDriver()
	return w
}

// runMainLoop runs the default main context (send's retries, the pumps'
// posts) for d.
func runMainLoop(d time.Duration) {
	ctx := glib.MainContextDefault()
	for end := time.Now().Add(d); time.Now().Before(end); {
		for ctx.Iteration(false) {
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// closeSendWindow closes w as closeRequest does (closed first, so the
// Driver's Ended is the window's own) and lets the posted Ended run.
func closeSendWindow(w *Window) {
	w.closed = true
	w.closeDriver()
	runMainLoop(50 * time.Millisecond)
}

// TestSendStaysWithItsDriver: an answer sent as the engine died is
// refused, and the user starts a new run before send would give up (Dev
// 3's finding). The retry must not hand it to the new run's Driver:
// prompt IDs restart per Session, so Confirm{1, yes} would approve the
// new run's gate before the user saw it. The fresh Driver gets nothing.
func TestSendStaysWithItsDriver(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dead, fresh := newRecDriver(-1), newRecDriver(0)
	w := sendWindow(dead, fresh)
	defer closeSendWindow(w)

	w.send(engine.Confirm{Prompt: 1, Yes: true})
	w.replaceDriver() // a Start within the retries' second
	runMainLoop(1200 * time.Millisecond)
	if got := fresh.sent(); len(got) != 0 {
		t.Errorf("the fresh Driver got %q: a command for the dead one", got)
	}
	if len(w.toasted) != 0 {
		t.Errorf("toasts %q: a dropped command isn't the engine being unreachable", w.toasted)
	}
}

// TestSendRetriesOnItsDriver: a full queue is still retried, on the same
// Driver, until it takes the command.
func TestSendRetriesOnItsDriver(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	busy := newRecDriver(3)
	w := sendWindow(busy)
	defer closeSendWindow(w)

	w.send(engine.Confirm{Prompt: 2, Yes: false})
	runMainLoop(300 * time.Millisecond)
	want := fmt.Sprintf("%#v", engine.Confirm{Prompt: 2, Yes: false})
	if got := busy.sent(); len(got) != 1 || got[0] != want {
		t.Errorf("the Driver got %q, want once %q", got, want)
	}
}
