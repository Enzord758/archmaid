package runview

import (
	"slices"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Enzord758/archmaid/internal/engine"
)

// dyingDriver is a Driver that stops on its own mid-run, as the real one
// does after a Session panic: its events end without a Finished.
type dyingDriver struct{ ch chan engine.Event }

func (d dyingDriver) Events() <-chan engine.Event { return d.ch }
func (dyingDriver) Send(engine.Command) bool      { return false }
func (dyingDriver) Close()                        {}

// TestPumpEndedWithoutFinished: the Driver's events end mid-run, a step
// running and its confirm open. The pump runs Ended once, on the main
// loop, after the last batch; Model.Stop then ends the action: the
// prompt closes, the running row reads aborted and the pending one not
// run, the status and a toast say the engine stopped, and nothing reads
// as a Finished outcome.
func TestPumpEndedWithoutFinished(t *testing.T) {
	defer goleak.VerifyNone(t)
	l := newLoop()
	defer l.close()
	d := dyingDriver{ch: make(chan engine.Event, 8)}
	m := NewModel()
	m.Finished = &engine.Finished{Outcome: engine.OutcomeCompleted} // the run before
	var applied []engine.Event
	var stop *Change
	ended := 0
	p := NewPump(d, l.post, func(evs []engine.Event) {
		if stop != nil {
			t.Error("a batch after Ended")
		}
		applied = append(applied, evs...)
		m.Apply(evs)
	}, PumpOptions{Ended: func() {
		if !l.onLoop() {
			t.Error("Ended ran off the main loop")
		}
		ended++
		c := m.Stop()
		stop = &c
	}})

	d.ch <- engine.QueueStarted{Action: "fullcleanup", Steps: []engine.PlanStep{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}}
	d.ch <- engine.StepStarted{ID: "a", Index: 0, At: time.Now()}
	d.ch <- engine.OutputChunk{ID: "a", Index: 0, Lines: []string{"working"}}
	d.ch <- engine.NeedsConfirm{ID: 7, Kind: engine.ConfirmStep, Step: "a", Msg: "Go on?"}
	close(d.ch)
	<-p.Done()
	waitFor(t, func() bool {
		done := make(chan bool)
		l.post(func() { done <- stop != nil })
		return <-done
	})
	l.do(func() {}) // anything still posted has run

	if ended != 1 || len(applied) != 4 {
		t.Fatalf("Ended ran %d times after %d events, want once after 4", ended, len(applied))
	}
	c := *stop
	if !c.Finished || !c.Status || !slices.Equal(c.Closed, []engine.PromptID{7}) || !slices.Equal(c.Toasts, []string{EngineStopped}) {
		t.Errorf("change %+v", c)
	}
	if m.Running || m.Prompt != nil || m.Finished != nil || m.Stopped != EngineStopped {
		t.Errorf("model: running %v prompt %v finished %v stopped %q", m.Running, m.Prompt, m.Finished, m.Stopped)
	}
	if m.Rows[0].State != RowAborted || m.Rows[1].State != RowNotRun || !slices.Equal(c.Rows, []int{0, 1}) {
		t.Errorf("rows %v %v, changed %v", m.Rows[0].State, m.Rows[1].State, c.Rows)
	}
	if m.Answer(7) {
		t.Error("the closed prompt can still be answered")
	}

	// A fresh run clears it.
	m.Apply([]engine.Event{engine.QueueStarted{Action: "fullcleanup"}})
	if m.Stopped != "" {
		t.Errorf("stopped %q after a new queue", m.Stopped)
	}
}

// TestPumpEndedAfterClose: Ended also runs for a Driver the front end
// closed itself (the window tells the two apart), and never without
// Ended set.
func TestPumpEndedAfterClose(t *testing.T) {
	defer goleak.VerifyNone(t)
	l := newLoop()
	defer l.close()
	d := dyingDriver{ch: make(chan engine.Event)}
	ended := make(chan struct{})
	p := NewPump(d, l.post, func([]engine.Event) {}, PumpOptions{Ended: func() { close(ended) }})
	close(d.ch)
	<-p.Done()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("Ended never ran")
	}
	d2 := dyingDriver{ch: make(chan engine.Event)}
	p2 := NewPump(d2, l.post, func([]engine.Event) {}, PumpOptions{})
	close(d2.ch)
	<-p2.Done()
	l.do(func() {})
}
