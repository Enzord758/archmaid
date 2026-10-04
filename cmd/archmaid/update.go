package main

import (
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// ── update ───────────────────────────────────────────────────────────────────
//
// The model is a front end to an engine.Driver (adapter.go): a key that
// answers a prompt or starts an action becomes a Command (engineKey), and
// what the action does comes back as Events (applyEvents). What is left
// here is the TUI's own: moving about, scrolling, the log viewer, the
// editor, the status line.

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth, m.termHeight = msg.Width, msg.Height
		m.resizeOutput()
		return m, nil

	case tea.KeyMsg:
		if m.eng == nil {
			return m, nil // no Driver (a view test's model): nothing to drive
		}
		if next, cmd, ok := m.engineKey(msg); ok {
			return next, cmd
		}
		return m.handleKey(msg)

	case engineEventsMsg:
		if m.eng == nil {
			return m, nil
		}
		return m.applyEvents(msg.evs)

	case statusMsg:
		m.status = string(msg)
		return m, refreshStatus() // tick again in a few seconds

	case statusNowMsg:
		m.status = string(msg)
		return m, nil // one-off: the Init loop keeps ticking on its own

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case editorDoneMsg:
		m.screen = screenMenu
		return m, refreshStatusNow()
	}

	// keep the password field's cursor blinking (blink msgs aren't KeyMsgs)
	if m.screen == screenPassword {
		var cmd tea.Cmd
		m.password, cmd = m.password.Update(msg)
		return m, cmd
	}
	return m, nil
}

// resizeOutput sizes the output viewport for the active screen. The log
// viewer's chrome around the output box is fixed (header + category line +
// box border + hint = 8 rows), but the running screen additionally stacks
// the checklist above the box, and that grows with the queue — so it needs
// those extra rows subtracted too, or the pane overflows the terminal by one
// row per queued step. Resolved through baseScreen rather than compared
// against m.screen directly, so a resize while a popup (per-step confirm,
// abort-confirm, an askpass prompt) is showing over the running screen still
// budgets for the checklist — otherwise dismissing the popup back to
// screenRunning would hit the same overflow. Called on resize and whenever
// the queue length or active screen changes (QueueStarted, StepInserted,
// OutputReport, openLogCategory).
func (m *model) resizeOutput() {
	m.output.Width = m.termWidth - 4
	height := m.termHeight - 8
	if m.baseScreen(m.screen) == screenRunning {
		height -= len(m.queue)
	}
	if height < 1 {
		height = 1
	}
	m.output.Height = height
}

// handleKey takes the keys engineKey leaves: moving about, toggling,
// typing, scrolling, the menu, the summary and the log viewer.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {

	case screenMenu:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(actions.Menu)-1 {
				m.cursor++
			}
		case "enter":
			return m.activate()
		}

	case screenPassword:
		// engineKey takes enter and esc, and every key while a password is
		// being checked (a second enter would send a second password, and
		// typed keys would land in the one already sent): what is left is
		// typing.
		var cmd tea.Cmd
		m.password, cmd = m.password.Update(msg)
		return m, cmd

	case screenConfirm:
		// engineKey takes y, n, esc and enter.
		switch msg.String() {
		case "left", "right", "h", "l", "tab":
			m.confirmYes = !m.confirmYes
		}

	case screenRunning:
		if msg.String() == "ctrl+c" {
			// Don't quit — ask first (the step keeps running underneath).
			m.bg = screenRunning
			m.screen = screenAbortConfirm
			m.confirmYes = false
			return m, nil
		}
		// let the viewport scroll while a step runs
		var cmd tea.Cmd
		m.output, cmd = m.output.Update(msg)
		return m, cmd

	case screenAbortConfirm:
		// engineKey takes y, and enter on Yes.
		switch msg.String() {
		case "left", "right", "h", "l", "tab":
			m.confirmYes = !m.confirmYes
		case "n", "esc", "ctrl+c", "enter":
			m.screen = screenRunning // resume; the step never stopped
		}
		return m, nil

	case screenSummary:
		switch msg.String() {
		case "l":
			m.logReturn = screenSummary
			return m.openLogPicker()
		case "enter", "esc", "q":
			m.screen = screenMenu
		}

	case screenLogPicker:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.screen = m.logReturn
		case "up", "k":
			if m.logCursor > 0 {
				m.logCursor--
			}
		case "down", "j":
			if m.logCursor < len(logCategoryLabels)-1 {
				m.logCursor++
			}
		case "enter":
			return m.openLogCategory(logCategoryLabels[m.logCursor].ID)
		}

	case screenLog:
		switch msg.String() {
		case "esc", "q":
			m.screen = screenLogPicker
		default:
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			return m, cmd
		}

	case screenAudit:
		// engineKey takes esc, q and a; the rest scrolls the report.
		var cmd tea.Cmd
		m.output, cmd = m.output.Update(msg)
		return m, cmd

	case screenNotice:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.screen = screenMenu // any other key returns

	case screenDiscovering:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// otherwise wait for the discovery to finish

	case screenServices:
		// engineKey takes enter, esc and ctrl+c.
		switch msg.String() {
		case "up", "k":
			if m.serviceCursor > 0 {
				m.serviceCursor--
			}
		case "down", "j":
			if m.serviceCursor < len(m.services)-1 {
				m.serviceCursor++
			}
		case " ", "x":
			m.serviceOn[m.serviceCursor] = !m.serviceOn[m.serviceCursor]
		}
	}
	return m, nil
}

// activate handles an Enter on the menu: the TUI's own entries by ID; any
// other action is the engine's to run, and its Events say what to show.
func (m model) activate() (tea.Model, tea.Cmd) {
	a := actions.Menu[m.cursor]
	switch a.ID {
	case actions.IDExit:
		return m, tea.Quit
	case actions.IDViewLog:
		m.logReturn = screenMenu
		return m.openLogPicker()
	case actions.IDEditConfig:
		return m, editConfig()
	}
	m.send(engine.Start{Action: a.ID})
	return m, nil
}

// openLogPicker shows the log-category menu ("View log").
func (m model) openLogPicker() (tea.Model, tea.Cmd) {
	m.screen = screenLogPicker
	m.logCursor = 0
	return m, nil
}

// openLogCategory opens the pager on one category's log.
func (m model) openLogCategory(cat logCategory) (tea.Model, tea.Cmd) {
	m.screen = screenLog
	m.resizeOutput() // switch off the running screen's queue-shrunk height, if any
	m.logCat = cat
	m.output.SetContent(readLog(cat))
	m.output.GotoTop()
	return m, nil
}
