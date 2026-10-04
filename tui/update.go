package main

import (
	"context"
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

	case statusNowMsg:
		m.status = string(msg)
		return m, nil // one-off: the Init loop keeps ticking on its own

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case sudoCachedMsg:
		if msg.cached && m.scanAfterAuth != nil {
			return m.startScan()
		}
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
				if m.scanAfterAuth != nil {
					// no queue to skip through — the scan itself needs root
					m.scanAfterAuth = nil
					m.notice = "Sudo authentication failed — can't scan for broken packages."
					m.bg = screenMenu
					m.screen = screenNotice
					return m, nil
				}
				// give up: run the queue anyway; sudo steps get skipped+logged.
				// The audit fetch is kept: the audit step needs no sudo and
				// still runs (and reports) from it.
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
		if m.scanAfterAuth != nil {
			return m.startScan()
		}
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
		if msg.kind == "audit" {
			m.auditCommit = msg.commit
			m.screen = screenAudit
			m.resizeOutput()
			m.output.SetContent(msg.report)
			m.output.GotoTop()
			return m, nil
		}
		m.pending = msg.pending // repair / downloads
		if msg.kind == "repair" {
			// The scan only ran once the sudo ticket was primed (scanAfterAuth),
			// so don't probe it a second time — go straight to the queue.
			m.resetRun()
			cmd := m.startQueue()
			return m, cmd
		}
		cmd := m.beginPending()
		return m, cmd

	case sudoKeepAliveMsg:
		if !m.sudoAlive || msg.gen != m.sudoGen {
			// queue ended — stop refreshing; or a tick from an earlier
			// queue's loop, which must die so only the current one runs
			return m, nil
		}
		return m, tea.Batch(refreshSudo(), sudoTick(m.sudoGen))

	case askpassMsg:
		return m.handleAskpass(msg)

	case askpassResultMsg:
		return m.handleAskpassResult(msg)

	case editorDoneMsg:
		m.screen = screenMenu
		return m, refreshStatusNow()

	case streamBatchMsg:
		return m.handleStream(msg)

	case stepResultMsg:
		return m.handleStepResult(msg)
	}

	// keep the password field's cursor blinking (blink msgs aren't KeyMsgs)
	if m.screen == screenPassword {
		var cmd tea.Cmd
		m.password, cmd = m.password.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handleAskpass vets a child's request for the sudo password, then pops the
// prompt over the current screen; the password flows straight back over the
// connection. The socket is reachable by anything that inherited SUDO_ASKPASS
// — under topgrade that is every AUR build script — so a request is answered
// only while a sudo step runs, and only from archmaid's own helper, run by
// sudo, inside that step's process tree (verifyAskpassPeer). Anything else is
// hung up on without a popup; sudo counts that as a failed attempt.
func (m model) handleAskpass(msg askpassMsg) (tea.Model, tea.Cmd) {
	title, ok := m.runningSudoStep()
	if !ok {
		msg.conn.Close() // no sudo step running: nothing of ours is asking
		return m, nil
	}
	if m.askpassConn != nil {
		msg.conn.Close()
		m.noteOutput("archmaid: refused a second sudo password request while one is open")
		return m, nil
	}
	from, err := verifyAskpassPeer(msg.peer, m.proc.Pid, m.procAt)
	if err != nil {
		msg.conn.Close()
		m.noteOutput("archmaid: refused a sudo password request: " + err.Error())
		return m, nil
	}
	from.step = title
	from.prompt = askpassPromptText(msg.prompt)
	m.askpassConn = msg.conn
	m.askpassFrom = from

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
}

// submitAskpass checks the password typed into the askpass popup with su
// before anything reaches sudo (answerAskpass), with the input locked while
// PAM works, like the prime's first stage. A password with a line break is
// refused outright: sudo reads one line, so it would get a different password
// than su checked.
func (m model) submitAskpass(pw string) (tea.Model, tea.Cmd) {
	m.password.Reset() // don't hold the password in the input
	if strings.ContainsAny(pw, "\r\n") {
		m.pwErr = "A password can't contain a line break"
		return m, nil
	}
	m.authInFlight = true
	return m, answerAskpass(m.askpassConn, pw)
}

// handleAskpassResult closes the askpass popup once su accepted the password
// (answerAskpass has already passed it to sudo). A rejected one leaves the
// popup open with nothing sent, counting toward maxSudoAttempts like the
// prime; at the limit the request is cancelled and, as after a failed prime,
// the queue's remaining sudo steps are skipped.
func (m model) handleAskpassResult(msg askpassResultMsg) (tea.Model, tea.Cmd) {
	if msg.conn != m.askpassConn {
		return m, nil // not the open request's answer
	}
	m.authInFlight = false
	if msg.err == nil {
		m.pwAttempts = 0
		m.closeAskpass()
		return m, nil
	}
	m.pwAttempts++
	if m.pwAttempts >= maxSudoAttempts {
		m.askpassConn.Close() // unanswered: this sudo fails, its step with it
		m.sudoUnavailable = true
		m.closeAskpass()
		return m, nil
	}
	left := maxSudoAttempts - m.pwAttempts
	m.pwErr = fmt.Sprintf("Incorrect password — %d attempt%s left", left, plural(left))
	if msg.detail != "" {
		m.pwErr = msg.detail // e.g. pam_faillock "account locked" notice
	}
	return m, nil
}

// closeAskpass drops the askpass popup's state and shows what was under it.
func (m *model) closeAskpass() {
	m.askpassConn = nil
	m.askpassFrom = askpassRequester{}
	m.password.Reset()
	m.pwErr = ""
	if m.screen == screenPassword {
		m.screen = m.bg
	}
}

// runningSudoStep returns the title of the step running right now, if it is a
// sudo step with a live child: the only time a password request can be ours.
func (m model) runningSudoStep() (string, bool) {
	if m.proc == nil || m.current < 0 || m.current >= len(m.queue) {
		return "", false
	}
	st := m.queue[m.current]
	if st.status != statusRunning || !st.step.needsSudo {
		return "", false
	}
	return st.step.title, true
}

// noteOutput adds one of archmaid's own lines to the running step's output
// pane (and so to its log). It may quote names read from /proc, so it is
// sanitized like any foreign text.
func (m *model) noteOutput(line string) {
	m.buf.WriteString(oneLine(line) + "\n")
	m.output.SetContent(m.buf.String())
	m.output.GotoBottom()
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
				m.closeAskpass()
				return m, nil
			}
			m.password.Reset()
			m.pwErr = ""
			m.scanAfterAuth = nil
			m.dropAuditFetch() // the action is cancelled before it started
			m.screen = screenMenu
			return m, nil
		case "enter":
			if m.askpassConn != nil {
				return m.submitAskpass(m.password.Value())
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

	case screenAudit:
		switch msg.String() {
		case "esc", "q":
			m.auditCommit = nil // leaving without accepting keeps the changes flagged
			m.screen = screenMenu
		case "a":
			if m.auditCommit == nil {
				return m, nil
			}
			m.notice = "Maintainer changes accepted: they won't be flagged again."
			if err := m.auditCommit(); err != nil {
				m.notice = "Snapshot not saved (" + shortErr(err) + "): the changes stay flagged."
			}
			m.auditCommit = nil
			m.bg = screenAudit
			m.screen = screenNotice
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
	m.gate = ""        // only steps-actions with a gate re-arm it below
	m.dropAuditFetch() // a previous action's, should one still be around
	switch item.id {
	case "exit":
		return m, tea.Quit
	case "viewlog":
		m.logReturn = screenMenu
		return m.openLogPicker()
	case "editconfig":
		return m, editConfig()
	case "repair":
		// pacman -Qkq must run as root (see brokenPackages), so authenticate
		// first, the same way Full Cleanup does; the scan follows.
		m.scanAfterAuth = discoverRepair()
		m.pwAttempts = 0
		return m, checkSudoCached()
	case "downloads":
		m.screen = screenDiscovering
		m.discovering = "Checking Downloads…"
		return m, discoverDownloads()
	case "services":
		m.screen = screenDiscovering
		m.discovering = "Finding unused services…"
		return m, discoverServices()
	case "auraudit":
		m.screen = screenDiscovering
		m.discovering = "Checking foreign packages against the AUR…"
		return m, discoverAURAudit()
	}

	if item.steps == nil {
		return m, nil
	}
	m.gate = item.gate
	m.pending = item.steps()
	for _, s := range m.pending {
		if s.aurAudit {
			// Start the audit now, so its lookup runs while the password
			// prompt (or the gate) is up; the step awaits it.
			m.auditFetch = startAuditFetch()
			break
		}
	}
	cmd := m.beginPending()
	return m, cmd
}

// dropAuditFetch cancels the prefetched audit, if any: the run was abandoned
// before the audit step consumed it, or the step has finished with it.
func (m *model) dropAuditFetch() {
	if m.auditFetch != nil {
		m.auditFetch.cancel()
		m.auditFetch = nil
	}
}

// startScan runs the discovery that was waiting on sudo (scanAfterAuth).
func (m model) startScan() (tea.Model, tea.Cmd) {
	cmd := m.scanAfterAuth
	m.scanAfterAuth = nil
	m.screen = screenDiscovering
	m.discovering = "Scanning for broken packages…"
	return m, cmd
}

// resetRun clears the per-run counters and auth state before a queue starts.
func (m *model) resetRun() {
	m.errs = 0
	m.skipped = 0
	m.aborted = false
	m.gateActive = false
	m.pwAttempts = 0
	m.sudoUnavailable = false
}

// beginPending starts m.pending: authenticate first if any step needs sudo
// (probe → skip prompt when cached), otherwise run the queue directly.
func (m *model) beginPending() tea.Cmd {
	m.resetRun()
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
		m.sudoGen++ // orphans the previous queue's loop if its tick is still pending
		return tea.Batch(cmd, sudoTick(m.sudoGen))
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
		m.dropAuditFetch()
		return refreshStatusNow()
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
	m.resetOutput()
	m.stepSeq++
	s := m.queue[m.current].step
	if s.run != nil {
		// In-process step: no child to signal, just a Cmd whose result
		// finishes the step. seq ties the result to this launch; ctx is
		// what an abort cancels.
		m.proc = nil
		ctx, cancel := context.WithCancel(context.Background())
		m.stepCancel = cancel
		seq, run := m.stepSeq, s.run
		if s.aurAudit && m.auditFetch != nil {
			run = m.auditFetch.run // already under way: await it, don't refetch
		}
		return func() tea.Msg { return stepResultMsg{seq: seq, stepResult: run(ctx)} }
	}
	m.stream = make(chan streamMsg, streamBatchMax)
	m.quit = make(chan struct{})
	m.proc, m.procAt = runStepUntil(s, m.stream, m.quit)
	// The spinner is already ticking (started in Init); just pump the stream.
	return waitForStream(m.stream, m.quit, m.stepSeq)
}

// stepResultMsg is an in-process step's result (see step.run).
type stepResultMsg struct {
	seq int
	stepResult
}

// sigkillGrace is how long a child gets to honour SIGINT before it's forcibly
// killed on abort. A var so tests can shrink it.
var sigkillGrace = 5 * time.Second

// abortRunning signals the active step's child to stop and returns to the menu.
// SIGINT (like a terminal ctrl+c) lets tools such as pacman exit cleanly; for
// sudo steps, sudo relays the signal to the command it runs. If the child
// ignores SIGINT it's force-killed (SIGKILL) after a grace period. Late output
// from the dying process (and a late in-process result) is dropped by its seq:
// bumping stepSeq here retires the aborted launch's number before any new run
// can start. An in-process step (the AUR audit) also has its ctx cancelled, so
// it returns at once rather than running on unseen.
func (m *model) abortRunning() {
	if m.stepCancel != nil {
		m.stepCancel()
		m.stepCancel = nil
	}
	m.dropAuditFetch()
	if p := m.proc; p != nil {
		_ = p.Signal(os.Interrupt)
		// grace read here (caller goroutine), not inside escalateKill, so the
		// spawned goroutine doesn't race a test that swaps sigkillGrace.
		go escalateKill(p, sigkillGrace)
		m.proc = nil
	}
	if m.quit != nil {
		close(m.quit) // nobody reads m.stream anymore: let runStep's goroutines finish
		m.quit = nil
	}
	m.stepSeq++         // the aborted launch is over: its late messages are stale
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
			m.dropAuditFetch()
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
			if s.step.alwaysLog { // a declined step's outcome stays on record too
				appendLog(stepCategory(s.step), "SKIP", s.step.logLabel+" (declined)", "")
			}
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
		m.dropAuditFetch()
		m.screen = screenSummary
		return m, nil
	}
	if s.step.onAccept != nil {
		s.step.onAccept() // a failure is logged by the callee and doesn't block the step
	}
	cmd := m.launch()
	return m, cmd
}

func (m model) handleStream(msg streamBatchMsg) (tea.Model, tea.Cmd) {
	// A batch from any launch but the active one is late output from an
	// aborted step (abortRunning retires its seq; a new run may have started
	// since): drop it and stop pumping. Applied, a late line would land in the
	// new step's pane and log, a late done would finish the new step, and the
	// re-pump would put a second reader on the new step's channel. This used
	// to be a screen check (menu/summary), which an abort followed by a new
	// run slips past. Popups over the running step (askpass, a confirm) don't
	// matter: its stream keeps pumping underneath. A finished run needs no
	// guard: nothing is sent after a step's done.
	if msg.seq != m.stepSeq {
		return m, nil
	}
	// The whole batch is applied in one go: one SetContent, one render.
	if len(msg.lines) > 0 {
		clean := make([]string, len(msg.lines)) // msg.lines stays raw
		for i, l := range msg.lines {
			clean[i] = Sanitize(l, SanitizeKeepSGR) // colours only
		}
		m.appendOutput(clean)
	}
	if !msg.done {
		return m, waitForStream(m.stream, m.quit, m.stepSeq) // keep pumping
	}
	return m.finishStep(msg.err)
}

// handleStepResult finishes an in-process step. A result from an earlier
// launch (the run was aborted, possibly followed by a new one) is dropped, as
// is one arriving after the run has ended.
func (m model) handleStepResult(msg stepResultMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.stepSeq || m.screen == screenMenu || m.screen == screenSummary {
		return m, nil
	}
	if m.stepCancel != nil {
		m.stepCancel() // the step is done; release its ctx
		m.stepCancel = nil
	}
	m.dropAuditFetch() // consumed (if this was the audit)
	if msg.output != "" {
		out := Sanitize(strings.TrimRight(msg.output, "\n"), SanitizeKeepSGR)
		m.appendOutput(strings.Split(out, "\n"))
		m.output.GotoTop() // a report reads top-down; its worst findings come first
	}
	if msg.confirmNext != "" && m.current+1 < len(m.queue) {
		next := &m.queue[m.current+1].step
		next.confirm = msg.confirmNext
		next.optional = true // declining skips just that step, not the queue
		next.onAccept = msg.commit
	} else if msg.confirmNext == "" && msg.commit != nil {
		msg.commit()
	}
	return m.finishStep(msg.err)
}

// finishStep records the active step's outcome (status, tallies, log entry)
// and moves on to the next one.
func (m model) finishStep(err error) (tea.Model, tea.Cmd) {
	s := &m.queue[m.current]
	cat := stepCategory(s.step)
	if err != nil {
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
