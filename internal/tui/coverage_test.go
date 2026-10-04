package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// These tests close spots that were once untested (see TESTING.md → Coverage map).

// ── editConfig non-dry-run path (tea.ExecProcess seam) ──────────────────────────

// TestEditConfigCreatesFileAndOpensEditor: the non-dry path creates the config
// with defaults and hands the *right* command to the editor — verified via the
// execProcess seam, without launching a real editor.
func TestEditConfigCreatesFileAndOpensEditor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor")

	var gotCmd *exec.Cmd
	orig := execProcess
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		gotCmd = c
		return func() tea.Msg { return fn(nil) }
	}
	defer func() { execProcess = orig }()

	msg := editConfig()() // non-dry: creates file, "opens" editor via the fake
	if _, ok := msg.(editorDoneMsg); !ok {
		t.Fatalf("expected editorDoneMsg, got %T", msg)
	}

	data, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if !strings.Contains(string(data), "aur_command") {
		t.Fatalf("default config missing expected content:\n%s", data)
	}
	if gotCmd == nil || len(gotCmd.Args) != 2 ||
		gotCmd.Args[0] != "myeditor" || gotCmd.Args[1] != configPath() {
		t.Fatalf("editor command = %v, want [myeditor %s]", gotCmd.Args, configPath())
	}
}

// TestEditConfigKeepsExistingFile: an existing config is not overwritten.
func TestEditConfigKeepsExistingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := configPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("# my custom config\n"), 0o644)

	orig := execProcess
	execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return fn(nil) }
	}
	defer func() { execProcess = orig }()

	editConfig()()

	data, _ := os.ReadFile(path)
	if string(data) != "# my custom config\n" {
		t.Fatalf("existing config was modified:\n%s", data)
	}
}

// ── password masking (rendered, not just configured) ───────────────────────────

// TestPasswordInputIsMasked: typed characters render as the mask glyph, never as
// cleartext.
func TestPasswordInputIsMasked(t *testing.T) {
	m := fixedModel()
	m.screen = screenPassword
	m.password.Focus()
	for _, r := range "secret" {
		m = update(m, key(string(r)))
	}

	view := m.viewPassword()
	if strings.Contains(view, "secret") {
		t.Fatal("password rendered in cleartext")
	}
	if !strings.Contains(view, strings.Repeat("•", 6)) {
		t.Fatalf("expected 6 mask glyphs, got:\n%s", view)
	}
}

// ── running-screen checklist (spinner pinned for determinism) ───────────────────

// TestChecklistRendersAllStatuses: every step status renders its expected glyph
// and label. The spinner is pinned to a single frame so the running row is
// deterministic.
func TestChecklistRendersAllStatuses(t *testing.T) {
	m := fixedModel()
	m.spinner.Spinner = spinner.Spinner{Frames: []string{"*"}, FPS: time.Second}
	m.queue = []stepState{
		{step: steps.Step{Title: "run", LogLabel: "run"}, status: steps.Running},
		{step: steps.Step{Title: "ok", LogLabel: "did ok"}, status: steps.OK},
		{step: steps.Step{Title: "boom", LogLabel: "boom"}, status: steps.Failed},
		{step: steps.Step{Title: "skip", LogLabel: "skip"}, status: steps.Skipped},
		{step: steps.Step{Title: "pend", LogLabel: "pend"}, status: steps.Pending},
	}

	out := m.checklist()
	for _, want := range []string{
		"* run",
		"✔ did ok",
		"✖ boom failed",
		"– skip (skipped)",
		"○ pend",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("checklist missing %q, got:\n%s", want, out)
		}
	}
}

// ── system status / refresh wiring ──────────────────────────────────────────────

// TestSystemStatusFormat: the status line always carries its three labels,
// regardless of the machine's actual df/free/du output.
func TestSystemStatusFormat(t *testing.T) {
	s := systemStatus()
	for _, want := range []string{"Free:", "RAM avail:", "Cache:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("status %q missing %q", s, want)
		}
	}
}

// TestStatusMsgUpdatesHeader: a statusMsg refreshes m.status (and schedules the
// next tick).
func TestStatusMsgUpdatesHeader(t *testing.T) {
	m := fixedModel()
	next, cmd := m.Update(statusMsg("pinned value"))
	got := next.(model)
	if got.status != "pinned value" {
		t.Fatalf("status = %q, want pinned value", got.status)
	}
	if cmd == nil {
		t.Fatal("expected a follow-up tick cmd")
	}
}

// ── summary messages for every outcome ──────────────────────────────────────────

// TestSummaryMessages: the summary line distinguishes success, failures, skips,
// their combination, and an aborted queue (finding 1 & 2's user-facing result).
func TestSummaryMessages(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*model)
		want  string
	}{
		{"success", func(m *model) {}, "completed successfully"},
		{"issues", func(m *model) { m.errs = 2 }, "2 issue(s)"},
		{"skipped", func(m *model) { m.skipped = 3 }, "3 step(s) skipped"},
		{"issues+skipped", func(m *model) { m.errs = 1; m.skipped = 2 }, "1 issue(s), 2 skipped"},
		{"aborted", func(m *model) { m.aborted = true }, "Aborted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := fixedModel()
			c.setup(&m)
			out := m.viewSummary()
			if !strings.Contains(out, c.want) {
				t.Fatalf("summary missing %q, got:\n%s", c.want, out)
			}
		})
	}
}
