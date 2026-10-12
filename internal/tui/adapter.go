package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── the TUI on the engine (E7) ───────────────────────────────────────────────
//
// The TUI is a front end to the engine: the model runs its actions on an
// engine.Driver. A key that answers a prompt or starts an action becomes a
// Command (Driver.Send), and the model's state — the checklist, the screen,
// the prompt, the output pane — is derived from the Events (applyEvents).
// The queue, privilege, askpass vetting, logging and the last-run record
// are all the engine's.
//
// The seam is engine.Driver, built by newDriver; main does the rest (the
// model on it, forwardEvents, Close after the program). The tests drive the
// model on syncDriver (syncdriver_test.go) instead.

// newDriver builds the Driver the binary runs on: internal/driver around s,
// on the sudo path (E6a). Call it on main's goroutine before the program
// starts, after startAskpassServer (privileged reads its wrapper) and the
// command line (logs reads --dryrun). askpass is the askpass Server's
// requests, nil when it isn't wired up; the Driver verifies each against
// the running step's child and gives the popup its Requester.
var newDriver = func(s *engine.Session, askpass <-chan *privilege.Request) engine.Driver {
	return driver.New(s, driverDeps(askpass))
}

// driverDeps is driver.Deps as the TUI wires them: sudo with the askpass
// Server's wrapper (privileged; SUDO_ASKPASS only on NeedsPriv steps), the
// log store, the TUI's kill grace and pipe wait, the Server's requests, the
// audit prefetch and the error log. Same goroutine rules as newDriver.
func driverDeps(askpass <-chan *privilege.Request) driver.Deps {
	return driver.Deps{
		Priv: privileged(), Log: logs(),
		KillGrace: sigkillGrace, WaitDelay: stepWaitDelay,
		Askpass:  askpass,
		Prefetch: startPrefetch,
		Report:   driverReports.add,
		// The widget's update count, refreshed after a topgrade run.
		RefreshUpdates: driver.RefreshUpdates,
	}
}

// driverReports is the TUI's error log: what the Driver reports that no
// Event carries (a log write that failed, a hook's error, a recovered
// panic). The alt screen owns the terminal while the program runs, so main
// prints it to stderr once the program has ended and the Driver is closed.
var driverReports reportLog

// reportLog collects errors from any goroutine, up to maxReports; past that
// it only counts them. add only appends: it runs on the Driver's loop and
// must never call Close.
type reportLog struct {
	mu      sync.Mutex
	errs    []error
	dropped int
}

// maxReports bounds what a reportLog keeps.
const maxReports = 20

func (r *reportLog) add(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) >= maxReports {
		r.dropped++
		return
	}
	r.errs = append(r.errs, err)
}

// flush writes what was reported to w, one "archmaid: " line each (a
// panic's stack keeps its own lines), and forgets it.
func (r *reportLog) flush(w io.Writer) {
	r.mu.Lock()
	errs, dropped := r.errs, r.dropped
	r.errs, r.dropped = nil, 0
	r.mu.Unlock()
	for _, err := range errs {
		fmt.Fprintln(w, "archmaid:", err)
	}
	if dropped > 0 {
		fmt.Fprintf(w, "archmaid: %d more error%s not shown\n", dropped, plural(dropped))
	}
}

// summary is one line on what went wrong, for the notice the model shows
// when the Driver stops on its own: the first line of the newest report
// (the Driver reports a Session panic just before it closes), or a plain
// "no error was reported". Unlike flush it forgets nothing: main still
// prints every report once the program has ended.
func (r *reportLog) summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) == 0 {
		return "no error was reported"
	}
	first, _, _ := strings.Cut(r.errs[len(r.errs)-1].Error(), "\n") // a panic's stack follows
	return sanitize.Clip(sanitize.OneLine(first), 60)
}

// forwardEvents hands the program every Event the Driver delivers, one per
// msg, in order, until Events is closed, and then an engineStoppedMsg. The
// Driver closes Events after its last Event, so a Report it made before
// closing (a Session panic) is in driverReports by then.
func forwardEvents(evs <-chan engine.Event, send func(tea.Msg)) {
	for ev := range evs {
		send(engineEventsMsg{evs: []engine.Event{ev}})
	}
	send(engineStoppedMsg{why: driverReports.summary()})
}

// engineEventsMsg carries Events from the Driver, in the order it
// delivered them.
type engineEventsMsg struct{ evs []engine.Event }

// engineStoppedMsg says the Driver's Events are closed: no Event, and so
// no Finished, will come again. why is driverReports' summary.
//
// After a quit the program has ended and the msg goes nowhere. While the
// program runs it means the Driver closed on its own (a Session panic),
// and the model stops: see engineStopped.
type engineStoppedMsg struct{ why string }

// engineState is what the model keeps of the engine: the Driver and the
// prompts it has open.
type engineState struct {
	drv engine.Driver

	prompt  engine.PromptID // the open confirm, up-front password or selection
	askpass engine.PromptID // the open askpass prompt
	report  engine.PromptID // the open OutputReport
	accept  bool            // the report's findings can be accepted (a)
	aborted bool            // the user aborted: Finished{OutcomeAborted} goes to the menu
	stopped bool            // the Driver closed on its own: any key quits
}

func newEngineState(d engine.Driver) *engineState { return &engineState{drv: d} }

// askpassOpen reports whether an askpass prompt is open; false for a model
// without a Driver.
func (e *engineState) askpassOpen() bool { return e != nil && e.askpass != 0 }

// canAccept reports whether the open report's findings can be accepted.
func (e *engineState) canAccept() bool { return e != nil && e.accept }

// send hands the Driver c. A closed Driver refuses it, and then there is
// nothing more to show.
func (m *model) send(c engine.Command) { m.eng.drv.Send(c) }

// applyEvents derives the model's state from evs, in order.
func (m model) applyEvents(evs []engine.Event) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, ev := range evs {
		cmds = append(cmds, m.applyEvent(ev))
	}
	return m, tea.Batch(cmds...)
}

func (m *model) applyEvent(ev engine.Event) tea.Cmd {
	e := m.eng
	switch ev := ev.(type) {
	case engine.PhaseChanged:
		if ev.Phase == engine.PhaseDiscovering {
			m.screen, m.discovering = screenDiscovering, ev.Busy
		}
	case engine.QueueStarted:
		m.queue = make([]stepState, len(ev.Steps))
		for i, p := range ev.Steps {
			m.queue[i] = stepState{step: planned(p), status: steps.Pending, plan: p}
		}
		m.current, m.errs, m.skipped, m.aborted, e.aborted = -1, 0, 0, false, false
		m.screen = screenRunning
		m.resizeOutput() // checklist length is now known
	case engine.StepInserted:
		m.queue = slices.Insert(m.queue, ev.Index, stepState{step: planned(ev.Step), status: steps.Pending, plan: ev.Step})
		m.resizeOutput()
	case engine.PlanChanged:
		m.queue[ev.Index].step, m.queue[ev.Index].plan = planned(ev.Step), ev.Step
	case engine.StepStarted:
		m.current = ev.Index
		m.queue[ev.Index].status = steps.Running
		m.resetOutput()
	case engine.OutputChunk:
		m.appendOutput(ev.Lines)
		if m.queue[ev.Index].plan.InProc {
			m.output.GotoTop() // a report reads top-down; its worst findings come first
		}
	case engine.StepDone:
		m.queue[ev.Index].status = ev.Status
		switch ev.Status {
		case steps.Failed:
			m.errs++
		case steps.Skipped, steps.AuthRefused:
			m.skipped++
		}
	case engine.Aborted:
		m.queue[ev.Index].status = steps.Aborted
		e.aborted = true
	case engine.NeedsConfirm:
		e.prompt = ev.ID
		m.confirmMsg, m.confirmYes = ev.Msg, ev.Default
		m.gateActive = ev.Kind == engine.ConfirmGate
		m.bg = screenMenu
		if !m.gateActive {
			m.bg = screenRunning
			if i := m.stepIndex(ev.Step); i >= 0 {
				m.current = i
			}
		}
		m.screen = screenConfirm
	case engine.NeedsPassword:
		if ev.Kind == engine.PasswordAskpass {
			return m.openAskpass(ev)
		}
		e.prompt = ev.ID
		m.authInFlight = false
		m.bg, m.screen = screenMenu, screenPassword
		m.pwErr = ev.Error
		m.password.Reset()
		return m.password.Focus()
	case engine.AuthWaiting:
		if ev.Kind == engine.WaitPasswordCheck {
			m.authInFlight = true
		}
	case engine.AuthResolved:
		m.authInFlight = false
		if e.askpass != 0 && (ev.Granted || ev.AttemptsLeft == 0) {
			m.closeEngineAskpass()
		}
	case engine.PromptClosed:
		switch ev.ID {
		case e.askpass:
			m.closeEngineAskpass()
		case e.prompt:
			e.prompt = 0
		}
	case engine.NeedsSelection:
		e.prompt = ev.ID
		m.services = slices.Clone(ev.Items)
		m.serviceOn = make([]bool, len(ev.Items))
		m.serviceCursor = 0
		m.screen = screenServices
	case engine.DiscoveryResult:
		// The record of a discovery that ends the action: its Notice follows
		// and says where to read the details.
		m.noticeLog = ""
		if ev.Outcome != engine.DiscoveryFound && ev.Category != "" {
			m.noticeLog = categoryLabel(ev.Category)
		}
	case engine.Notice:
		m.notice = ev.Text
		m.bg, m.screen = screenMenu, screenNotice
	case engine.OutputReport:
		e.report, e.accept = ev.ID, ev.Audit != nil && ev.Audit.CanAccept
		m.screen = screenAudit
		m.resizeOutput()
		m.output.SetContent(ev.Text)
		m.output.GotoTop()
	case engine.ReportAccepted:
		e.accept = false
		m.notice, m.noticeLog = ev.Text, ""
		m.bg, m.screen = screenAudit, screenNotice
	case engine.StatusUpdated:
		m.status = ev.Status.Line()
	case engine.Finished:
		return m.finished(ev)
	}
	return nil
}

// engineStopped handles a Driver that closed on its own mid-session. Its
// Events stopped without a Finished, so the screen it was on would wait
// forever (the running screen only leaves on a Finished or an abort, and
// an abort is a Command the closed Driver refuses).
//
// The model shows why over whatever was showing, and any key quits; main
// then prints the full reports to stderr. It doesn't go back to the menu:
// the Driver refuses every Command once closed, so every engine action
// there would silently do nothing. Only the log viewer and the editor
// would still work, and a menu that looks alive but isn't is worse than
// an honest exit (restarting archmaid starts a fresh Driver).
func (m model) engineStopped(why string) (tea.Model, tea.Cmd) {
	e := m.eng
	e.stopped = true
	e.prompt, e.askpass, e.report, e.accept = 0, 0, 0, false
	m.askpassFrom = askpassRequester{}
	m.password.Reset()
	m.pwErr = ""
	m.gateActive, m.authInFlight = false, false
	m.bg = m.baseScreen(m.screen)
	m.notice, m.noticeLog = "archmaid's engine stopped unexpectedly: "+why, ""
	m.screen = screenNotice
	return m, nil
}

// finished ends the action: the summary for a queue that ran (or that a
// declined required step stopped), the menu for one the user aborted or
// that never started; a notice or an accepted report stays up.
func (m *model) finished(ev engine.Finished) tea.Cmd {
	e := m.eng
	if e.askpass != 0 {
		m.closeEngineAskpass()
	}
	e.prompt, e.report, e.accept = 0, 0, false
	m.errs, m.skipped = ev.Errs, ev.Skipped
	m.gateActive, m.authInFlight = false, false
	switch ev.Outcome {
	case engine.OutcomeCompleted:
		m.screen = screenSummary
		return refreshStatusNow()
	case engine.OutcomeAborted:
		if e.aborted {
			// The abort settles the checklist: what never ran is skipped.
			for i := range m.queue {
				if m.queue[i].status == steps.Pending {
					m.queue[i].status = steps.Skipped
				}
			}
			m.screen = screenMenu
			return nil
		}
		m.aborted = true // a declined required step
		m.screen = screenSummary
	case engine.OutcomeCancelled:
		m.password.Reset()
		m.pwErr = ""
		m.screen = screenMenu
	case engine.OutcomeReport:
		if m.screen == screenAudit {
			m.screen = screenMenu // dismissed
		}
	}
	return nil
}

// planned is a planned step as the checklist and confirms read it.
func planned(p engine.PlanStep) steps.Step {
	return steps.Step{ID: p.ID, Verb: p.Verb, Title: p.Title, LogLabel: p.LogLabel,
		Argv: p.Argv, NeedsPriv: p.NeedsPriv, Optional: p.Optional, AlwaysLog: p.AlwaysLog}
}

// stepIndex is the position of the step with id in the checklist, or -1.
func (m *model) stepIndex(id string) int {
	return slices.IndexFunc(m.queue, func(s stepState) bool { return s.step.ID == id })
}

// openAskpass pops the askpass prompt over whatever shows. The engine only
// asks for a request the Driver verified (the running sudo step's own
// child, through archmaid's helper); anything else was hung up on without
// a popup.
func (m *model) openAskpass(ev engine.NeedsPassword) tea.Cmd {
	m.eng.askpass = ev.ID
	title := ""
	if i := m.stepIndex(ev.Step); i >= 0 {
		title = m.queue[i].step.Title
	}
	m.askpassFrom = askpassRequester{step: title, caller: ev.Requester, prompt: askpassPromptText(ev.Prompt)}
	switch m.screen {
	case screenPassword, screenConfirm, screenNotice:
		// These three read the shared m.bg field for their own
		// background (popupLayer in view.go). Overwriting it with their
		// own screen value here would make it self-referential and
		// recurse forever in renderScreen — so leave it as whatever it
		// already correctly resolved to when that popup opened; once
		// this askpass prompt is dismissed, m.screen = m.bg still lands
		// back on that same popup.
	default:
		// screenAbortConfirm (safe: its background is hardcoded in
		// popupLayer, not read from m.bg) or an ordinary screen — record
		// it so whatever was showing reappears once the password prompt
		// closes.
		m.bg = m.screen
	}
	m.authInFlight = false
	m.pwErr = ev.Error
	m.screen = screenPassword
	m.password.Reset()
	return m.password.Focus()
}

// closeEngineAskpass drops the askpass popup and shows what was under it.
func (m *model) closeEngineAskpass() {
	m.eng.askpass = 0
	m.askpassFrom = askpassRequester{}
	m.password.Reset()
	m.pwErr = ""
	if m.screen == screenPassword {
		m.screen = m.bg
	}
}

// engineKey turns a key on a prompt into a Command; ok is false for a key
// handleKey takes (moving about, typing, scrolling, the menu, the log).
func (m model) engineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	e := m.eng
	switch m.screen {
	case screenPassword:
		if m.authInFlight {
			// A password is being checked: ignore input, except ctrl+c. A
			// check whose worker panicked never resolves (the Driver
			// reports it and the Session waits), so ctrl+c must still get
			// out: an askpass prompt hangs up on sudo like esc, back to
			// the running step; the up-front password aborts the action,
			// which the Session always honours (Finished: the menu).
			if msg.String() != "ctrl+c" {
				return m, nil, true
			}
			m.authInFlight = false
			if e.askpass != 0 {
				m.send(engine.CancelPrompt{Prompt: e.askpass})
				m.closeEngineAskpass()
				return m, nil, true
			}
			m.password.Reset()
			m.pwErr = ""
			m.send(engine.Abort{})
			return m, nil, true
		}
		switch msg.String() {
		case "esc", "ctrl+c":
			if e.askpass != 0 {
				// CancelPrompt on askpass hangs up on sudo; no Event says so.
				m.send(engine.CancelPrompt{Prompt: e.askpass})
				m.closeEngineAskpass()
				return m, nil, true
			}
			m.password.Reset()
			m.pwErr = ""
			m.send(engine.CancelPrompt{Prompt: e.prompt})
			return m, nil, true
		case "enter":
			pw := engine.Secret(m.password.Value())
			m.password.Reset() // don't hold the password in the input
			id := e.prompt
			if e.askpass != 0 {
				id = e.askpass
			}
			m.authInFlight = true
			m.send(engine.SubmitPassword{Prompt: id, Password: pw})
			return m, nil, true
		}
	case screenConfirm:
		switch msg.String() {
		case "y":
			m.confirmYes = true
			return m.engineConfirm()
		case "n", "esc":
			m.confirmYes = false
			return m.engineConfirm()
		case "enter":
			return m.engineConfirm()
		}
	case screenAbortConfirm:
		switch msg.String() {
		case "y":
			m.engineAbort()
			return m, nil, true
		case "enter":
			if m.confirmYes {
				m.engineAbort()
				return m, nil, true
			}
		}
	case screenAudit:
		switch msg.String() {
		case "esc", "q":
			id := e.report
			e.accept = false
			m.screen = screenMenu
			m.send(engine.Confirm{Prompt: id, Yes: false})
			return m, nil, true
		case "a":
			if e.accept {
				m.send(engine.Confirm{Prompt: e.report, Yes: true})
			}
			return m, nil, true
		}
	case screenServices:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.screen = screenMenu
			m.send(engine.CancelPrompt{Prompt: e.prompt})
			return m, nil, true
		case "enter":
			var sel []string
			for i, on := range m.serviceOn {
				if on {
					sel = append(sel, m.services[i])
				}
			}
			m.send(engine.Select{Prompt: e.prompt, Picked: sel})
			return m, nil, true
		}
	}
	return m, nil, false
}

// engineConfirm answers the open confirm. A step's confirm goes back to
// the running screen at once; a gate waits for the
// queue (or the menu) the answer leads to.
func (m model) engineConfirm() (tea.Model, tea.Cmd, bool) {
	id := m.eng.prompt
	m.eng.prompt = 0
	if !m.gateActive {
		m.screen = screenRunning
	}
	m.gateActive = false
	m.send(engine.Confirm{Prompt: id, Yes: m.confirmYes})
	return m, nil, true
}

// engineAbort aborts the running action; Finished returns to the menu.
func (m *model) engineAbort() {
	m.eng.aborted = true
	m.screen = screenRunning
	m.send(engine.Abort{})
}
