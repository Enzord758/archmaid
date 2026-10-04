package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// panicAuth has no cached ticket, and its password check panics: the
// Driver reports the worker's panic, posts nothing, and the Session waits
// for an AuthDone that never comes.
type panicAuth struct{}

func (panicAuth) Probe(context.Context) bool { return false }
func (panicAuth) Authenticate(context.Context, string) (string, error) {
	panic("auth worker boom")
}
func (panicAuth) Refresh(context.Context) {}
func (panicAuth) Command(argv []string, needsPriv bool) proc.Spec {
	return privilege.Sudo{}.Command(argv, needsPriv)
}

// A password check whose worker panicked never resolves, and the password
// screen ignored every key while a check was in flight, ctrl+c included:
// the TUI was stuck on "authenticating…" for good. On the real Driver:
// ctrl+c now aborts the action, and the Session's Finished goes back to
// the menu.
func TestCtrlCLeavesAStuckPasswordCheck(t *testing.T) {
	driverReports.flush(io.Discard)
	defer driverReports.flush(io.Discard)
	probe := actions.Action{ID: "privonly", Label: "Needs root", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{{ID: "p", Title: "Rooting…", LogLabel: "p", Argv: []string{"true"}, NeedsPriv: true}}
	}}
	old := actions.Menu
	actions.Menu = append(slices.Clone(old), probe)
	t.Cleanup(func() { actions.Menu = old })

	d := driver.New(engine.New(engine.Options{Env: actionEnv()}), driver.Deps{
		Priv: panicAuth{}, Log: logs(), Report: driverReports.add,
		Faillock: func() engine.FaillockRead { return engine.FaillockRead{} },
	})
	defer d.Close()
	m := fixedModel()
	m.eng = newEngineState(d)
	// pump applies the Driver's Events to m until done(m), as the program
	// would (forwardEvents), on this goroutine.
	pump := func(what string, done func(model) bool) {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for !done(m) {
			select {
			case ev := <-d.Events():
				m = update(m, engineEventsMsg{evs: []engine.Event{ev}})
			case <-timeout:
				t.Fatalf("waiting for %s: screen %d", what, m.screen)
			}
		}
	}

	d.Send(engine.Start{Action: probe.ID})
	pump("the password prompt", func(m model) bool { return m.screen == screenPassword })
	m = update(m, key("pw"))
	m = update(m, key("enter"))
	if !m.authInFlight {
		t.Fatal("enter didn't send the password")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(driverReports.summary(), "auth worker boom") {
		if time.Now().After(deadline) {
			t.Fatalf("the worker's panic was never reported: %q", driverReports.summary())
		}
		time.Sleep(5 * time.Millisecond)
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	pump("the menu", func(m model) bool { return m.screen == screenMenu })
	if m.authInFlight {
		t.Error("still authenticating on the menu")
	}
}

// ctrl+c during an askpass check hangs up on sudo like esc, back to the
// running step (where ctrl+c asks to abort); any other key still waits.
func TestCtrlCLeavesAStuckAskpassCheck(t *testing.T) {
	m, f := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass, Step: "a", Prompt: "pw:"})
	m = update(m, key("enter"))
	if !m.authInFlight {
		t.Fatal("enter didn't send the password")
	}
	n := len(f.sent)
	if m = update(m, key("esc")); !m.authInFlight || len(f.sent) != n {
		t.Fatalf("esc during the check: authInFlight %v, sent %#v", m.authInFlight, f.sent[n:])
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := lastSent(t, f); got != (engine.CancelPrompt{Prompt: 7}) {
		t.Errorf("ctrl+c sent %#v, want CancelPrompt on the askpass", got)
	}
	if m.screen != screenRunning || m.authInFlight || m.eng.askpassOpen() {
		t.Errorf("screen %d, authInFlight %v, askpass %d; want the running step", m.screen, m.authInFlight, m.eng.askpass)
	}
}
