package ui

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/engine"
)

// dyingDriver passes on d's events up to the second step's start, then
// ends its own Events without a Finished, as the real Driver does when it
// closes on its own (a Session panic). Only one Driver per child dies
// (died), so the run after it is a normal one.
type dyingDriver struct {
	engine.Driver
	out  chan engine.Event
	done chan struct{}
}

func newDyingDriver(d engine.Driver, died *atomic.Bool) *dyingDriver {
	x := &dyingDriver{Driver: d, out: make(chan engine.Event, 64), done: make(chan struct{})}
	go func() {
		defer close(x.done)
		defer close(x.out)
		in := d.Events()
		for ev := range in {
			x.out <- ev
			if s, ok := ev.(engine.StepStarted); ok && s.Index == 1 && died.CompareAndSwap(false, true) {
				go d.Close()
				for range in { // drained until the stub's Close ends it
				}
				return
			}
		}
	}()
	return x
}

func (x *dyingDriver) Events() <-chan engine.Event { return x.out }

func (x *dyingDriver) Close() {
	x.Driver.Close()
	<-x.done
}

// TestGUIEngineStops: the Driver's events end mid-run without a Finished.
// The window leaves the running state: not busy, Stop off, the menu row
// back on, no dialog left open, the status line and a toast saying the
// engine stopped, the running row aborted. Closing then closes at once
// (no "Stop the run and quit?"), and a new Start runs on a fresh Driver
// to the end.
func TestGUIEngineStops(t *testing.T) {
	needDisplay(t)
	wantControls := `busy=false stop=false menu=true dialogs=0 status="` + runview.EngineStopped

	rep, _ := child(t, "engine-stops")
	if len(rep.Outcomes) != 1 || rep.Outcomes[0] != "stopped" || !strings.HasPrefix(rep.Controls, wantControls) {
		t.Errorf("outcomes %q controls %s; want stopped, %s…", rep.Outcomes, rep.Controls, wantControls)
	}
	if !contains(rep.Toasts, runview.EngineStopped) || contains(rep.Toasts, "Run completed") {
		t.Errorf("toasts %q", rep.Toasts)
	}
	var aborted, running int
	for _, r := range rep.Rows {
		switch {
		case strings.HasSuffix(r, "=aborted"):
			aborted++
		case strings.HasSuffix(r, "=running"), strings.HasSuffix(r, "=pending"):
			running++
		}
	}
	if aborted != 1 || running != 0 {
		t.Errorf("rows %q: want the running one aborted, none left running or pending", rep.Rows)
	}

	rep, _ = child(t, "engine-stops-rerun")
	if len(rep.Outcomes) != 2 || rep.Outcomes[0] != "stopped" || rep.Outcomes[1] != "completed" {
		t.Errorf("outcomes %q: want stopped, then a completed run", rep.Outcomes)
	}
	if len(rep.DriverOpts) != 3 || rep.DriverCloses != 3 {
		t.Errorf("drivers %q, %d closed: want the window's, the dead one and a fresh one, all closed", rep.DriverOpts, rep.DriverCloses)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
