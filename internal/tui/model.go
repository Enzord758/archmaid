package tui

import (
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
	screenDiscovering  // spinner while a discovery runs
	screenServices     // multi-select checklist of services to disable
	screenAbortConfirm // ctrl+c while running → confirm before stopping the step
	screenAudit        // scrollable AUR-audit report (see audit.go)
)

// ── model ────────────────────────────────────────────────────────────────────

// model is the TUI: what it shows, derived from the engine's Events
// (adapter.go), and the TUI's own state (the menu, the log viewer, the
// output pane's scroll).
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
	password     textinput.Model
	pwErr        string
	authInFlight bool             // a password was sent and is being checked: input locked
	askpassFrom  askpassRequester // who the open askpass prompt is for, for the popup

	// dynamic-action state
	notice string // screenNotice message
	// noticeLog names the log a discovery's record went to ("Services
	// check"), shown under its notice; "" for any other notice.
	noticeLog   string
	discovering string // screenDiscovering label

	// services multi-select state
	services      []string
	serviceOn     []bool
	serviceCursor int

	// log viewer state
	logCursor int         // screenLogPicker selection
	logCat    logCategory // category currently open in screenLog
	logReturn screen      // screen to return to on esc from screenLogPicker

	// running-queue state
	queue   []stepState
	current int       // index into queue
	tail    *lineTail // the newest lines of the running step: what the output pane shows (see output.go)

	errs    int  // steps that failed
	skipped int  // steps skipped (refused, declined or aborted)
	aborted bool // queue stopped by declining a required confirm
	// runNote is a note for the whole run, shown above the checklist
	// while it runs: the up-front password's AuthResolved Detail
	// (privilege.NoTicketNote: sudo keeps no ticket, so each sudo asks
	// through the popup). "" for none; cleared when the action ends.
	runNote string

	// confirm-modal state: gateActive marks the action's proceed-gate, as
	// opposed to a step's confirm.
	confirmMsg string
	confirmYes bool
	gateActive bool

	// eng is the engine side of the model (adapter.go): the Driver the
	// model runs its actions on, and the prompts it has open. nil only in
	// a test that just draws the model.
	eng *engineState
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
		tail:     newLineTail(maxVisibleLines),
		status:   systemStatus(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, refreshStatus())
}
