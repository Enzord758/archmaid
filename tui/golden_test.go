package main

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// Frame goldens for the screens and popups the headless-program goldens in
// teatest_test.go don't reach. These render View() directly instead of running
// a teatest program: a live program ticks the spinner and the input cursor, so
// its frames drift. Each model is fixedModel() (pinned status line) at 80×24
// under TestMain's ASCII profile, driven through Update where that's practical
// and hand-built where the real path needs a child process.

const goldenW, goldenH = 80, 24

// sizedModel is fixedModel() after the terminal reports its size.
func sizedModel() model {
	return update(fixedModel(), tea.WindowSizeMsg{Width: goldenW, Height: goldenH})
}

// viewGolden golden-compares m's frame. It re-sends the window size first so
// the output pane is budgeted for whatever screen and queue the test built.
func viewGolden(t *testing.T, m model) {
	t.Helper()
	m = update(m, tea.WindowSizeMsg{Width: goldenW, Height: goldenH})
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// goldenQueue builds a checklist from titled steps with the given statuses.
func goldenQueue(statuses ...stepStatus) []stepState {
	steps := []step{
		{title: "Auditing AUR packages…", logLabel: "AUR audit"},
		{title: "Cleaning package cache…", logLabel: "Old package cache cleaned"},
		{title: "Removing orphaned packages…", logLabel: "Orphaned packages removed"},
		{title: "Vacuuming logs…", logLabel: "Old logs removed"},
	}
	q := make([]stepState, len(statuses))
	for i, st := range statuses {
		q[i] = stepState{step: steps[i], status: st}
	}
	return q
}

// TestSummaryGolden: the summary screen for each outcome line.
func TestSummaryGolden(t *testing.T) {
	cases := []struct {
		name    string
		queue   []stepState
		errs    int
		skipped int
		aborted bool
	}{
		{name: "ok", queue: goldenQueue(statusOK, statusOK, statusOK, statusOK)},
		{name: "issues", queue: goldenQueue(statusOK, statusFailed, statusSkipped, statusOK), errs: 1, skipped: 1},
		{name: "skipped", queue: goldenQueue(statusOK, statusSkipped, statusSkipped, statusOK), skipped: 2},
		{name: "aborted", queue: goldenQueue(statusOK, statusOK, statusSkipped, statusSkipped), skipped: 2, aborted: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := sizedModel()
			m.screen = screenSummary
			m.queue = c.queue
			m.current = len(c.queue)
			m.errs, m.skipped, m.aborted = c.errs, c.skipped, c.aborted
			viewGolden(t, m)
		})
	}
}

// passwordOverMenu is the prime prompt as an uncached sudo probe opens it,
// with a typed (masked) password.
func passwordOverMenu(t *testing.T) model {
	t.Helper()
	m := update(sizedModel(), sudoCachedMsg{cached: false})
	m = update(m, key("hunter2"))
	if m.screen != screenPassword || m.bg != screenMenu {
		t.Fatalf("screen=%d bg=%d, want the password prompt over the menu", m.screen, m.bg)
	}
	return m
}

// TestPasswordGolden: the password popup over the menu, typed input masked.
func TestPasswordGolden(t *testing.T) {
	viewGolden(t, passwordOverMenu(t))
}

// TestPasswordErrorGolden: the same popup after a wrong password, with the
// attempts-left line.
func TestPasswordErrorGolden(t *testing.T) {
	m := passwordOverMenu(t)
	m = update(m, sudoResultMsg{err: errors.New("auth failed")})
	if m.pwErr == "" {
		t.Fatal("no error text after a wrong password")
	}
	viewGolden(t, m)
}

// TestConfirmOverRunningGolden: a per-step confirm over the running checklist.
// The confirm is asked before its step starts, so that step is still pending
// and no spinner frame is drawn.
func TestConfirmOverRunningGolden(t *testing.T) {
	m := sizedModel()
	m.queue = goldenQueue(statusOK, statusOK, statusPending, statusPending)
	m.current = 2
	m.output.SetContent("==> finished: Old package cache cleaned")
	m.bg = screenRunning
	m.screen = screenConfirm
	m.confirmMsg = "Remove 3 orphaned package(s)?"
	m.confirmYes = false
	viewGolden(t, m)
}

// TestLogPickerGolden: the View-log category menu with the cursor on row 1.
func TestLogPickerGolden(t *testing.T) {
	m := sizedModel()
	m.cursor = menuIndex(t, "View log")
	m = update(m, key("enter"))
	m = update(m, key("down"))
	if m.screen != screenLogPicker || m.logCursor != 1 {
		t.Fatalf("screen=%d logCursor=%d, want the log picker on row 1", m.screen, m.logCursor)
	}
	viewGolden(t, m)
}

// TestAuditReportGolden: the audit screen showing a report rendered from a
// hand-built result (classify stamps ages from the clock, so it isn't used).
func TestAuditReportGolden(t *testing.T) {
	res := auditResult{
		total:    6,
		baseline: baselineOK,
		findings: []auditFinding{
			{name: "some-aur-tool", high: true, flags: []string{"maintainer changed: alice → mallory"}},
			{name: "orphaned-thing-git", flags: []string{"orphaned", "0 votes"}},
			{name: "fresh-pkg", flags: []string{"modified 5h ago"}},
		},
	}
	m := update(sizedModel(), discoveryMsg{kind: "audit", report: renderAudit(res)})
	if m.screen != screenAudit {
		t.Fatalf("screen = %d, want screenAudit", m.screen)
	}
	viewGolden(t, m)
}

// TestNoticeGolden: a discovery notice popup over the menu.
func TestNoticeGolden(t *testing.T) {
	m := update(sizedModel(), discoveryMsg{kind: "downloads", notice: "Nothing in Downloads is older than 30 days."})
	if m.screen != screenNotice || m.bg != screenMenu {
		t.Fatalf("screen=%d bg=%d, want the notice over the menu", m.screen, m.bg)
	}
	viewGolden(t, m)
}

// TestDiscoveringGolden: the spinner screen while a discovery runs. The
// discovery command is never executed (update drops it), and the spinner is
// on its initial frame since no tick is delivered.
func TestDiscoveringGolden(t *testing.T) {
	m := sizedModel()
	m.cursor = menuIndex(t, "Disable unused services")
	m = update(m, key("enter"))
	if m.screen != screenDiscovering {
		t.Fatalf("screen = %d, want screenDiscovering", m.screen)
	}
	viewGolden(t, m)
}
