package stub

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/steps"
)

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// next reads one event, failing the test if none comes within a second (a
// stuck replay) or the stream is closed.
func next(t *testing.T, d *Driver) engine.Event {
	t.Helper()
	select {
	case ev, ok := <-d.Events():
		if !ok {
			t.Fatal("event stream closed early")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event within 1s")
	}
	return nil
}

func send(t *testing.T, d *Driver, c engine.Command) {
	t.Helper()
	if !d.Send(c) {
		t.Fatalf("Send(%#v) = false", c)
	}
}

// expect reads len(want) events and compares them.
func expect(t *testing.T, d *Driver, want ...engine.Event) {
	t.Helper()
	for i, w := range want {
		if got := next(t, d); !reflect.DeepEqual(got, w) {
			t.Fatalf("event %d: got %#v, want %#v", i, got, w)
		}
	}
}

// closeDrained closes d and checks Events ends.
func closeDrained(t *testing.T, d *Driver) {
	t.Helper()
	d.Close()
	d.Close() // twice is fine
	for range d.Events() {
	}
	if d.Send(engine.Abort{}) {
		t.Error("Send after Close = true")
	}
}

func TestReplayFullCleanupInOrder(t *testing.T) {
	script := FullCleanup(t0)
	clock := NewFakeClock(t0)
	d := New(script, Options{Clock: clock})
	defer closeDrained(t, d)

	send(t, d, engine.Start{Action: "fullcleanup"})
	var got []engine.Event
	for range script {
		ev := next(t, d)
		got = append(got, ev)
		if id := promptID(ev); id != 0 {
			send(t, d, engine.Confirm{Prompt: id, Yes: true})
		}
	}
	if want := script.Events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay differs from the script:\n got %v\nwant %v", got, want)
	}
	if clock.Now() != t0.Add(script.Duration()) {
		t.Errorf("clock at %v, want start + %v", clock.Now(), script.Duration())
	}
	enginetest.CheckContract(t, got)
}

// A script with two prompts and a marker after each.
func twoPrompts() Script {
	return Script{
		{Event: engine.NeedsConfirm{ID: 1, Msg: "first?"}},
		{Event: engine.Notice{Text: "after first"}},
		{Event: engine.NeedsConfirm{ID: 2, Msg: "second?"}},
		{Event: engine.Notice{Text: "after second"}},
		{Event: engine.Finished{Action: "x", Outcome: engine.OutcomeNotice}},
	}
}

func TestStaleAnswersIgnored(t *testing.T) {
	d := New(twoPrompts(), Options{Clock: NewFakeClock(t0)})
	defer closeDrained(t, d)

	send(t, d, engine.Confirm{Prompt: 1, Yes: true}) // before Start: ignored
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsConfirm{ID: 1, Msg: "first?"})
	// Answers for prompts that aren't open change nothing; the abort behind
	// them proves prompt 1 was still open when they were read.
	send(t, d, engine.Confirm{Prompt: 2, Yes: true})
	send(t, d, engine.SubmitPassword{Prompt: 99, Password: "pw"})
	send(t, d, engine.Select{Prompt: 0})
	send(t, d, engine.CancelPrompt{Prompt: 3})
	send(t, d, engine.Start{Action: "y"}) // a Start while it plays: ignored
	send(t, d, engine.Abort{})
	expect(t, d,
		engine.PromptClosed{ID: 1},
		engine.Finished{Action: "x", Outcome: engine.OutcomeCancelled},
		engine.PhaseChanged{Phase: engine.PhaseIdle})

	// The next Start replays from the top, and every answer kind closes its
	// prompt.
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsConfirm{ID: 1, Msg: "first?"})
	send(t, d, engine.CancelPrompt{Prompt: 1})
	expect(t, d, engine.Notice{Text: "after first"}, engine.NeedsConfirm{ID: 2, Msg: "second?"})
	send(t, d, engine.Confirm{Prompt: 2, Yes: false})
	expect(t, d, engine.Notice{Text: "after second"}, engine.Finished{Action: "x", Outcome: engine.OutcomeNotice})
}

func TestPasswordAndSelectionAnswers(t *testing.T) {
	d := New(Script{
		{Event: engine.NeedsPassword{ID: 4}},
		{Event: engine.NeedsSelection{ID: 5, Items: []string{"a.service"}}},
		{Event: engine.OutputReport{ID: 6, Text: "report"}},
		{Event: engine.Finished{Outcome: engine.OutcomeReport}},
	}, Options{Clock: NewFakeClock(t0)})
	defer closeDrained(t, d)

	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.NeedsPassword{ID: 4})
	send(t, d, engine.SubmitPassword{Prompt: 4, Password: "hunter2"})
	expect(t, d, engine.NeedsSelection{ID: 5, Items: []string{"a.service"}})
	send(t, d, engine.Select{Prompt: 5, Picked: []string{"a.service"}})
	expect(t, d, engine.OutputReport{ID: 6, Text: "report"})
	send(t, d, engine.Confirm{Prompt: 6, Yes: true})
	expect(t, d, engine.Finished{Outcome: engine.OutcomeReport})
}

// An abort during a mid-step askpass prompt withdraws the prompt, aborts the
// step with its elapsed time on the clock, and finishes the run.
func TestAbortAtPromptInRunningStep(t *testing.T) {
	plan := []engine.PlanStep{{ID: steps.IDTopgrade, Title: "Updating…"}}
	d := New(Script{
		{Event: engine.QueueStarted{Action: "topgrade", Steps: plan, Started: t0}},
		{Event: engine.StepStarted{ID: steps.IDTopgrade, At: t0}},
		{After: 2 * time.Second, Event: engine.NeedsPassword{ID: 1, Kind: engine.PasswordAskpass, Step: steps.IDTopgrade}},
		{Event: engine.StepDone{ID: steps.IDTopgrade, Status: steps.OK}},
	}, Options{Clock: NewFakeClock(t0)})
	defer closeDrained(t, d)

	send(t, d, engine.Start{Action: "topgrade"})
	expect(t, d,
		engine.QueueStarted{Action: "topgrade", Steps: plan, Started: t0},
		engine.StepStarted{ID: steps.IDTopgrade, At: t0},
		engine.NeedsPassword{ID: 1, Kind: engine.PasswordAskpass, Step: steps.IDTopgrade})
	send(t, d, engine.Abort{})
	expect(t, d,
		engine.PromptClosed{ID: 1},
		engine.Aborted{ID: steps.IDTopgrade, Index: 0, Elapsed: 2 * time.Second},
		engine.Finished{Action: "topgrade", Outcome: engine.OutcomeAborted, Elapsed: 2 * time.Second},
		engine.PhaseChanged{Phase: engine.PhaseIdle})
}

// stuckClock never fires, so the replay sits in a cue's wait.
type stuckClock struct{}

func (stuckClock) Now() time.Time                       { return t0 }
func (stuckClock) After(time.Duration) <-chan time.Time { return nil }

func TestAbortWhileWaiting(t *testing.T) {
	d := New(Script{
		{Event: engine.StepStarted{ID: "s"}},
		{After: time.Hour, Event: engine.StepDone{ID: "s", Status: steps.OK}},
	}, Options{Clock: stuckClock{}})
	defer closeDrained(t, d)

	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.StepStarted{ID: "s"})
	send(t, d, engine.Confirm{Prompt: 1}) // no prompt open: ignored
	send(t, d, engine.Abort{})
	expect(t, d,
		engine.Aborted{ID: "s"},
		engine.Finished{Action: "x", Outcome: engine.OutcomeAborted},
		engine.PhaseChanged{Phase: engine.PhaseIdle})
}

// Send never blocks: with nobody reading Events the queue fills and Send
// says so, at once.
func TestSendNeverBlocks(t *testing.T) {
	d := New(FullCleanup(t0), Options{Clock: NewFakeClock(t0), Queue: 2})
	done := make(chan int)
	go func() {
		n := 0
		for d.Send(engine.Start{Action: "fullcleanup"}) {
			n++
			if n > 100 {
				break
			}
		}
		done <- n
	}()
	select {
	case n := <-done:
		// The loop takes at most one Start off the queue before it blocks
		// publishing to a reader that never comes.
		if n < 2 || n > 3 {
			t.Errorf("Send accepted %d commands with a queue of 2", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked")
	}
	closeDrained(t, d)
}

// Close while the replay is blocked publishing, or idle, returns and closes
// Events.
func TestCloseUnblocks(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint("started=", started), func(t *testing.T) {
			d := New(FullCleanup(t0), Options{Clock: NewFakeClock(t0)})
			if started {
				send(t, d, engine.Start{Action: "fullcleanup"})
			}
			closed := make(chan struct{})
			go func() { d.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("Close blocked")
			}
			closeDrained(t, d)
		})
	}
}

// Without a Clock the wall clock paces the replay.
func TestWallClock(t *testing.T) {
	d := New(Script{{After: time.Millisecond, Event: engine.Notice{Text: "hi"}}}, Options{})
	defer closeDrained(t, d)
	begin := time.Now()
	send(t, d, engine.Start{Action: "x"})
	expect(t, d, engine.Notice{Text: "hi"})
	if time.Since(begin) < time.Millisecond {
		t.Error("the wall clock didn't wait")
	}
}
