package tui

import "github.com/charmbracelet/lipgloss"

// Tokyo Night Midnight palette — same values the bash version exported to gum,
// so the two look like the same tool.
const (
	colFg     = "#c0caf5"
	colBlue   = "#7aa2f7"
	colCyan   = "#7dcfff"
	colGreen  = "#9ece6a"
	colRed    = "#f7768e"
	colYellow = "#e0af68"
	colPurple = "#bb9af7"
	colGray   = "#565f89"
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colPurple)).Bold(true)
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colGray))
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colGray))
	// noteStyle is the running screen's note for the run (runNote).
	noteStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colYellow))

	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)).Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)).Bold(true)
	itemStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colFg))

	// Per-step checklist glyphs, mirroring start_msg/end_msg/fail_msg.
	stepPendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colGray))
	stepRunStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)).Bold(true)
	stepOKStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)).Bold(true)
	stepFailStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colRed)).Bold(true)
	stepSkipStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colGray))

	outputPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBlue)).
			Padding(0, 1)

	// popupBox is the floating dialog frame composited over the current screen.
	popupBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colPurple)).
			Padding(1, 3)
)
