package main

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// fakeDriver records what the model Sends.
type fakeDriver struct{ sent []engine.Command }

func (f *fakeDriver) Events() <-chan engine.Event { return nil }
func (f *fakeDriver) Send(c engine.Command) bool  { f.sent = append(f.sent, c); return true }
func (f *fakeDriver) Close()                      {}

// engineModel is a sized model on a fakeDriver.
func engineModel() (model, *fakeDriver) {
	f := &fakeDriver{}
	m := sizedModel()
	m.eng = newEngineState(f)
	return m, f
}

func feed(m model, evs ...engine.Event) model { return update(m, engineEventsMsg{evs: evs}) }

// a two-step queue, the first running: the sudo step "b" behind it.
func runningQueue(m model) model {
	return feed(m,
		engine.QueueStarted{Action: "q", Steps: []engine.PlanStep{
			{ID: "a", Title: "Doing a…", LogLabel: "a done", NeedsPriv: true, Argv: []string{"a"}},
			{ID: "b", Title: "Doing b…", LogLabel: "b done", Argv: []string{"b"}},
		}},
		engine.StepStarted{ID: "a", Index: 0},
		engine.OutputChunk{ID: "a", Index: 0, Lines: []string{"hello"}},
	)
}

func lastSent(t *testing.T, f *fakeDriver) engine.Command {
	t.Helper()
	if len(f.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return f.sent[len(f.sent)-1]
}

// The askpass prompt opens over the running step, naming the step and the
// engine's Requester; esc hangs up (CancelPrompt) and, since no Event says
// so, the model closes the popup itself.
func TestEngineAskpassCancel(t *testing.T) {
	m, f := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass, Step: "a",
		Requester: "/usr/bin/pacman (pid 42)", Prompt: "[sudo] password for me: "})
	if m.screen != screenPassword || m.bg != screenRunning {
		t.Fatalf("screen %d over %d, want the password popup over the running step", m.screen, m.bg)
	}
	v := m.View()
	for _, want := range []string{"Doing a…", "/usr/bin/pacman (pid 42)", "[sudo] password for me:"} {
		if !strings.Contains(v, want) {
			t.Errorf("the popup doesn't show %q:\n%s", want, v)
		}
	}
	m = update(m, key("esc"))
	if got := lastSent(t, f); got != (engine.CancelPrompt{Prompt: 7}) {
		t.Errorf("sent %#v", got)
	}
	if m.screen != screenRunning || m.eng.askpassOpen() {
		t.Errorf("after esc: screen %d, askpass open %v", m.screen, m.eng.askpassOpen())
	}
}

// A wrong askpass password: the input locks while it's checked, the
// re-ask shows the error, a right one closes the popup.
func TestEngineAskpassRetry(t *testing.T) {
	m, f := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass, Step: "a", Requester: "x (pid 1)"})
	m.password.SetValue("nope")
	m = update(m, key("enter"))
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.SubmitPassword{Prompt: 7, Password: "nope"}) {
		t.Errorf("sent %#v", got)
	}
	if !m.authInFlight || m.password.Value() != "" {
		t.Errorf("after enter: locked %v, input %q", m.authInFlight, m.password.Value())
	}
	n := len(f.sent)
	m = update(m, key("enter")) // locked: ignored
	if len(f.sent) != n {
		t.Error("a key during the check was sent")
	}
	m = feed(m, engine.AuthWaiting{Kind: engine.WaitPasswordCheck},
		engine.AuthResolved{AttemptsLeft: 2},
		engine.NeedsPassword{ID: 8, Kind: engine.PasswordAskpass, Step: "a", AttemptsLeft: 2,
			Error: "Incorrect password — 2 attempts left"})
	if m.authInFlight || !strings.Contains(m.View(), "Incorrect password — 2 attempts left") {
		t.Errorf("re-ask: locked %v\n%s", m.authInFlight, m.View())
	}
	m = feed(m, engine.AuthWaiting{Kind: engine.WaitPasswordCheck}, engine.AuthResolved{Granted: true})
	if m.screen != screenRunning || m.eng.askpassOpen() {
		t.Errorf("granted: screen %d, askpass open %v", m.screen, m.eng.askpassOpen())
	}
	// withdrawn by the engine (its step ended)
	m = feed(m, engine.NeedsPassword{ID: 9, Kind: engine.PasswordAskpass, Step: "a"}, engine.PromptClosed{ID: 9})
	if m.screen != screenRunning || m.eng.askpassOpen() {
		t.Errorf("withdrawn: screen %d, askpass open %v", m.screen, m.eng.askpassOpen())
	}
}

// ctrl+c, y aborts: Abort is sent, and Finished{OutcomeAborted} after the
// Aborted step goes back to the menu with what never ran skipped.
func TestEngineAbort(t *testing.T) {
	m, f := engineModel()
	m = runningQueue(m)
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.screen != screenAbortConfirm {
		t.Fatalf("ctrl+c: screen %d", m.screen)
	}
	m = update(m, key("y"))
	if got := lastSent(t, f); got != (engine.Abort{}) {
		t.Errorf("sent %#v", got)
	}
	m = feed(m, engine.Aborted{ID: "a", Index: 0}, engine.Finished{Outcome: engine.OutcomeAborted, NotRun: 1})
	if m.screen != screenMenu {
		t.Errorf("screen %d, want the menu", m.screen)
	}
	if a, b := m.queue[0].status, m.queue[1].status; a != steps.Aborted || b != steps.Skipped {
		t.Errorf("statuses %v %v", a, b)
	}
}

// A declined required step ends the queue on the summary, as aborted.
func TestEngineDeclinedRequired(t *testing.T) {
	m, f := engineModel()
	m = feed(m, engine.QueueStarted{Action: "q", Steps: []engine.PlanStep{{ID: "a", Title: "a…", Confirm: true}}},
		engine.NeedsConfirm{ID: 3, Kind: engine.ConfirmStep, Step: "a", Msg: "Do a?"})
	if m.screen != screenConfirm || m.bg != screenRunning || m.gateActive {
		t.Fatalf("screen %d over %d, gate %v", m.screen, m.bg, m.gateActive)
	}
	m = update(m, key("n"))
	if got := lastSent(t, f); got != (engine.Confirm{Prompt: 3, Yes: false}) {
		t.Errorf("sent %#v", got)
	}
	m = feed(m, engine.StepDone{ID: "a", Index: 0, Status: steps.Skipped},
		engine.Finished{Outcome: engine.OutcomeAborted, Skipped: 1})
	if m.screen != screenSummary || !m.aborted || m.skipped != 1 {
		t.Errorf("screen %d, aborted %v, skipped %d", m.screen, m.aborted, m.skipped)
	}
}

// The audit's a accepts (Confirm yes), ReportAccepted shows its notice over
// the report, which Finished leaves up; q on a report dismisses it.
func TestEngineReport(t *testing.T) {
	m, f := engineModel()
	m = feed(m, engine.OutputReport{ID: 5, Text: "1 package flagged", Audit: &engine.AuditReport{CanAccept: true}})
	if m.screen != screenAudit || !strings.Contains(m.View(), "a accept maintainer changes") {
		t.Fatalf("screen %d\n%s", m.screen, m.View())
	}
	m = update(m, key("a"))
	if got := lastSent(t, f); got != (engine.Confirm{Prompt: 5, Yes: true}) {
		t.Errorf("sent %#v", got)
	}
	m = feed(m, engine.ReportAccepted{ID: 5, Saved: true, Text: "Maintainer changes accepted."},
		engine.Finished{Outcome: engine.OutcomeReport})
	if m.screen != screenNotice || m.bg != screenAudit || m.notice != "Maintainer changes accepted." {
		t.Errorf("screen %d over %d, notice %q", m.screen, m.bg, m.notice)
	}
	m = update(m, key("x"))
	if m.screen != screenMenu {
		t.Errorf("any key: screen %d", m.screen)
	}

	m = feed(m, engine.OutputReport{ID: 6, Text: "nothing"})
	n := len(f.sent)
	m = update(m, key("a")) // nothing to accept
	if len(f.sent) != n {
		t.Error("a with nothing to accept was sent")
	}
	m = update(m, key("q"))
	if got := lastSent(t, f); got != (engine.Confirm{Prompt: 6, Yes: false}) || m.screen != screenMenu {
		t.Errorf("q: sent %#v, screen %d", got, m.screen)
	}
	m = feed(m, engine.Finished{Outcome: engine.OutcomeReport})
	if m.screen != screenMenu {
		t.Errorf("screen %d", m.screen)
	}
}

// The up-front prompt: esc cancels it (the menu follows Finished), enter
// submits; the services list: esc cancels, enter selects.
func TestEnginePromptsSend(t *testing.T) {
	m, f := engineModel()
	m = feed(m, engine.NeedsPassword{ID: 2, Kind: engine.PasswordUpFront, AttemptsLeft: 3})
	if m.screen != screenPassword || m.bg != screenMenu {
		t.Fatalf("screen %d over %d", m.screen, m.bg)
	}
	m = update(m, key("esc"))
	if got := lastSent(t, f); got != (engine.CancelPrompt{Prompt: 2}) {
		t.Errorf("sent %#v", got)
	}
	m = feed(m, engine.Finished{Outcome: engine.OutcomeCancelled})
	if m.screen != screenMenu {
		t.Errorf("screen %d", m.screen)
	}

	m = feed(m, engine.NeedsSelection{ID: 4, Items: []string{"a.service", "b.service"}})
	m = update(m, key("down"))
	m = update(m, key(" "))
	m = update(m, key("enter"))
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.Select{Prompt: 4, Picked: []string{"b.service"}}) {
		t.Errorf("sent %#v", got)
	}
	m = feed(m, engine.NeedsSelection{ID: 6, Items: []string{"a.service"}})
	m = update(m, key("esc"))
	if got := lastSent(t, f); got != (engine.CancelPrompt{Prompt: 6}) || m.screen != screenMenu {
		t.Errorf("esc: sent %#v, screen %d", got, m.screen)
	}

	// the menu starts an engine action; the TUI's own entries stay its own
	m.cursor = 0
	m = update(m, key("enter"))
	if _, ok := lastSent(t, f).(engine.Start); !ok {
		t.Errorf("enter on the menu sent %#v", lastSent(t, f))
	}
}

// forwardEvents sends each Event as its own msg, in order, until the
// channel closes, and then engineStoppedMsg.
func TestForwardEvents(t *testing.T) {
	driverReports.flush(io.Discard)
	ch := make(chan engine.Event, 2)
	ch <- engine.Notice{Text: "a"}
	ch <- engine.Notice{Text: "b"}
	close(ch)
	var got []tea.Msg
	forwardEvents(ch, func(m tea.Msg) { got = append(got, m) })
	want := []tea.Msg{
		engineEventsMsg{evs: []engine.Event{engine.Notice{Text: "a"}}},
		engineEventsMsg{evs: []engine.Event{engine.Notice{Text: "b"}}},
		engineStoppedMsg{why: "no error was reported"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
}

// StatusUpdated is the status line; a model without a Driver (a view
// test's) ignores Events and keys.
func TestEngineStatusAndNoDriver(t *testing.T) {
	m, _ := engineModel()
	st := sysstat.Status{}
	m = feed(m, engine.StatusUpdated{Now: time.Now(), Status: st})
	if m.status != st.Line() {
		t.Errorf("status %q, want %q", m.status, st.Line())
	}
	bare := sizedModel()
	bare.eng = nil
	if got := update(bare, engineEventsMsg{evs: []engine.Event{engine.Notice{Text: "x"}}}); got.screen != bare.screen {
		t.Errorf("a model without a Driver took an engine event: screen %d", got.screen)
	}
	if got := update(bare, key("down")); got.cursor != bare.cursor {
		t.Errorf("a model without a Driver took a key: cursor %d", got.cursor)
	}
}

// ctrl+c on the running step asks first; n, esc, ctrl+c and enter on No
// resume without sending anything, and the arrows move the choice.
func TestEngineAbortDeclined(t *testing.T) {
	for _, k := range []tea.KeyMsg{key("n"), key("esc"), {Type: tea.KeyCtrlC}, key("enter")} {
		m, f := engineModel()
		m = runningQueue(m)
		m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
		if m.screen != screenAbortConfirm || m.bg != screenRunning || m.confirmYes {
			t.Fatalf("ctrl+c: screen %d over %d, yes %v", m.screen, m.bg, m.confirmYes)
		}
		m = update(m, k)
		if m.screen != screenRunning || len(f.sent) != 0 {
			t.Errorf("%q: screen %d, sent %v", k, m.screen, f.sent)
		}
	}
	m, f := engineModel()
	m = runningQueue(m)
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	m = update(m, key("left"))
	if !m.confirmYes {
		t.Fatal("left didn't move the choice to Yes")
	}
	m = update(m, key("enter"))
	if got := lastSent(t, f); got != (engine.Abort{}) || m.screen != screenRunning {
		t.Errorf("enter on Yes: sent %#v, screen %d", got, m.screen)
	}
}

// The askpass popup shows sudo's prompt only quoted and stripped of
// escapes and line breaks: whoever runs sudo picks it with -p.
func TestEngineAskpassPromptSanitized(t *testing.T) {
	m, _ := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 7, Kind: engine.PasswordAskpass, Step: "a",
		Requester: "/usr/bin/yay (pid 5000150)", Prompt: "\x1b]0;pwned\x07[sudo] password\nfor u: "})
	view := m.viewPassword()
	for _, want := range []string{"Doing a…", "/usr/bin/yay (pid 5000150)", `"[sudo] password for u:"`} {
		if !strings.Contains(view, want) {
			t.Errorf("popup missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "pwned") || strings.ContainsRune(view, '\x07') {
		t.Fatalf("popup shows the prompt's escape sequence:\n%q", view)
	}
}

// A long askpass prompt is cut to one short line.
func TestAskpassPromptTextClipped(t *testing.T) {
	got := askpassPromptText(strings.Repeat("ab ", 40))
	if n := len([]rune(got)); n != 48 || !strings.HasSuffix(got, "…") {
		t.Fatalf("prompt = %q (%d runes), want 48 ending in …", got, n)
	}
}

// newDriver builds the real Driver on the package's knobs; closing it
// closes its Events.
func TestNewDriverIsTheRealDriver(t *testing.T) {
	transcriptEnv(t, true)
	installShims(t, Shims{})
	d := newDriver(engine.New(engine.Options{DryRun: true, Env: actionEnv()}), nil)
	if _, ok := d.(*driver.Driver); !ok {
		t.Fatalf("newDriver built a %T", d)
	}
	d.Close()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-d.Events():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("Events was never closed")
		}
	}
}

// startPrefetch starts the AUR audit's lookup and nothing else.
func TestPrefetchStartsOnlyTheAudit(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": pacmanRules("", "", "")})
	if p := startPrefetch("something-else"); p != nil {
		t.Fatalf("startPrefetch(unknown) = %v, want nil", p)
	}
	p := startPrefetch(steps.PrefetchAURAudit)
	if p == nil {
		t.Fatal("startPrefetch(the audit) = nil")
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the audit's prefetch never finished")
	}
	if res := p.Run(context.Background()); !strings.Contains(res.Output, "nothing to audit") {
		t.Fatalf("Run = %+v", res)
	}
	p.Cancel()
}

// The error log keeps maxReports errors, counts the rest and empties on
// flush.
func TestReportLogFlush(t *testing.T) {
	var r reportLog
	r.add(nil)
	for i := range maxReports + 2 {
		r.add(fmt.Errorf("e%d", i))
	}
	var b strings.Builder
	r.flush(&b)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != maxReports+1 || lines[0] != "archmaid: e0" ||
		lines[maxReports] != "archmaid: 2 more errors not shown" {
		t.Fatalf("flush wrote:\n%s", b.String())
	}
	b.Reset()
	r.flush(&b)
	if b.Len() != 0 {
		t.Fatalf("a second flush wrote %q", b.String())
	}
}
