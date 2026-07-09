// Command tui is a bubbletea rewrite sketch of cleanup.sh.
//
// Status: sketch. The menu → full-cleanup-queue → live-output → summary loop
// and the log viewer are wired end to end. Per-action confirms and sudo
// priming are modelled; the services multi-select and dynamic orphan/broken-
// package discovery are stubbed (see README). It is meant to show the
// architecture, not to be feature-complete.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ── screens ──────────────────────────────────────────────────────────────────

type screen int

const (
	screenMenu screen = iota
	screenPassword
	screenConfirm
	screenRunning
	screenSummary
	screenLog
	screenNotice      // one-line result ("nothing to do") → any key back
	screenDiscovering // spinner while a discovery cmd runs
	screenServices    // multi-select checklist of services to disable
)

// maxSudoAttempts is how many wrong passwords are allowed before we give up,
// skip the sudo-requiring steps, and log them.
const maxSudoAttempts = 3

// menuItem is one row of the main menu. Static actions supply steps(); dynamic
// or special ones (discovery, editor, log, exit) dispatch on id in activate().
type menuItem struct {
	label string
	id    string
	steps func() []step
}

var menu = []menuItem{
	{label: "󰓦  Full Cleanup (Recommended)", steps: fullCleanupSteps},
	{label: "󰃨  Clear user cache", steps: clearUserCacheSteps},
	{label: "󰚰  Repair broken packages", id: "repair"},
	{label: "󰋊  Delete old downloads", id: "downloads"},
	{label: "󰏗  Run topgrade (system update)", steps: topgradeSteps},
	{label: "󰒔  Disable unused services", id: "services"},
	{label: "󰙪  Edit topgrade config", id: "editconfig"},
	{label: "󰑇  View log", id: "viewlog"},
	{label: "󰗼  Exit", id: "exit"},
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	screen screen
	width  int
	height int

	cursor  int    // menu selection
	status  string // system status line (disk/ram/cache)
	spinner spinner.Model
	output  viewport.Model // live command output + log pager

	// password-prompt state
	password        textinput.Model
	pwErr           string
	pwAttempts      int  // consecutive wrong passwords this action
	sudoUnavailable bool // set after maxSudoAttempts failures: skip sudo steps

	// dynamic-action state
	notice      string // screenNotice message
	discovering string // screenDiscovering label

	// services multi-select state
	services      []string
	serviceOn     []bool
	serviceCursor int

	// running-queue state
	queue   []stepState
	current int              // index into queue
	stream  chan streamMsg   // output channel for the active step
	buf     *strings.Builder // accumulated output of the active step (pointer:
	//                          strings.Builder must not be copied by value, and
	//                          the model is copied on every Update)
	errs    int  // steps that failed
	skipped int  // steps skipped (sudo unavailable, or aborted queue)
	aborted bool // queue aborted by declining a required confirm

	// confirm-modal state
	pending    []step // queue waiting on a confirm
	confirmMsg string
	confirmYes bool
}

func initialModel() model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = stepRunStyle

	vp := viewport.New(0, 0)

	pw := textinput.New()
	pw.Prompt = "Password: "
	pw.EchoMode = textinput.EchoPassword // mask input
	pw.EchoCharacter = '•'               // shown per typed character
	pw.PromptStyle = stepRunStyle
	pw.Cursor.Style = cursorStyle

	return model{
		screen:   screenMenu,
		spinner:  sp,
		output:   vp,
		password: pw,
		buf:      &strings.Builder{},
		status:   systemStatus(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, refreshStatus())
}

// ── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// leave room for the header and footer around the output pane
		m.output.Width = msg.Width - 4
		m.output.Height = msg.Height - 8
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
		if msg.cached { // credentials still valid — skip the prompt
			cmd := m.startQueue()
			return m, cmd
		}
		m.screen = screenPassword
		m.pwErr = ""
		m.password.Reset()
		return m, m.password.Focus()

	case sudoResultMsg:
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
			return m, nil // stay on the prompt for a retry
		}
		m.pwAttempts = 0
		m.password.Reset() // don't keep the password around
		cmd := m.startQueue()
		return m, cmd

	case discoveryMsg:
		if msg.notice != "" {
			m.notice = msg.notice
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
		switch msg.String() {
		case "esc", "ctrl+c":
			m.password.Reset()
			m.pwErr = ""
			m.screen = screenMenu
			return m, nil
		case "enter":
			return m, authSudo(m.password.Value())
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
			return m, tea.Quit // real version: SIGINT the child, then return
		}
		// let the viewport scroll while a step runs
		var cmd tea.Cmd
		m.output, cmd = m.output.Update(msg)
		return m, cmd

	case screenSummary:
		switch msg.String() {
		case "l":
			return m.openLog()
		case "enter", "esc", "q":
			m.screen = screenMenu
		}

	case screenLog:
		switch msg.String() {
		case "esc", "q":
			m.screen = screenMenu
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
		needsSudo: true,
	}}
	return m.beginPending()
}

// activate handles an Enter on the menu.
func (m model) activate() (tea.Model, tea.Cmd) {
	item := menu[m.cursor]
	switch item.id {
	case "exit":
		return m, tea.Quit
	case "viewlog":
		return m.openLog()
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
	m.pwAttempts = 0
	m.sudoUnavailable = false
	for _, s := range m.pending {
		if s.needsSudo {
			return checkSudoCached()
		}
	}
	return m.startQueue()
}

// startQueue initialises the checklist and kicks off the first step.
func (m *model) startQueue() tea.Cmd {
	m.queue = make([]stepState, len(m.pending))
	for i, s := range m.pending {
		m.queue[i] = stepState{step: s, status: statusPending}
	}
	m.pending = nil
	m.current = -1
	m.screen = screenRunning
	return m.advance()
}

// advance moves to the next step: shows a confirm if the step has one,
// otherwise launches it. When the queue is exhausted it flips to the summary.
func (m *model) advance() tea.Cmd {
	m.current++
	if m.current >= len(m.queue) {
		m.screen = screenSummary
		return refreshStatus()
	}
	s := m.queue[m.current].step
	// Sudo auth was abandoned after too many wrong passwords: skip any step
	// that needs it, note it in the log, and move on.
	if s.needsSudo && m.sudoUnavailable {
		m.queue[m.current].status = statusSkipped
		appendLog("SKIP", s.logLabel+" (sudo authentication failed)", "")
		m.skipped++ // a skip is not a failure — tallied separately
		return m.advance()
	}
	if s.confirm != "" {
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
	runStep(m.queue[m.current].step, m.stream)
	// The spinner is already ticking (started in Init); just pump the stream.
	return waitForStream(m.stream)
}

func (m model) resolveConfirm() (tea.Model, tea.Cmd) {
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
		m.screen = screenSummary
		return m, nil
	}
	cmd := m.launch()
	return m, cmd
}

func (m model) handleStream(msg streamMsg) (tea.Model, tea.Cmd) {
	if !msg.done {
		m.buf.WriteString(msg.line + "\n")
		m.output.SetContent(m.buf.String())
		m.output.GotoBottom()
		return m, waitForStream(m.stream) // keep pumping
	}

	// step finished
	s := &m.queue[m.current]
	if msg.err != nil {
		s.status = statusFailed
		m.errs++
		appendLog("FAIL", s.step.logLabel, m.buf.String())
	} else {
		s.status = statusOK
		if dryRun { // a dry run logs every action, not just failures
			appendLog("DRYRUN", s.step.logLabel, m.buf.String())
		}
	}
	cmd := m.advance()
	return m, cmd
}

func (m model) openLog() (tea.Model, tea.Cmd) {
	m.screen = screenLog
	m.output.SetContent(readLog())
	m.output.GotoTop()
	return m, nil
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.screen {
	case screenMenu:
		return m.viewMenu()
	case screenPassword:
		return m.viewPassword()
	case screenConfirm:
		return m.viewConfirm()
	case screenRunning:
		return m.viewRunning()
	case screenSummary:
		return m.viewSummary()
	case screenLog:
		return m.viewLog()
	case screenNotice:
		return m.viewNotice()
	case screenDiscovering:
		return m.viewDiscovering()
	case screenServices:
		return m.viewServices()
	}
	return ""
}

func (m model) header() string {
	line := titleStyle.Render("Arch Linux Maintenance")
	if dryRun {
		line += "  " + stepFailStyle.Render("[dry-run]")
	}
	return line + "\n" + statusStyle.Render(m.status) + "\n"
}

func (m model) viewMenu() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	for i, it := range menu {
		cursor := "  "
		style := itemStyle
		if i == m.cursor {
			cursor = cursorStyle.Render("➤ ")
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(it.label) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · enter select · q quit"))
	return b.String()
}

func (m model) viewPassword() string {
	body := stepRunStyle.Render("🔒 sudo access required") + "\n\n" +
		m.password.View() + "\n"
	if m.pwErr != "" {
		body += "\n" + stepFailStyle.Render(m.pwErr) + "\n"
	}
	return m.header() + "\n" + body + "\n" + hintStyle.Render("enter submit · esc cancel")
}

func (m model) viewConfirm() string {
	yes, no := "  Yes  ", "  No  "
	if m.confirmYes {
		yes = selectedStyle.Render("▸ Yes ")
	} else {
		no = selectedStyle.Render("▸ No ")
	}
	return m.header() + "\n" +
		stepFailStyle.Render("⚠ "+m.confirmMsg) + "\n\n" +
		yes + "   " + no + "\n\n" +
		hintStyle.Render("y/n or ←/→ then enter")
}

func (m model) viewRunning() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.checklist() + "\n")
	b.WriteString(outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n")
	b.WriteString(hintStyle.Render("↑/↓ scroll output · ctrl+c abort"))
	return b.String()
}

func (m model) checklist() string {
	var b strings.Builder
	for _, st := range m.queue {
		var glyph, text string
		switch st.status {
		case statusPending:
			glyph, text = stepPendingStyle.Render("○"), stepPendingStyle.Render(st.step.title)
		case statusRunning:
			glyph, text = m.spinner.View(), stepRunStyle.Render(st.step.title)
		case statusOK:
			glyph, text = stepOKStyle.Render("✔"), itemStyle.Render(st.step.logLabel)
		case statusFailed:
			glyph, text = stepFailStyle.Render("✖"), stepFailStyle.Render(st.step.title+" failed")
		case statusSkipped:
			glyph, text = stepSkipStyle.Render("–"), stepSkipStyle.Render(st.step.title+" (skipped)")
		}
		b.WriteString(glyph + " " + text + "\n")
	}
	return b.String()
}

func (m model) viewSummary() string {
	var line string
	switch {
	case m.aborted:
		line = stepFailStyle.Render("⚠ Aborted — remaining steps skipped")
	case m.errs > 0:
		msg := fmt.Sprintf("⚠ Completed with %d issue(s)", m.errs)
		if m.skipped > 0 {
			msg += fmt.Sprintf(", %d skipped", m.skipped)
		}
		line = stepFailStyle.Render(msg)
	case m.skipped > 0:
		line = stepSkipStyle.Render(fmt.Sprintf("Completed — %d step(s) skipped", m.skipped))
	default:
		line = stepOKStyle.Render("✔ Cleanup completed successfully")
	}
	return m.header() + "\n" + m.checklist() + "\n" + line + "\n\n" +
		hintStyle.Render("l view log · enter back to menu")
}

func (m model) viewLog() string {
	return m.header() + "\n" +
		outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n" +
		hintStyle.Render("↑/↓ scroll · q back")
}

func (m model) viewNotice() string {
	return m.header() + "\n" + itemStyle.Render(m.notice) + "\n\n" +
		hintStyle.Render("any key to return")
}

func (m model) viewDiscovering() string {
	return m.header() + "\n" +
		m.spinner.View() + " " + stepRunStyle.Render(m.discovering) + "\n"
}

func (m model) viewServices() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(stepRunStyle.Render("Enabled but not running — select services to disable:") + "\n\n")
	for i, svc := range m.services {
		cursor := "  "
		if i == m.serviceCursor {
			cursor = cursorStyle.Render("➤ ")
		}
		box := "[ ]"
		if m.serviceOn[i] {
			box = selectedStyle.Render("[x]")
		}
		b.WriteString(cursor + box + " " + itemStyle.Render(svc) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · space toggle · enter disable · esc cancel"))
	return b.String()
}

// ── system status ────────────────────────────────────────────────────────────

type statusMsg string

// refreshStatus recomputes the disk/ram/cache line every few seconds so the
// header stays live (the bit gum couldn't do without redrawing the world).
func refreshStatus() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return statusMsg(systemStatus())
	})
}

func systemStatus() string {
	disk := shellField("df -h / | awk 'NR==2{print $4}'")
	mem := shellField("free -h | awk '/^Mem:/{print $4}'")
	cache := shellField("du -sh /var/cache/pacman/pkg 2>/dev/null | awk '{print $1}'")
	if cache == "" {
		cache = "N/A"
	}
	return fmt.Sprintf(" Free: %s   RAM free: %s   Cache: %s", disk, mem, cache)
}

func shellField(cmd string) string {
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "--dryrun" {
			dryRun = true
		}
	}

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
