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
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ── screens ──────────────────────────────────────────────────────────────────

type screen int

const (
	screenMenu screen = iota
	screenConfirm
	screenRunning
	screenSummary
	screenLog
)

// menuItem is one row of the main menu; steps() yields its work queue.
type menuItem struct {
	label string
	steps func() []step
}

var menu = []menuItem{
	{"󰓦  Full Cleanup (Recommended)", fullCleanupSteps},
	{"󰃨  Clear user cache", clearUserCacheSteps},
	{"󰚰  Repair broken packages", nil}, // stub: dynamic pacman -Qk discovery
	{"󰋊  Delete old downloads", nil},    // stub: find ~/Downloads -mtime +30
	{"󰏗  Run topgrade (system update)", topgradeSteps},
	{"󰒔  Disable unused services", nil}, // stub: bubbles/list multi-select
	{"󰙪  Edit topgrade config", nil},    // stub: tea.ExecProcess($EDITOR)
	{"󰑇  View log", nil},                 // handled specially → screenLog
	{"󰗼  Exit", nil},
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

	// running-queue state
	queue   []stepState
	current int              // index into queue
	stream  chan streamMsg   // output channel for the active step
	buf     *strings.Builder // accumulated output of the active step (pointer:
	//                          strings.Builder must not be copied by value, and
	//                          the model is copied on every Update)
	errs int // failed steps so far

	// confirm-modal state
	pending   []step // queue waiting on a confirm
	confirmMsg string
	confirmYes bool
}

func initialModel() model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = stepRunStyle

	vp := viewport.New(0, 0)

	return model{
		screen:  screenMenu,
		spinner: sp,
		output:  vp,
		buf:     &strings.Builder{},
		status:  systemStatus(),
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

	case sudoPrimedMsg:
		if msg.err != nil { // auth failed/cancelled → back to menu
			m.screen = screenMenu
			return m, nil
		}
		cmd := m.startQueue()
		return m, cmd

	case streamMsg:
		return m.handleStream(msg)
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
	}
	return m, nil
}

// activate handles an Enter on the menu.
func (m model) activate() (tea.Model, tea.Cmd) {
	label := menu[m.cursor].label
	switch {
	case strings.Contains(label, "Exit"):
		return m, tea.Quit
	case strings.Contains(label, "View log"):
		return m.openLog()
	}

	item := menu[m.cursor]
	if item.steps == nil {
		// stubbed action — no-op in the sketch
		return m, nil
	}

	m.pending = item.steps()
	m.errs = 0
	// If any step needs sudo, prime it before entering the queue.
	for _, s := range m.pending {
		if s.needsSudo {
			return m, primeSudo()
		}
	}
	cmd := m.startQueue()
	return m, cmd
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
			cmd := m.advance()
			return m, cmd
		}
		// non-optional "no" aborts the remaining queue
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
		appendLog(s.step.logLabel, m.buf.String()) // only log on failure
	} else {
		s.status = statusOK
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
	case screenConfirm:
		return m.viewConfirm()
	case screenRunning:
		return m.viewRunning()
	case screenSummary:
		return m.viewSummary()
	case screenLog:
		return m.viewLog()
	}
	return ""
}

func (m model) header() string {
	return titleStyle.Render("Arch Linux Maintenance") + "\n" +
		statusStyle.Render(m.status) + "\n"
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
	if m.errs > 0 {
		line = stepFailStyle.Render(fmt.Sprintf("⚠ Completed with %d issue(s)", m.errs))
	} else {
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

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
