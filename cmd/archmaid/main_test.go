package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// key builds a KeyMsg from a string the same way handleKey matches on it.
func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// update runs one Update and type-asserts the result back to model.
func update(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

// drainStep reads a step's stream to completion, returning the collected output
// lines and the terminal error.
func drainStep(ch <-chan proc.Msg) ([]string, error) {
	var lines []string
	for msg := range func() chan proc.Msg {
		out := make(chan proc.Msg)
		go func() {
			for {
				m := <-ch
				out <- m
				if m.Done {
					close(out)
					return
				}
			}
		}()
		return out
	}() {
		if msg.Done {
			return lines, msg.Err
		}
		lines = append(lines, msg.Line)
	}
	return lines, nil
}

// TestDryRunLogsEveryStep verifies a successful dry-run step is logged to the
// dry-run log (normal runs only log failures).
func TestDryRunLogsEveryStep(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()
	t.Setenv("HOME", t.TempDir())

	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: steps.Step{LogLabel: "Test step"}, status: steps.Running}}
	m.current = 0
	m.buf.WriteString("[dry-run] would run: echo hi\n")

	tm, _ := m.handleStream(streamBatchMsg{done: true})
	_ = tm

	data, err := os.ReadFile(logPath(logCategoryCleanup))
	if err != nil {
		t.Fatalf("dry-run log not written: %v", err)
	}
	if !strings.Contains(string(data), "DRYRUN: Test step") {
		t.Fatalf("expected DRYRUN entry, got:\n%s", data)
	}
}

// TestDiscoveryNotice: a discovery with a notice routes to the notice screen.
func TestDiscoveryNotice(t *testing.T) {
	m := initialModel()
	tm, _ := m.Update(discoveryMsg{d: actions.Notice{Text: "nothing to do"}})
	got := tm.(model)
	if got.screen != screenNotice || got.notice != "nothing to do" {
		t.Fatalf("expected notice screen, got screen=%d notice=%q", got.screen, got.notice)
	}
}

// TestDiscoveryServices: a services discovery routes to the multi-select with a
// selection slice sized to the list.
func TestDiscoveryServices(t *testing.T) {
	m := initialModel()
	svcs := []string{"a.service", "b.service"}
	tm, _ := m.Update(discoveryMsg{d: actions.NeedsSelection{Items: svcs, Steps: actions.DisableUnits}})
	got := tm.(model)
	if got.screen != screenServices {
		t.Fatalf("expected services screen, got %d", got.screen)
	}
	if len(got.serviceOn) != len(svcs) {
		t.Fatalf("serviceOn size %d, want %d", len(got.serviceOn), len(svcs))
	}
}

// ── behaviour 1: dry-run safety across the whole full-cleanup queue ───────────

// TestDryRunFullCleanupExecutesNothing runs every step in the Full Cleanup
// queue under --dryrun and asserts none of them actually executes: each emits a
// "would run" line and nothing else.
func TestDryRunFullCleanupExecutesNothing(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	for _, s := range fullCleanupSteps() {
		if s.Run != nil {
			continue // in-process (the AUR audit): read-only by contract, covered in fullcleanup_audit_test.go
		}
		ch := make(chan proc.Msg, 8)
		runStep(s, ch, nil)
		lines, err := drainStep(ch)
		if err != nil {
			t.Fatalf("dry-run step %q errored: %v", s.Title, err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "would run") {
			t.Fatalf("step %q: expected exactly one 'would run' line, got %v", s.Title, lines)
		}
	}
}

// TestCheckSudoCachedDryRunNeverProbes: under --dryrun the sudo cache probe must
// report cached without ever shelling out to sudo, so no prompt appears.
func TestCheckSudoCachedDryRunNeverProbes(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	msg := checkSudoCached()().(sudoCachedMsg)
	if !msg.cached {
		t.Fatal("dry-run sudo probe must report cached (never prompt)")
	}
}

// ── behaviour 2: sudo flow ────────────────────────────────────────────────────

// TestSudoCachedSkipsPrompt: a cached probe result jumps straight into the
// running queue without visiting the password screen.
func TestSudoCachedSkipsPrompt(t *testing.T) {
	m := initialModel()
	m.pending = []steps.Step{{Title: "x", LogLabel: "x", Argv: []string{"true"}, NeedsPriv: true}}

	got := update(m, sudoCachedMsg{cached: true})
	if got.screen != screenRunning {
		t.Fatalf("cached sudo should start the queue (screenRunning), got screen=%d", got.screen)
	}
}

// TestSudoUncachedShowsPassword: an uncached probe result routes to the masked
// password prompt.
func TestSudoUncachedShowsPassword(t *testing.T) {
	m := initialModel()
	got := update(m, sudoCachedMsg{cached: false})
	if got.screen != screenPassword {
		t.Fatalf("uncached sudo should show the password screen, got screen=%d", got.screen)
	}
}

// TestWrongPasswordShowsAttemptsLeft: a wrong password increments the attempt
// counter and surfaces the number of tries remaining, staying on the prompt.
func TestWrongPasswordShowsAttemptsLeft(t *testing.T) {
	m := initialModel()
	m.screen = screenPassword

	got := update(m, sudoResultMsg{err: errors.New("nope")})
	if got.pwAttempts != 1 {
		t.Fatalf("pwAttempts = %d, want 1", got.pwAttempts)
	}
	left := maxSudoAttempts - 1
	if !strings.Contains(got.pwErr, "attempt") || !strings.Contains(got.pwErr, itoa(left)) {
		t.Fatalf("pwErr = %q, want it to mention %d attempts left", got.pwErr, left)
	}
	if got.sudoUnavailable {
		t.Fatal("sudo should not be marked unavailable after a single failure")
	}
}

// TestSudoThreeStrikesSkipsAndLogs: after maxSudoAttempts wrong passwords, sudo
// is abandoned, the queue still runs, and each sudo step is skipped + logged.
func TestSudoThreeStrikesSkipsAndLogs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Hermetic real-log dir: never touch /var/log or the user's state dir.
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	m := initialModel()
	m.pending = []steps.Step{{Title: "root job", LogLabel: "needs root", NeedsPriv: true, Argv: []string{"true"}}}

	got := m
	for i := 0; i < maxSudoAttempts; i++ {
		got = update(got, sudoResultMsg{err: errors.New("bad password")})
	}
	if !got.sudoUnavailable {
		t.Fatalf("expected sudoUnavailable after %d failures", maxSudoAttempts)
	}
	if got.screen != screenSummary {
		t.Fatalf("queue should have run to the summary, got screen=%d", got.screen)
	}
	if got.queue[0].status != steps.AuthRefused {
		t.Fatalf("sudo step status = %d, want steps.AuthRefused", got.queue[0].status)
	}
	if got.errs != 0 {
		t.Fatalf("errs = %d, want 0 (a skip is not a failure)", got.errs)
	}
	if got.skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the skipped sudo step)", got.skipped)
	}
	data, _ := os.ReadFile(logPath(logCategoryCleanup))
	if !strings.Contains(string(data), "SKIP: needs root (sudo authentication failed)") {
		t.Fatalf("missing SKIP log entry, got:\n%s", data)
	}
}

// ── behaviour 3: confirm flow ──────────────────────────────────────────────────

// TestConfirmStepShowsModal: a step carrying a confirm string pauses the queue
// on the confirm screen before running.
func TestConfirmStepShowsModal(t *testing.T) {
	m := initialModel()
	m.pending = []steps.Step{{Title: "risky", LogLabel: "risky", Argv: []string{"true"},
		Confirm: "Are you sure?", Optional: true}}

	got := update(m, sudoCachedMsg{cached: true}) // no sudo needed, but this drives startQueue
	// startQueue → advance → confirm screen
	if got.screen != screenConfirm || got.confirmMsg != "Are you sure?" {
		t.Fatalf("expected confirm modal, got screen=%d msg=%q", got.screen, got.confirmMsg)
	}
	if got.confirmYes {
		t.Fatal("confirm should default to No")
	}
}

// TestConfirmOptionalNoSkips: declining an optional step skips just that step
// and advances the queue.
func TestConfirmOptionalNoSkips(t *testing.T) {
	m := initialModel()
	m.queue = []stepState{
		{step: steps.Step{Title: "opt", Confirm: "?", Optional: true}, status: steps.Pending},
		{step: steps.Step{Title: "next", Argv: []string{"true"}}, status: steps.Pending},
	}
	m.current = 0
	m.screen = screenConfirm
	m.confirmYes = false

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.queue[0].status != steps.Skipped {
		t.Fatalf("optional step should be skipped, status=%d", got.queue[0].status)
	}
	if got.current != 1 {
		t.Fatalf("queue should have advanced to step 1, current=%d", got.current)
	}
}

// TestConfirmNonOptionalNoAborts: declining a non-optional step aborts the whole
// queue to the summary and marks the declined step and everything after it as
// skipped (so the summary reflects what didn't run).
func TestConfirmNonOptionalNoAborts(t *testing.T) {
	m := initialModel()
	m.queue = []stepState{
		{step: steps.Step{Title: "must", Confirm: "?", Optional: false}, status: steps.Pending},
		{step: steps.Step{Title: "next"}, status: steps.Pending},
	}
	m.current = 0
	m.screen = screenConfirm
	m.confirmYes = false

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.screen != screenSummary {
		t.Fatalf("non-optional no should abort to summary, got screen=%d", got.screen)
	}
	if !got.aborted {
		t.Fatal("aborted flag should be set")
	}
	if got.queue[0].status != steps.Skipped || got.queue[1].status != steps.Skipped {
		t.Fatalf("aborted steps should be skipped, got %d and %d",
			got.queue[0].status, got.queue[1].status)
	}
	if got.skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (declined step + the one after)", got.skipped)
	}
}

// TestConfirmYesLaunches: accepting a confirm launches the step (running status,
// running screen).
func TestConfirmYesLaunches(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	m.queue = []stepState{{step: steps.Step{Title: "go", LogLabel: "go", Argv: []string{"true"}, Confirm: "?"}, status: steps.Pending}}
	m.current = 0
	m.screen = screenConfirm
	m.confirmYes = true

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.screen != screenRunning {
		t.Fatalf("yes should launch (screenRunning), got screen=%d", got.screen)
	}
	if got.queue[0].status != steps.Running {
		t.Fatalf("launched step status=%d, want steps.Running", got.queue[0].status)
	}
}

// ── behaviour 4: streaming order + failure ────────────────────────────────────

// TestStreamFailurePropagates: a failing command captures its output, reports a
// non-nil error, and handleStream marks it failed, bumps errs, and logs FAIL
// with the captured body.
func TestStreamFailurePropagates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Hermetic real-log dir: never touch /var/log or the user's state dir.
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	ch := make(chan proc.Msg, 16)
	runStep(steps.Step{Argv: []string{"bash", "-c", "echo boom; exit 3"}}, ch, nil)
	lines, err := drainStep(ch)
	if err == nil {
		t.Fatal("failing command should report a non-nil error")
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Fatalf("captured output = %v, want [boom]", lines)
	}

	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: steps.Step{Title: "boom", LogLabel: "Boom step"}, status: steps.Running}}
	m.current = 0
	m.buf.WriteString(strings.Join(lines, "\n") + "\n")

	next, _ := m.handleStream(streamBatchMsg{done: true, err: err})
	got := next.(model)
	if got.queue[0].status != steps.Failed {
		t.Fatalf("status = %d, want steps.Failed", got.queue[0].status)
	}
	if got.errs != 1 {
		t.Fatalf("errs = %d, want 1", got.errs)
	}
	data, _ := os.ReadFile(logPath(logCategoryCleanup))
	if !strings.Contains(string(data), "FAIL: Boom step") || !strings.Contains(string(data), "boom") {
		t.Fatalf("expected FAIL log with body, got:\n%s", data)
	}
}

// ── behaviour 5: discovery routing ────────────────────────────────────────────

// TestDiscoveryPendingRunsQueue: a discovery that yields runnable (non-sudo)
// steps flows into the running queue.
func TestDiscoveryPendingRunsQueue(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	got := update(m, discoveryMsg{d: actions.Pending{Steps: []steps.Step{
		{Title: "del", LogLabel: "del", Argv: []string{"true"}},
	}}})
	if got.screen != screenRunning {
		t.Fatalf("pending discovery should start running, got screen=%d", got.screen)
	}
}

// TestDiscoveryPendingSudoProbes: a discovery whose steps need sudo goes through
// the sudo probe; under dry-run the probe reports cached and the queue runs.
func TestDiscoveryPendingSudoProbes(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	next, cmd := m.Update(discoveryMsg{d: actions.Pending{Steps: []steps.Step{
		{Title: "fix", LogLabel: "fix", Argv: []string{"sudo"}, NeedsPriv: true},
	}}})
	if cmd == nil {
		t.Fatal("expected a sudo-probe command")
	}
	got := next.(model)
	// The probe hasn't resolved yet; feeding its message drives the queue.
	got = update(got, cmd().(sudoCachedMsg))
	if got.screen != screenRunning {
		t.Fatalf("cached sudo probe should start the queue, got screen=%d", got.screen)
	}
}

// TestDiscoverServicesRuns exercises the real services pipeline end to end: it
// must not panic and must return a well-formed discoveryMsg (either a services
// list or a "nothing to do" notice).
func TestDiscoverServicesRuns(t *testing.T) {
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		t.Skip("systemctl not available")
	}
	msg := discoverNow(t, actions.IDServices)
	sel, _ := msg.d.(actions.NeedsSelection)
	if len(sel.Items) == 0 && noticeOf(msg.d) == "" {
		t.Fatal("expected either services or a notice")
	}
	// Any returned service line must be clean (trimmed, non-empty).
	for _, s := range sel.Items {
		if s != strings.TrimSpace(s) || s == "" {
			t.Fatalf("unclean service line %q", s)
		}
	}
}

// ── behaviour 6: services multi-select ────────────────────────────────────────

// TestServiceToggle: space toggles the selection under the cursor.
func TestServiceToggle(t *testing.T) {
	m := initialModel()
	m.screen = screenServices
	m.services = []string{"a.service", "b.service"}
	m.serviceOn = make([]bool, 2)
	m.serviceCursor = 1

	got := update(m, key(" "))
	if !got.serviceOn[1] {
		t.Fatal("space should toggle serviceOn[cursor] on")
	}
	got = update(got, key(" "))
	if got.serviceOn[1] {
		t.Fatal("space should toggle serviceOn[cursor] back off")
	}
}

// TestServicesEnterBuildsDisableStep: enter with a selection builds a
// `systemctl disable --now …` sudo step from the checked services.
func TestServicesEnterBuildsDisableStep(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	m.screen = screenServices
	m.services = []string{"a.service", "b.service", "c.service"}
	m.serviceOn = []bool{true, false, true}
	m.selectSteps = actions.DisableUnits

	m.confirmServices() // mutates m.pending in place
	if len(m.pending) != 1 {
		t.Fatalf("expected one disable step, got %d", len(m.pending))
	}
	s := m.pending[0]
	if s.Command() != "sudo" || !s.NeedsPriv {
		t.Fatalf("disable step should be a sudo step, got %+v", s)
	}
	joined := strings.Join(s.Args(), " ")
	if !strings.HasPrefix(joined, "systemctl disable --now ") {
		t.Fatalf("args = %v, want systemctl disable --now …", s.Args())
	}
	if !strings.Contains(joined, "a.service") || !strings.Contains(joined, "c.service") ||
		strings.Contains(joined, "b.service") {
		t.Fatalf("selected services wrong, args = %v", s.Args())
	}
}

// TestServicesEnterNoneReturnsMenu: enter with nothing selected is a no-op that
// returns to the menu.
func TestServicesEnterNoneReturnsMenu(t *testing.T) {
	m := initialModel()
	m.screen = screenServices
	m.services = []string{"a.service"}
	m.serviceOn = []bool{false}

	m.confirmServices()
	if m.screen != screenMenu {
		t.Fatalf("empty selection should return to menu, got screen=%d", m.screen)
	}
	if m.pending != nil {
		t.Fatalf("no step should be queued, got %v", m.pending)
	}
}

// ── behaviour 7: parsers ──────────────────────────────────────────────────────

// TestNonEmptyLinesTrimsAndDropsBlanks: the shared line parser trims whitespace
// and drops empty lines (dedup itself is done by `sort -u` in the shell).
func TestNonEmptyLinesTrimsAndDropsBlanks(t *testing.T) {
	in := "  a.service \n\n\tb.service\n   \nc.service\n"
	got := nonEmptyLines(in)
	want := []string{"a.service", "b.service", "c.service"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("nonEmptyLines = %v, want %v", got, want)
	}
	if len(nonEmptyLines("")) != 0 || len(nonEmptyLines("   \n\n")) != 0 {
		t.Fatal("blank input should yield no lines")
	}
}

// ── behaviour 8: log categories + view-log picker ──────────────────────────────

// TestStepCategoryDefaultsToCleanup: a step with no category set (the common
// case) buckets into "cleanup", not an empty/zero category.
func TestStepCategoryDefaultsToCleanup(t *testing.T) {
	if got := (steps.Step{}).LogCategory(); got != logCategoryCleanup {
		t.Fatalf("zero Step.LogCategory() = %q, want %q", got, logCategoryCleanup)
	}
}

// TestFailureLogsToItsStepCategory: a failing step's FAIL entry lands in its
// own category's file, not a shared one — topgrade and cleanup steps failing
// in the same run must not land in the same log.
func TestFailureLogsToItsStepCategory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{
		step:   steps.Step{Title: "update", LogLabel: "Topgrade completed", Category: logCategoryTopgrade},
		status: steps.Running,
	}}
	m.current = 0
	m.buf.WriteString("boom\n")

	next, _ := m.handleStream(streamBatchMsg{done: true, err: errors.New("fail")})
	_ = next.(model)

	topgradeLog, _ := os.ReadFile(logPath(logCategoryTopgrade))
	if !strings.Contains(string(topgradeLog), "FAIL: Topgrade completed") {
		t.Fatalf("expected FAIL entry in topgrade log, got:\n%s", topgradeLog)
	}
	if data, err := os.ReadFile(logPath(logCategoryCleanup)); err == nil && strings.Contains(string(data), "Topgrade") {
		t.Fatalf("topgrade failure leaked into the cleanup log:\n%s", data)
	}
}

// TestAlwaysLogStepLogsOnSuccess: a step marked alwaysLog (destructive —
// removes or overwrites something) logs its outcome even when it succeeds in
// a real run, unlike an ordinary step (which stays failure-only). This is
// what closes the gap that made the orphan-removal incident only traceable
// via pacman's own log, not archmaid's.
func TestAlwaysLogStepLogsOnSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{
		step: steps.Step{
			Title: "remove orphans", LogLabel: "Orphaned packages removed",
			Category: logCategoryOrphans, AlwaysLog: true,
		},
		status: steps.Running,
	}}
	m.current = 0
	m.buf.WriteString("removing liba libb\n")

	next, _ := m.handleStream(streamBatchMsg{done: true})
	got := next.(model)
	if got.queue[0].status != steps.OK {
		t.Fatalf("status = %d, want steps.OK", got.queue[0].status)
	}

	data, err := os.ReadFile(logPath(logCategoryOrphans))
	if err != nil {
		t.Fatalf("alwaysLog step should log on success, got no file: %v", err)
	}
	if !strings.Contains(string(data), "OK: Orphaned packages removed") ||
		!strings.Contains(string(data), "removing liba libb") {
		t.Fatalf("expected OK entry with captured output, got:\n%s", data)
	}
}

// TestOrdinaryStepStaysSilentOnSuccess: without alwaysLog, a successful real
// (non-dry-run) step writes nothing — matching the pre-existing
// failures-only policy for anything not flagged destructive.
func TestOrdinaryStepStaysSilentOnSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	m := initialModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: steps.Step{Title: "cache", LogLabel: "Cache cleaned"}, status: steps.Running}}
	m.current = 0
	m.buf.WriteString("ok\n")

	m.handleStream(streamBatchMsg{done: true})

	if _, err := os.Stat(logPath(logCategoryCleanup)); !os.IsNotExist(err) {
		data, _ := os.ReadFile(logPath(logCategoryCleanup))
		t.Fatalf("ordinary step should not log on success, got:\n%s", data)
	}
}

// TestViewLogOpensPicker: "View log" from the menu goes to the category
// picker, not straight to a pager, and remembers the menu as its way back.
func TestViewLogOpensPicker(t *testing.T) {
	m := initialModel()
	m.cursor = len(actions.Menu) - 2 // "View log"
	if actions.Menu[m.cursor].ID != actions.IDViewLog {
		t.Fatalf("test menu index drifted: menu[%d].ID = %q", m.cursor, actions.Menu[m.cursor].ID)
	}
	got := update(m, key("enter"))
	if got.screen != screenLogPicker {
		t.Fatalf("screen = %d, want screenLogPicker", got.screen)
	}
	if got.logReturn != screenMenu {
		t.Fatalf("logReturn = %d, want screenMenu", got.logReturn)
	}
}

// TestLogPickerEscReturnsWhereItCameFrom: esc from the picker goes back to
// logReturn, which differs depending on whether the picker was opened from
// the menu or from the summary screen's "l" shortcut.
func TestLogPickerEscReturnsWhereItCameFrom(t *testing.T) {
	m := initialModel()
	m.screen = screenSummary
	got := update(m, key("l"))
	if got.screen != screenLogPicker || got.logReturn != screenSummary {
		t.Fatalf("after 'l' on summary: screen=%d logReturn=%d, want screenLogPicker/screenSummary",
			got.screen, got.logReturn)
	}
	back := update(got, key("esc"))
	if back.screen != screenSummary {
		t.Fatalf("esc from picker = screen %d, want screenSummary", back.screen)
	}
}

// TestLogPickerEnterOpensChosenCategory: selecting a category in the picker
// opens the pager on that category's log, tracked via m.logCat.
func TestLogPickerEnterOpensChosenCategory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()

	m := initialModel()
	m.screen = screenLogPicker
	m.logCursor = 1 // orphans, per logCategoryLabels' order
	if logCategoryLabels[1].ID != logCategoryOrphans {
		t.Fatalf("test index drifted: logCategoryLabels[1] = %+v", logCategoryLabels[1])
	}

	got := update(m, key("enter"))
	if got.screen != screenLog {
		t.Fatalf("screen = %d, want screenLog", got.screen)
	}
	if got.logCat != logCategoryOrphans {
		t.Fatalf("logCat = %q, want %q", got.logCat, logCategoryOrphans)
	}
}

// itoa is a tiny int→string helper so the test file needn't import strconv just
// for one substring check.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
