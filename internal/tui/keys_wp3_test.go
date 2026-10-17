package tui

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// quits reports whether cmd is Bubble Tea's quit.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// press runs one key on m and returns the model and the command.
func press(m model, k string) (model, tea.Cmd) {
	next, cmd := m.Update(key(k))
	return next.(model), cmd
}

// TestMenuCursorKeys: up/k and down/j move the menu cursor one entry and stop
// at both ends; enter on an entry that is the TUI's own (here none selected
// beyond the cursor) never moves it.
func TestMenuCursorKeys(t *testing.T) {
	m := fixedModel()
	last := len(actions.Menu) - 1
	for _, up := range []string{"up", "k"} {
		if got, _ := press(m, up); got.cursor != 0 {
			t.Errorf("%q at the top: cursor %d, want 0", up, got.cursor)
		}
	}
	for _, down := range []string{"down", "j"} {
		got, _ := press(m, down)
		if got.cursor != 1 {
			t.Errorf("%q: cursor %d, want 1", down, got.cursor)
		}
		for _, up := range []string{"up", "k"} {
			if back, _ := press(got, up); back.cursor != 0 {
				t.Errorf("%q after %q: cursor %d, want 0", up, down, back.cursor)
			}
		}
	}
	m.cursor = last
	if got, _ := press(m, "j"); got.cursor != last {
		t.Errorf("down at the bottom: cursor %d, want %d", got.cursor, last)
	}
	if _, cmd := press(m, "q"); !quits(cmd) {
		t.Error("q on the menu did not quit")
	}
}

// TestMenuExitAndEditConfig: Enter on "Exit" quits; on "Edit topgrade config"
// hands the editor the config (through the execProcess seam, nothing is
// started).
func TestMenuExitAndEditConfig(t *testing.T) {
	at := func(id string) int {
		for i, a := range actions.Menu {
			if a.ID == id {
				return i
			}
		}
		t.Fatalf("no menu entry %q", id)
		return 0
	}
	m := fixedModel()
	m.cursor = at(actions.IDExit)
	if _, cmd := press(m, "enter"); !quits(cmd) {
		t.Error("Enter on Exit did not quit")
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor")
	var got *exec.Cmd
	orig := execProcess
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		got = c
		return func() tea.Msg { return fn(nil) }
	}
	defer func() { execProcess = orig }()
	m.cursor = at(actions.IDEditConfig)
	next, cmd := press(m, "enter")
	if cmd == nil || got == nil || got.Args[0] != "myeditor" || got.Args[1] != configPath() {
		t.Fatalf("editor command = %v (cmd %v), want myeditor on %s", got, cmd, configPath())
	}
	if _, ok := cmd().(editorDoneMsg); !ok {
		t.Errorf("the editor's return is %T, want editorDoneMsg", cmd())
	}
	if len(m.eng.drv.(*fakeDriver).sent) != 0 || next.screen != screenMenu {
		t.Error("Edit config must not start an engine action")
	}
}

// TestConfirmKeys: left/right/h/l/tab flip the selected button; Enter sends
// the answer that button stands for, and "y"/"n" force it.
func TestConfirmKeys(t *testing.T) {
	for _, k := range []string{"left", "right", "h", "l", "tab"} {
		m, _ := engineModel()
		m.screen, m.confirmYes = screenConfirm, false
		got, _ := press(m, k)
		if !got.confirmYes {
			t.Errorf("%q did not select Yes", k)
		}
		if back, _ := press(got, k); back.confirmYes {
			t.Errorf("%q twice did not select No again", k)
		}
	}
	for _, c := range []struct {
		start bool
		k     string
		want  bool
	}{{true, "enter", true}, {false, "enter", false}, {false, "y", true}, {true, "n", false}} {
		m, f := engineModel()
		m.screen, m.confirmYes = screenConfirm, c.start
		m.eng.prompt = 9
		got, _ := press(m, c.k)
		if len(f.sent) != 1 || f.sent[0] != (engine.Confirm{Prompt: 9, Yes: c.want}) {
			t.Errorf("start %v, %q: sent %+v, want Confirm{9, %v}", c.start, c.k, f.sent, c.want)
		}
		if got.screen != screenRunning {
			t.Errorf("start %v, %q: screen %d, want back on the running screen", c.start, c.k, got.screen)
		}
	}
}

// TestSummaryKeys: Enter and Esc on the summary go back to the menu (q quits, see
// TestSummaryQuitsOnQ); any
// other key leaves it up.
func TestSummaryKeys(t *testing.T) {
	for _, k := range []string{"enter", "esc"} {
		m := fixedModel()
		m.screen = screenSummary
		if got, _ := press(m, k); got.screen != screenMenu {
			t.Errorf("%q: screen %d, want the menu", k, got.screen)
		}
	}
	m := fixedModel()
	m.screen = screenSummary
	if got, _ := press(m, "x"); got.screen != screenSummary {
		t.Errorf("x left the summary: screen %d", got.screen)
	}
}

// TestLogPickerCursorKeys: up/k and down/j move the category cursor and stop
// at both ends; Esc, q and ctrl+c return to where the picker was opened from.
func TestLogPickerCursorKeys(t *testing.T) {
	last := len(logCategoryLabels) - 1
	m := fixedModel()
	m.screen, m.logReturn = screenLogPicker, screenSummary
	for _, up := range []string{"up", "k"} {
		if got, _ := press(m, up); got.logCursor != 0 {
			t.Errorf("%q at the top: %d", up, got.logCursor)
		}
	}
	for _, down := range []string{"down", "j"} {
		got, _ := press(m, down)
		if got.logCursor != 1 {
			t.Errorf("%q: %d, want 1", down, got.logCursor)
		}
		for _, up := range []string{"up", "k"} {
			if back, _ := press(got, up); back.logCursor != 0 {
				t.Errorf("%q: %d, want 0", up, back.logCursor)
			}
		}
	}
	m.logCursor = last
	if got, _ := press(m, "down"); got.logCursor != last {
		t.Errorf("down at the bottom: %d, want %d", got.logCursor, last)
	}
	for _, k := range []string{"esc", "q", "ctrl+c"} {
		if got, _ := press(m, k); got.screen != screenSummary {
			t.Errorf("%q: screen %d, want where the picker came from", k, got.screen)
		}
	}
}

// TestLogViewerKeys: Esc and q leave the pager for the picker; a scrolling key
// moves the text instead and stays in the pager.
func TestLogViewerKeys(t *testing.T) {
	m := sizedModel()
	m.screen = screenLog
	m.output.SetContent(strings.Repeat("a log line\n", 200))
	for _, k := range []string{"esc", "q"} {
		if got, _ := press(m, k); got.screen != screenLogPicker {
			t.Errorf("%q: screen %d, want the picker", k, got.screen)
		}
	}
	got, _ := press(m, "pgdown")
	if got.screen != screenLog || got.output.YOffset <= m.output.YOffset {
		t.Errorf("pgdown: screen %d, offset %d -> %d; want the pager scrolled", got.screen, m.output.YOffset, got.output.YOffset)
	}
}

// TestAuditReportScrolls: a scrolling key on the audit report moves the text;
// the keys the engine takes (Esc, q, a) are not scrolling.
func TestAuditReportScrolls(t *testing.T) {
	m, f := engineModel()
	m.screen = screenAudit
	m.output.SetContent(strings.Repeat("a finding\n", 200))
	got, _ := press(m, "pgdown")
	if got.screen != screenAudit || got.output.YOffset <= m.output.YOffset || len(f.sent) != 0 {
		t.Errorf("pgdown: screen %d, offset %d -> %d, sent %+v", got.screen, m.output.YOffset, got.output.YOffset, f.sent)
	}
}

// TestNoticeAndDiscoveringKeys: on a notice any key returns to the menu and
// ctrl+c quits; while a discovery runs only ctrl+c does anything.
func TestNoticeAndDiscoveringKeys(t *testing.T) {
	m := fixedModel()
	m.screen = screenNotice
	if got, cmd := press(m, "x"); got.screen != screenMenu || quits(cmd) {
		t.Errorf("a key on a notice: screen %d, quit %v; want the menu", got.screen, quits(cmd))
	}
	if _, cmd := press(m, "ctrl+c"); !quits(cmd) {
		t.Error("ctrl+c on a notice did not quit")
	}
	m.screen = screenDiscovering
	if got, cmd := press(m, "x"); got.screen != screenDiscovering || quits(cmd) {
		t.Errorf("a key while discovering: screen %d, quit %v; want to keep waiting", got.screen, quits(cmd))
	}
	if _, cmd := press(m, "ctrl+c"); !quits(cmd) {
		t.Error("ctrl+c while discovering did not quit")
	}
}

// TestServicesCursorKeys: up/k and down/j move the service cursor and stop at
// both ends.
func TestServicesCursorKeys(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.NeedsSelection{ID: 1, Items: []string{"a.service", "b.service", "c.service"}})
	for _, up := range []string{"up", "k"} {
		if got, _ := press(m, up); got.serviceCursor != 0 {
			t.Errorf("%q at the top: %d", up, got.serviceCursor)
		}
	}
	for _, down := range []string{"down", "j"} {
		got, _ := press(m, down)
		if got.serviceCursor != 1 {
			t.Errorf("%q: %d, want 1", down, got.serviceCursor)
		}
		if back, _ := press(got, "k"); back.serviceCursor != 0 {
			t.Errorf("k: %d, want 0", back.serviceCursor)
		}
	}
	m.serviceCursor = 2
	if got, _ := press(m, "j"); got.serviceCursor != 2 {
		t.Errorf("down at the bottom: %d, want 2", got.serviceCursor)
	}
}

// TestUnhandledMessages: a message no case knows (a cursor blink, say) is
// passed to the password field while one is showing and ignored elsewhere.
func TestUnhandledMessages(t *testing.T) {
	type unknown struct{}
	m := fixedModel()
	got, cmd := m.Update(unknown{})
	if cmd != nil || got.(model).screen != screenMenu {
		t.Errorf("on the menu: cmd %v, screen %d; want nothing", cmd, got.(model).screen)
	}
	m.screen = screenPassword
	m.password.Focus()
	got, _ = m.Update(unknown{})
	if got.(model).screen != screenPassword {
		t.Errorf("on the password screen: screen %d", got.(model).screen)
	}
}
