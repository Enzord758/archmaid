package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
func drainStep(ch <-chan streamMsg) ([]string, error) {
	var lines []string
	for msg := range func() chan streamMsg {
		out := make(chan streamMsg)
		go func() {
			for {
				m := <-ch
				out <- m
				if m.done {
					close(out)
					return
				}
			}
		}()
		return out
	}() {
		if msg.done {
			return lines, msg.err
		}
		lines = append(lines, msg.line)
	}
	return lines, nil
}

// TestDryRunDoesNotExecute is the safety-critical test: in dry run a step must
// be reported, not run.
func TestDryRunDoesNotExecute(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	marker := filepath.Join(t.TempDir(), "marker")
	ch := make(chan streamMsg, 8)
	runStep(step{command: "touch", args: []string{marker}}, ch)

	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("dry-run step should not error, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("dry-run executed the command: %s was created", marker)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "would run") {
		t.Fatalf("expected a 'would run' line, got %v", lines)
	}
}

// TestDryRunLogsEveryStep verifies a successful dry-run step is logged to the
// dry-run log (normal runs only log failures).
func TestDryRunLogsEveryStep(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()
	t.Setenv("HOME", t.TempDir())

	m := initialModel()
	m.queue = []stepState{{step: step{logLabel: "Test step"}, status: statusRunning}}
	m.current = 0
	m.buf.WriteString("[dry-run] would run: echo hi\n")

	tm, _ := m.handleStream(streamMsg{done: true})
	_ = tm

	data, err := os.ReadFile(logPath())
	if err != nil {
		t.Fatalf("dry-run log not written: %v", err)
	}
	if !strings.Contains(string(data), "DRYRUN: Test step") {
		t.Fatalf("expected DRYRUN entry, got:\n%s", data)
	}
}

// TestSudoThreeStrikes: after maxSudoAttempts wrong passwords, sudo is marked
// unavailable and the queue proceeds (skipping sudo steps).
func TestSudoThreeStrikes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := initialModel()
	m.pending = []step{{logLabel: "needs root", needsSudo: true, command: "true"}}

	var tm interface{} = m
	for i := 0; i < maxSudoAttempts; i++ {
		var mm model = tm.(model)
		next, _ := mm.Update(sudoResultMsg{err: errors.New("bad password")})
		tm = next
	}
	got := tm.(model)
	if !got.sudoUnavailable {
		t.Fatalf("expected sudoUnavailable after %d failures", maxSudoAttempts)
	}
}

// TestDiscoveryNotice: a discovery with a notice routes to the notice screen.
func TestDiscoveryNotice(t *testing.T) {
	m := initialModel()
	tm, _ := m.Update(discoveryMsg{kind: "downloads", notice: "nothing to do"})
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
	tm, _ := m.Update(discoveryMsg{kind: "services", services: svcs})
	got := tm.(model)
	if got.screen != screenServices {
		t.Fatalf("expected services screen, got %d", got.screen)
	}
	if len(got.serviceOn) != len(svcs) {
		t.Fatalf("serviceOn size %d, want %d", len(got.serviceOn), len(svcs))
	}
}

// TestBrokenPackagesParsing sanity-checks the parser against a healthy system:
// pacman -Qk should report no broken packages (all "0 missing/altered files").
func TestBrokenPackagesParsing(t *testing.T) {
	if _, err := os.Stat("/usr/bin/pacman"); err != nil {
		t.Skip("pacman not available")
	}
	// We can't assert a specific list, but the call must not panic and must
	// return cleanly. On a healthy system it's empty.
	_ = brokenPackages()
}

// ── behaviour 1: dry-run safety across the whole full-cleanup queue ───────────

// TestDryRunFullCleanupExecutesNothing runs every step in the Full Cleanup
// queue under --dryrun and asserts none of them actually executes: each emits a
// "would run" line and nothing else.
func TestDryRunFullCleanupExecutesNothing(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	for _, s := range fullCleanupSteps() {
		ch := make(chan streamMsg, 8)
		runStep(s, ch)
		lines, err := drainStep(ch)
		if err != nil {
			t.Fatalf("dry-run step %q errored: %v", s.title, err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "would run") {
			t.Fatalf("step %q: expected exactly one 'would run' line, got %v", s.title, lines)
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
	m.pending = []step{{title: "x", logLabel: "x", command: "true", needsSudo: true}}

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

	m := initialModel()
	m.pending = []step{{title: "root job", logLabel: "needs root", needsSudo: true, command: "true"}}

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
	if got.queue[0].status != statusSkipped {
		t.Fatalf("sudo step status = %d, want statusSkipped", got.queue[0].status)
	}
	if got.errs != 0 {
		t.Fatalf("errs = %d, want 0 (a skip is not a failure)", got.errs)
	}
	if got.skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the skipped sudo step)", got.skipped)
	}
	data, _ := os.ReadFile(logPath())
	if !strings.Contains(string(data), "SKIP: needs root (sudo authentication failed)") {
		t.Fatalf("missing SKIP log entry, got:\n%s", data)
	}
}

// ── behaviour 3: confirm flow ──────────────────────────────────────────────────

// TestConfirmStepShowsModal: a step carrying a confirm string pauses the queue
// on the confirm screen before running.
func TestConfirmStepShowsModal(t *testing.T) {
	m := initialModel()
	m.pending = []step{{title: "risky", logLabel: "risky", command: "true",
		confirm: "Are you sure?", optional: true}}

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
		{step: step{title: "opt", confirm: "?", optional: true}, status: statusPending},
		{step: step{title: "next", command: "true"}, status: statusPending},
	}
	m.current = 0
	m.screen = screenConfirm
	m.confirmYes = false

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.queue[0].status != statusSkipped {
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
		{step: step{title: "must", confirm: "?", optional: false}, status: statusPending},
		{step: step{title: "next"}, status: statusPending},
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
	if got.queue[0].status != statusSkipped || got.queue[1].status != statusSkipped {
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
	m.queue = []stepState{{step: step{title: "go", logLabel: "go", command: "true", confirm: "?"}, status: statusPending}}
	m.current = 0
	m.screen = screenConfirm
	m.confirmYes = true

	next, _ := m.resolveConfirm()
	got := next.(model)
	if got.screen != screenRunning {
		t.Fatalf("yes should launch (screenRunning), got screen=%d", got.screen)
	}
	if got.queue[0].status != statusRunning {
		t.Fatalf("launched step status=%d, want statusRunning", got.queue[0].status)
	}
}

// ── behaviour 4: streaming order + failure ────────────────────────────────────

// TestStreamOrderAndSingleDone: runStep emits output lines in order followed by
// exactly one done message, for a successful command.
func TestStreamOrderAndSingleDone(t *testing.T) {
	ch := make(chan streamMsg, 16)
	runStep(step{command: "bash", args: []string{"-c", "printf 'one\\ntwo\\nthree\\n'"}}, ch)

	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("command should succeed, got %v", err)
	}
	want := []string{"one", "two", "three"}
	if strings.Join(lines, ",") != strings.Join(want, ",") {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	// After the done message the channel must carry nothing more.
	select {
	case extra := <-ch:
		t.Fatalf("expected no message after done, got %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestStreamFailurePropagates: a failing command captures its output, reports a
// non-nil error, and handleStream marks it failed, bumps errs, and logs FAIL
// with the captured body.
func TestStreamFailurePropagates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ch := make(chan streamMsg, 16)
	runStep(step{command: "bash", args: []string{"-c", "echo boom; exit 3"}}, ch)
	lines, err := drainStep(ch)
	if err == nil {
		t.Fatal("failing command should report a non-nil error")
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Fatalf("captured output = %v, want [boom]", lines)
	}

	m := initialModel()
	m.queue = []stepState{{step: step{title: "boom", logLabel: "Boom step"}, status: statusRunning}}
	m.current = 0
	m.buf.WriteString(strings.Join(lines, "\n") + "\n")

	next, _ := m.handleStream(streamMsg{done: true, err: err})
	got := next.(model)
	if got.queue[0].status != statusFailed {
		t.Fatalf("status = %d, want statusFailed", got.queue[0].status)
	}
	if got.errs != 1 {
		t.Fatalf("errs = %d, want 1", got.errs)
	}
	data, _ := os.ReadFile(logPath())
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
	got := update(m, discoveryMsg{kind: "downloads", pending: []step{
		{title: "del", logLabel: "del", command: "true"},
	}})
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
	next, cmd := m.Update(discoveryMsg{kind: "repair", pending: []step{
		{title: "fix", logLabel: "fix", command: "sudo", needsSudo: true},
	}})
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
	msg, ok := discoverServices()().(discoveryMsg)
	if !ok {
		t.Fatal("discoverServices should return a discoveryMsg")
	}
	if msg.kind != "services" {
		t.Fatalf("kind = %q, want services", msg.kind)
	}
	if len(msg.services) == 0 && msg.notice == "" {
		t.Fatal("expected either services or a notice")
	}
	// Any returned service line must be clean (trimmed, non-empty).
	for _, s := range msg.services {
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

	m.confirmServices() // mutates m.pending in place
	if len(m.pending) != 1 {
		t.Fatalf("expected one disable step, got %d", len(m.pending))
	}
	s := m.pending[0]
	if s.command != "sudo" || !s.needsSudo {
		t.Fatalf("disable step should be a sudo step, got %+v", s)
	}
	joined := strings.Join(s.args, " ")
	if !strings.HasPrefix(joined, "systemctl disable --now ") {
		t.Fatalf("args = %v, want systemctl disable --now …", s.args)
	}
	if !strings.Contains(joined, "a.service") || !strings.Contains(joined, "c.service") ||
		strings.Contains(joined, "b.service") {
		t.Fatalf("selected services wrong, args = %v", s.args)
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

// TestBrokenPackagesNoMatchNoPanic: on a healthy system the grep inside
// brokenPackages exits 1 (no matches); the function must swallow that and return
// an empty list rather than panicking.
func TestBrokenPackagesNoMatchNoPanic(t *testing.T) {
	if _, err := os.Stat("/usr/bin/pacman"); err != nil {
		t.Skip("pacman not available")
	}
	// Must not panic; on a healthy machine the result is empty, but we only
	// assert the call is safe and returns a (possibly nil) clean slice.
	for _, p := range brokenPackages() {
		if p != strings.TrimSpace(p) || p == "" {
			t.Fatalf("unclean package line %q", p)
		}
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
