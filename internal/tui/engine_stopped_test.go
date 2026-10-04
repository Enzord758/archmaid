package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/Enzord758/archmaid/internal/engine"
)

// closingDriver is a Driver whose Events the test feeds and then closes,
// the way internal/driver closes them after a Session panic: without a
// Finished. Like a closed Driver it refuses every Command.
type closingDriver struct{ evs chan engine.Event }

func (d *closingDriver) Events() <-chan engine.Event { return d.evs }
func (d *closingDriver) Send(engine.Command) bool    { return false }
func (d *closingDriver) Close()                      {}

// A Driver that closes on its own mid-run (a Session panic: reported, then
// Events closed without a Finished) used to leave the TUI on the running
// screen for good: ctrl+c only opens the abort confirm, and the Abort it
// sends goes to a closed Driver. Now the model says the engine stopped,
// with the report's first line, and any key quits; the full report stays
// for main to print.
func TestEngineStoppedMidRun(t *testing.T) {
	driverReports.flush(io.Discard)
	defer driverReports.flush(io.Discard)

	d := &closingDriver{evs: make(chan engine.Event)}
	m := fixedModel()
	m.eng = newEngineState(d)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	go forwardEvents(d.Events(), tm.Send)

	d.evs <- engine.QueueStarted{Action: "q", Steps: []engine.PlanStep{
		{ID: "a", Title: "Doing a…", LogLabel: "a done", Argv: []string{"a"}},
	}}
	d.evs <- engine.StepStarted{ID: "a", Index: 0}
	waitForText(t, tm, "Doing a…")

	driverReports.add(errors.New("driver: the engine panicked: boom\ngoroutine 1 [running]:"))
	close(d.evs)
	waitForText(t, tm, "engine stopped unexpectedly")

	tm.Send(key("x"))
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(model)
	if fm.screen != screenNotice || fm.bg != screenRunning {
		t.Errorf("screen %d over %d, want the notice over the running screen", fm.screen, fm.bg)
	}
	want := "archmaid's engine stopped unexpectedly: driver: the engine panicked: boom"
	if fm.notice != want {
		t.Errorf("notice %q, want %q", fm.notice, want)
	}
	if v := fm.View(); !strings.Contains(v, "any key to quit") {
		t.Errorf("the notice doesn't say a key quits:\n%s", v)
	}
	var stderr bytes.Buffer
	driverReports.flush(&stderr)
	if !strings.Contains(stderr.String(), "archmaid: driver: the engine panicked: boom\ngoroutine 1") {
		t.Errorf("the full report is gone: stderr %q", stderr.String())
	}
}

// The stop closes whatever prompt was open and shows the notice over the
// screen under it; a key then quits instead of answering the dead prompt.
func TestEngineStoppedClosesPrompts(t *testing.T) {
	m, f := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass, Step: "a", Prompt: "pw:"})
	if m.screen != screenPassword {
		t.Fatalf("askpass didn't open: screen %d", m.screen)
	}
	m = update(m, engineStoppedMsg{why: "no error was reported"})
	if m.screen != screenNotice || m.bg != screenRunning || m.eng.askpassOpen() || m.eng.prompt != 0 {
		t.Fatalf("screen %d over %d, askpass %d, prompt %d", m.screen, m.bg, m.eng.askpass, m.eng.prompt)
	}
	n := len(f.sent)
	_, cmd := m.Update(key("enter"))
	if len(f.sent) != n {
		t.Errorf("enter after the stop sent %#v", f.sent[n:])
	}
	if cmd == nil {
		t.Fatal("enter after the stop doesn't quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("enter after the stop doesn't quit")
	}

	// a model without a Driver ignores the msg
	var bare model
	if got := update(bare, engineStoppedMsg{}); got.screen != bare.screen {
		t.Errorf("a model without a Driver moved to screen %d", got.screen)
	}
}

// summary is the newest report's first line, clipped, and forgets nothing.
func TestReportSummary(t *testing.T) {
	var r reportLog
	if got := r.summary(); got != "no error was reported" {
		t.Errorf("empty: %q", got)
	}
	r.add(errors.New("first"))
	r.add(errors.New("second\nstack"))
	if got := r.summary(); got != "second" {
		t.Errorf("summary %q, want the newest report's first line", got)
	}
	r.add(errors.New(strings.Repeat("x", 100)))
	if got := r.summary(); len([]rune(got)) > 60 {
		t.Errorf("not clipped: %q", got)
	}
	var b bytes.Buffer
	r.flush(&b)
	if strings.Count(b.String(), "archmaid: ") != 3 {
		t.Errorf("summary forgot reports: %q", b.String())
	}
}
