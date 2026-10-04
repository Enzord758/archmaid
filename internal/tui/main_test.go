package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
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

// ── behaviour 1: dry-run safety across the whole full-cleanup queue ───────────

// TestDryRunFullCleanupExecutesNothing runs every step in the Full Cleanup
// queue under --dryrun, as the Driver starts it (runStep), and asserts none
// of them actually executes: each emits a "would run" line and nothing else.
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

// ── behaviour 6: services multi-select ────────────────────────────────────────

// TestDiscoverServicesRuns exercises the real services pipeline end to end: it
// must not panic and must return a well-formed discovery (either a services
// list or a "nothing to do" notice).
func TestDiscoverServicesRuns(t *testing.T) {
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		t.Skip("systemctl not available")
	}
	d := discoverNow(t, actions.IDServices)
	sel, _ := d.(actions.NeedsSelection)
	if len(sel.Items) == 0 && noticeOf(d) == "" {
		t.Fatal("expected either services or a notice")
	}
	// Any returned service line must be clean (trimmed, non-empty).
	for _, s := range sel.Items {
		if s != strings.TrimSpace(s) || s == "" {
			t.Fatalf("unclean service line %q", s)
		}
	}
}

// TestServiceToggle: space toggles the selection under the cursor.
func TestServiceToggle(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.NeedsSelection{ID: 1, Items: []string{"a.service", "b.service"}})
	m = update(m, key("down"))

	got := update(m, key(" "))
	if !got.serviceOn[1] {
		t.Fatal("space should toggle serviceOn[cursor] on")
	}
	got = update(got, key(" "))
	if got.serviceOn[1] {
		t.Fatal("space should toggle serviceOn[cursor] back off")
	}
}

// TestServicesDisableStep: the services picked become one
// `systemctl disable --now …` sudo step (actions.DisableUnits, which the
// engine builds the queue with on Select).
func TestServicesDisableStep(t *testing.T) {
	q := actions.DisableUnits([]string{"a.service", "c.service"})
	if len(q) != 1 {
		t.Fatalf("expected one disable step, got %d", len(q))
	}
	s := q[0]
	if s.Command() != "sudo" || !s.NeedsPriv {
		t.Fatalf("disable step should be a sudo step, got %+v", s)
	}
	joined := strings.Join(s.Args(), " ")
	if !strings.HasPrefix(joined, "systemctl disable --now ") {
		t.Fatalf("args = %v, want systemctl disable --now …", s.Args())
	}
	if !strings.Contains(joined, "a.service") || !strings.Contains(joined, "c.service") {
		t.Fatalf("selected services wrong, args = %v", s.Args())
	}
}

// ── behaviour 7: parsers ──────────────────────────────────────────────────────

// nonEmptyLines is s's lines, trimmed, without the blank ones: how the
// tests read a shim's exec log or a command's list. (The foreign-package
// parser it once was is frontend.ForeignPackages, tested there.)
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ── behaviour 8: log categories + view-log picker ──────────────────────────────

// TestStepCategoryDefaultsToCleanup: a step with no category set (the common
// case) buckets into "cleanup", not an empty/zero category.
func TestStepCategoryDefaultsToCleanup(t *testing.T) {
	if got := (steps.Step{}).LogCategory(); got != logCategoryCleanup {
		t.Fatalf("zero Step.LogCategory() = %q, want %q", got, logCategoryCleanup)
	}
}

// TestViewLogOpensPicker: "View log" from the menu goes to the category
// picker, not straight to a pager, and remembers the menu as its way back.
func TestViewLogOpensPicker(t *testing.T) {
	m := fixedModel()
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
	m := fixedModel()
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

	m := fixedModel()
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
