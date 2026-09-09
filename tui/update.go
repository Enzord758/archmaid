package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// ── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.termWidth, m.termHeight = msg.Width, msg.Height
		m.resizeOutput()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case statusMsg:
		m.status = string(msg)
		return m, refreshStatus() // tick again in a few seconds

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case sudoCachedMsg:
		if msg.cached {
			// No password prompt to act as the gate. If this action wants a
			// proceed-gate, show it; otherwise run straight away.
			if m.gate != "" {
				m.bg = screenMenu
				m.confirmMsg = m.gate
				m.confirmYes = false
				m.gateActive = true
				m.screen = screenConfirm
				return m, nil
			}
			cmd := m.startQueue()
			return m, cmd
		}
		// Uncached: the password prompt itself is the confirmation.
		m.bg = screenMenu
		m.screen = screenPassword
		m.pwErr = ""
		m.password.Reset()
		return m, m.password.Focus()

	case sudoResultMsg:
		m.authInFlight = false // auth finished — unlock the input
		if msg.err != nil {
			m.pwAttempts++
			m.password.Reset()
			if m.pwAttempts >= maxSudoAttempts {
				// give up: run the queue anyway; sudo steps get skipped+logged
				m.sudoUnavailable = true
				cmd := m.startQueue()
				return m, cmd
			}
			left := maxSudoAttempts - m.pwAttempts
			m.pwErr = fmt.Sprintf("Incorrect password — %d attempt%s left", left, plural(left))
			if msg.detail != "" {
				m.pwErr = msg.detail // e.g. pam_faillock "account locked" notice
			}
			return m, nil // stay on the prompt for a retry
		}
		m.pwAttempts = 0
		m.password.Reset()
		cmd := m.startQueue()
		return m, cmd

	case discoveryMsg:
		if msg.notice != "" {
			m.notice = msg.notice
			m.bg = screenMenu
			m.screen = screenNotice
			return m, nil
		}
		if msg.kind == "services" {
			m.services = msg.services
			m.serviceOn = make([]bool, len(msg.services))
			m.serviceCursor = 0
			m.screen = screenServices
			return m, nil
		}
		m.pending = msg.pending // repair / downloads
		cmd := m.beginPending()
		return m, cmd

	case sudoKeepAliveMsg:
		if !m.sudoAlive {
			return m, nil // queue ended — stop refreshing
		}
		return m, tea.Batch(refreshSudo(), sudoTick())

	case askpassMsg:
		// A child's sudo wants a password — pop the prompt over the current
		// screen. The password flows straight back over the connection.
		m.askpassConn = msg.conn
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
		m.pwErr = ""
		if strings.Contains(strings.ToLower(msg.prompt), "try again") {
			m.pwErr = "Incorrect password — try again"
		}
		m.screen = screenPassword
		m.password.Reset()
		return m, m.password.Focus()

	case editorDoneMsg:
		m.screen = screenMenu
		return m, refreshStatus()

	case streamMsg:
		return m.handleStream(msg)
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
// the queue length or active screen changes (startQueue, openLogCategory).
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
			if m.cursor < len(menu)-1 {
				m.cursor++
			}
		case "enter":
			return m.activate()
		}

	case screenPassword:
		if m.authInFlight {
			// PAM is checking a submitted password (takes ~2s on failure).
			// Ignore all input meanwhile: a second enter would fire a
			// concurrent sudo, and typed keys would silently append to the
			// already-submitted password.
			return m, nil
		}
		switch msg.String() {
		case "esc", "ctrl+c":
			if m.askpassConn != nil {
				m.askpassConn.Close() // cancel → sudo fails this attempt
				m.askpassConn = nil
				m.password.Reset()
				m.pwErr = ""
				m.screen = m.bg
				return m, nil
			}
			m.password.Reset()
			m.pwErr = ""
			m.screen = screenMenu
			return m, nil
		case "enter":
			if m.askpassConn != nil {
				// pass the password straight through to sudo; never stored.
				fmt.Fprintf(m.askpassConn, "%s\n", m.password.Value())
				m.askpassConn.Close()
				m.askpassConn = nil
				m.password.Reset()
				m.pwErr = ""
				m.screen = m.bg
				return m, nil
			}
			m.authInFlight = true
			pw := m.password.Value()
			m.password.Reset() // don't hold the password in the input
			return m, authSudo(pw)
		default:
			var cmd tea.Cmd
			m.password, cmd = m.password.Update(msg)
			return m, cmd
		}

	case screenConfirm:
		switch msg.String() {
		case "left", "right", "h", "l", "tab":
			m.confirmYes = !m.confirmYes
		case "y":
			m.confirmYes = true
			return m.resolveConfirm()
		case "n", "esc":
			m.confirmYes = false
			return m.resolveConfirm()
		case "enter":
			return m.resolveConfirm()
		}

	case screenRunning:
		if msg.String() == "ctrl+c" {
			// Don't quit — ask first (the child keeps running underneath).
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
		switch msg.String() {
		case "left", "right", "h", "l", "tab":
			m.confirmYes = !m.confirmYes
		case "y":
			m.abortRunning()
		case "n", "esc", "ctrl+c":
			m.screen = screenRunning // resume; the step never stopped
		case "enter":
			if m.confirmYes {
				m.abortRunning()
			} else {
				m.screen = screenRunning
			}
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
			return m.openLogCategory(logCategoryLabels[m.logCursor].id)
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

	case screenNotice:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.screen = screenMenu // any other key returns

	case screenDiscovering:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// otherwise wait for the discovery cmd to finish

	case screenServices:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.screen = screenMenu
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
		case "enter":
			cmd := m.confirmServices()
			return m, cmd
		}
	}
	return m, nil
}

// confirmServices builds a disable step from the selected services and enters
// the sudo/queue flow; a no-op if nothing was selected.
func (m *model) confirmServices() tea.Cmd {
	var sel []string
	for i, on := range m.serviceOn {
		if on {
			sel = append(sel, m.services[i])
		}
	}
	if len(sel) == 0 {
		m.screen = screenMenu
		return nil
	}
	m.pending = []step{{
		title: "Disabling services…", logLabel: "Services disabled",
		command: "sudo", args: append([]string{"systemctl", "disable", "--now"}, sel...),
		needsSudo: true, alwaysLog: true,
	}}
	return m.beginPending()
}

// activate handles an Enter on the menu.
func (m model) activate() (tea.Model, tea.Cmd) {
	item := menu[m.cursor]
	m.gate = "" // only steps-actions with a gate re-arm it below
	switch item.id {
	case "exit":
		return m, tea.Quit
	case "viewlog":
		m.logReturn = screenMenu
		return m.openLogPicker()
	case "editconfig":
		return m, editConfig()
	case "repair":
		m.screen = screenDiscovering
		m.discovering = "Scanning for broken packages…"
		return m, discoverRepair()
	case "downloads":
		m.screen = screenDiscovering
		m.discovering = "Checking Downloads…"
		return m, discoverDownloads()
	case "services":
		m.screen = screenDiscovering
		m.discovering = "Finding unused services…"
		return m, discoverServices()
	}

	if item.steps == nil {
		return m, nil
	}
	m.gate = item.gate
	m.pending = item.steps()
	cmd := m.beginPending()
	return m, cmd
}

// beginPending starts m.pending: authenticate first if any step needs sudo
// (probe → skip prompt when cached), otherwise run the queue directly.
func (m *model) beginPending() tea.Cmd {
	m.errs = 0
	m.skipped = 0
	m.aborted = false
	m.gateActive = false
	m.pwAttempts = 0
	m.sudoUnavailable = false
	for _, s := range m.pending {
		if s.needsSudo {
			return checkSudoCached()
		}
	}
	return m.startQueue()
}

// startQueue initialises the checklist and kicks off the first step. If any
// step needs sudo (and we're not in a dry run), it also starts the keep-alive
// loop so a long step can't let the sudo timestamp lapse mid-queue.
func (m *model) startQueue() tea.Cmd {
	m.queue = make([]stepState, len(m.pending))
	for i, s := range m.pending {
		m.queue[i] = stepState{step: s, status: statusPending}
	}
	m.pending = nil
	m.current = -1
	m.screen = screenRunning
	m.resizeOutput() // checklist length is now known — shrink the output pane to fit

	cmd := m.advance()
	if !dryRun && !m.sudoAlive && queueNeedsSudo(m.queue) {
		m.sudoAlive = true
		return tea.Batch(cmd, sudoTick())
	}
	return cmd
}

func queueNeedsSudo(q []stepState) bool {
	for _, s := range q {
		if s.step.needsSudo {
			return true
		}
	}
	return false
}

// advance moves to the next step: shows a confirm if the step has one,
// otherwise launches it. When the queue is exhausted it flips to the summary.
func (m *model) advance() tea.Cmd {
	m.current++
	if m.current >= len(m.queue) {
		m.screen = screenSummary
		m.sudoAlive = false // stop the keep-alive loop
		return refreshStatus()
	}
	s := m.queue[m.current].step
	// Sudo auth was abandoned after too many wrong passwords: skip any step
	// that needs it, note it in the log, and move on.
	if s.needsSudo && m.sudoUnavailable {
		m.queue[m.current].status = statusSkipped
		appendLog(stepCategory(s), "SKIP", s.logLabel+" (sudo authentication failed)", "")
		m.skipped++ // a skip is not a failure — tallied separately
		return m.advance()
	}
	if s.confirm != "" {
		m.bg = screenRunning
		m.screen = screenConfirm
		m.confirmMsg = s.confirm
		m.confirmYes = false
		return nil
	}
	return m.launch()
}

func (m *model) launch() tea.Cmd {
	m.queue[m.current].status = statusRunning
	m.buf.Reset()
	m.output.SetContent("")
	m.stream = make(chan streamMsg, 128)
	m.proc = runStep(m.queue[m.current].step, m.stream)
	// The spinner is already ticking (started in Init); just pump the stream.
	return waitForStream(m.stream)
}

// sigkillGrace is how long a child gets to honour SIGINT before it's forcibly
// killed on abort. A var so tests can shrink it.
var sigkillGrace = 5 * time.Second

// abortRunning signals the active step's child to stop and returns to the menu.
// SIGINT (like a terminal ctrl+c) lets tools such as pacman exit cleanly; for
// sudo steps, sudo relays the signal to the command it runs. If the child
// ignores SIGINT it's force-killed (SIGKILL) after a grace period. Late output
// from the dying process is dropped by handleStream's screen guard.
func (m *model) abortRunning() {
	if p := m.proc; p != nil {
		_ = p.Signal(os.Interrupt)
		// grace read here (caller goroutine), not inside escalateKill, so the
		// spawned goroutine doesn't race a test that swaps sigkillGrace.
		go escalateKill(p, sigkillGrace)
		m.proc = nil
	}
	m.sudoAlive = false // stop the keep-alive loop
	m.screen = screenMenu
}

// escalateKill force-terminates p if it hasn't exited within grace. p.Kill() is
// a no-op (returns an ignored error) once the process has been reaped by
// runStep's cmd.Wait, and never touches a reused PID.
func escalateKill(p *os.Process, grace time.Duration) {
	time.Sleep(grace)
	_ = p.Kill() // SIGKILL — cannot be caught or ignored
}

func (m model) resolveConfirm() (tea.Model, tea.Cmd) {
	// Proceed-gate (cached-sudo path): not tied to a queue step yet.
	if m.gateActive {
		m.gateActive = false
		if !m.confirmYes {
			m.screen = screenMenu
			return m, nil
		}
		cmd := m.startQueue()
		return m, cmd
	}

	m.screen = screenRunning
	s := &m.queue[m.current]
	if !m.confirmYes {
		if s.step.optional {
			s.status = statusSkipped
			m.skipped++
			cmd := m.advance()
			return m, cmd
		}
		// non-optional "no" aborts: mark the declined step and everything
		// after it as skipped so the summary shows what didn't run.
		for i := m.current; i < len(m.queue); i++ {
			m.queue[i].status = statusSkipped
			m.skipped++
		}
		m.aborted = true
		m.sudoAlive = false // stop the keep-alive loop
		m.screen = screenSummary
		return m, nil
	}
	cmd := m.launch()
	return m, cmd
}

func (m model) handleStream(msg streamMsg) (tea.Model, tea.Cmd) {
	// Once we've left the run for good (aborted → menu, or finished → summary),
	// drop late output from the dying child and stop pumping. Keep pumping while
	// an askpass popup or per-step confirm is up over the running step.
	if m.screen == screenMenu || m.screen == screenSummary {
		return m, nil
	}
	if !msg.done {
		m.buf.WriteString(msg.line + "\n")
		m.output.SetContent(m.buf.String())
		m.output.GotoBottom()
		return m, waitForStream(m.stream) // keep pumping
	}

	// step finished
	s := &m.queue[m.current]
	cat := stepCategory(s.step)
	if msg.err != nil {
		s.status = statusFailed
		m.errs++
		appendLog(cat, "FAIL", s.step.logLabel, m.buf.String())
	} else {
		s.status = statusOK
		switch {
		case dryRun: // a dry run logs every action, not just failures
			appendLog(cat, "DRYRUN", s.step.logLabel, m.buf.String())
		case s.step.alwaysLog: // destructive steps log their outcome even on success
			appendLog(cat, "OK", s.step.logLabel, m.buf.String())
		}
	}
	cmd := m.advance()
	return m, cmd
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
