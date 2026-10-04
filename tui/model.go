package main

import (
	"context"
	"net"
	"os"
	"strings"

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
	screenLogPicker // "View log" category menu (topgrade / orphans / cleanup)
	screenLog
	screenNotice       // one-line result ("nothing to do") → any key back
	screenDiscovering  // spinner while a discovery cmd runs
	screenServices     // multi-select checklist of services to disable
	screenAbortConfirm // ctrl+c while running → confirm before killing the child
	screenAudit        // scrollable AUR-audit report (see audit.go)
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
	// gate, if set, is a confirm shown before a steps-action runs *only when
	// sudo is already cached* — otherwise the sudo password prompt is the gate.
	gate string
}

var menu = []menuItem{
	{label: "󰓦  Full Cleanup (Recommended)", steps: fullCleanupSteps, gate: "Proceed with full cleanup?"},
	{label: "󰃨  Clear user cache", steps: clearUserCacheSteps},
	{label: "󰚰  Repair broken packages", id: "repair"},
	{label: "󰋊  Delete old downloads", id: "downloads"},
	{label: "󰏗  Run topgrade (system update)", steps: topgradeSteps},
	{label: "󰒔  Disable unused services", id: "services"},
	{label: "󰶚  Audit AUR packages", id: "auraudit"},
	{label: "󰙪  Edit topgrade config", id: "editconfig"},
	{label: "󰑇  View log", id: "viewlog"},
	{label: "󰗼  Exit", id: "exit"},
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	screen screen // active screen; for popup screens, drawn over bg
	bg     screen // background screen to composite a popup over

	cursor  int    // menu selection
	status  string // system status line (disk/ram/cache)
	spinner spinner.Model
	output  viewport.Model // live command output + log pager

	// termWidth/termHeight are the last-seen terminal size, cached so the
	// output pane can be resized outside of a WindowSizeMsg too — e.g. when
	// the running screen's checklist grows the chrome around it (see
	// resizeOutput).
	termWidth, termHeight int

	// password-prompt state
	password        textinput.Model
	pwErr           string
	pwAttempts      int              // consecutive wrong passwords this action
	authInFlight    bool             // prime submitted, waiting on PAM — input locked
	sudoUnavailable bool             // set after maxSudoAttempts failures: skip sudo steps
	sudoAlive       bool             // keep-alive loop active for the running queue
	sudoGen         int              // generation of the live keep-alive loop; stale ticks are dropped
	askpassConn     net.Conn         // pending sudo askpass request (nil = prime-mode prompt)
	askpassFrom     askpassRequester // who askpassConn's request came from, for the popup

	// dynamic-action state
	notice      string // screenNotice message
	discovering string // screenDiscovering label

	// services multi-select state
	services      []string
	serviceOn     []bool
	serviceCursor int

	// log viewer state
	logCursor int         // screenLogPicker selection
	logCat    logCategory // category currently open in screenLog
	logReturn screen      // screen to return to on esc from screenLogPicker

	// auditCommit accepts the open audit report's high findings into the
	// maintainer snapshot (`a` on screenAudit); nil when there's nothing to
	// accept. Leaving the screen drops it.
	auditCommit func() error

	// running-queue state
	queue   []stepState
	current int              // index into queue
	proc    *os.Process      // the active step's child process (for abort)
	procAt  uint64           // proc's start time, to tie askpass requests to it (see verifyAskpassPeer)
	stream  chan streamMsg   // output channel for the active step
	quit    chan struct{}    // closed on abort: releases the active step's stream goroutines
	stepSeq int              // bumped per launch and on abort; stream messages and in-process results carry it so stale ones are dropped
	buf     *strings.Builder // accumulated output of the active step (pointer:
	//                          strings.Builder must not be copied by value, and
	//                          the model is copied on every Update)
	tail *lineTail // the newest lines of buf: what the output pane shows (see output.go)

	errs    int  // steps that failed
	skipped int  // steps skipped (sudo unavailable, or aborted queue)
	aborted bool // queue aborted by declining a required confirm

	// stepCancel cancels the running in-process step's ctx (see launch); an
	// abort calls it so the step returns at once. nil when none is running.
	stepCancel context.CancelFunc
	// auditFetch is the current action's AUR audit, started when the action
	// was chosen so it runs behind the password prompt (see startAuditFetch).
	// nil when the action has none or it was dropped (dropAuditFetch).
	auditFetch *auditFetch

	// confirm-modal state
	pending    []step // queue waiting on a confirm
	confirmMsg string
	confirmYes bool

	// proceed-gate state: gate is the message to show when sudo is cached (no
	// password prompt to act as the gate); gateActive marks that the current
	// confirm is the gate rather than a per-step confirm.
	gate       string
	gateActive bool

	// scanAfterAuth is a discovery that needs root (Repair's pacman -Qkq): it
	// runs once the sudo ticket is primed, in place of starting a queue.
	scanAfterAuth tea.Cmd
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
		tail:     newLineTail(maxVisibleLines),
		status:   systemStatus(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, refreshStatus())
}
