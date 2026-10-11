package tui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestPromptClosedForgetsTheOpenPrompt: the engine closing the open prompt on
// its own (an abort elsewhere, a timeout) clears the TUI's record of it, so a
// later key can't answer a prompt that no longer exists.
func TestPromptClosedForgetsTheOpenPrompt(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.NeedsPassword{ID: 5, Kind: engine.PasswordUpFront})
	if m.eng.prompt != 5 || m.screen != screenPassword {
		t.Fatalf("prompt %d, screen %d; want the password prompt open", m.eng.prompt, m.screen)
	}
	m = feed(m, engine.PromptClosed{ID: 5})
	if m.eng.prompt != 0 {
		t.Errorf("prompt %d after PromptClosed, want 0", m.eng.prompt)
	}
	m = feed(m, engine.PromptClosed{ID: 77}) // some other prompt: nothing to forget
}

// TestFinishedClosesAnOpenAskpassPrompt: an action that ends with a sudo
// password popup still up takes the popup down with it and shows the summary.
func TestFinishedClosesAnOpenAskpassPrompt(t *testing.T) {
	m, _ := engineModel()
	m = runningQueue(m)
	m = feed(m, engine.NeedsPassword{ID: 6, Kind: engine.PasswordAskpass, Step: "a", Requester: "/usr/bin/sudo (pid 1)", Prompt: "[sudo] password:"})
	if !m.eng.askpassOpen() || m.screen != screenPassword {
		t.Fatalf("askpass open %v, screen %d; want the popup", m.eng.askpassOpen(), m.screen)
	}
	m = feed(m, engine.Finished{Outcome: engine.OutcomeCompleted})
	if m.eng.askpassOpen() || m.screen != screenSummary || m.askpassFrom != (askpassRequester{}) {
		t.Errorf("askpass open %v, screen %d, requester %+v; want the summary and no popup", m.eng.askpassOpen(), m.screen, m.askpassFrom)
	}
}

// TestFinishedReportDismissed: a report the user dismissed ends in the menu.
func TestFinishedReportDismissed(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.OutputReport{ID: 3, Text: "findings"})
	if m.screen != screenAudit {
		t.Fatalf("screen %d, want the report", m.screen)
	}
	m = feed(m, engine.Finished{Outcome: engine.OutcomeReport})
	if m.screen != screenMenu {
		t.Errorf("screen %d after the report ended, want the menu", m.screen)
	}
}

// TestOverlayTallerThanBackgroundAndShortLines: a popup taller than the
// screen under it extends the frame; a background line shorter than the
// popup's column is padded so the popup stays aligned.
func TestOverlayTallerThanBackgroundAndShortLines(t *testing.T) {
	got := overlay("abcdefgh", "12\n34\n56")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3 (the popup's height):\n%s", len(lines), got)
	}
	if lines[0] != "abc12fgh" {
		t.Errorf("first line %q, want the popup over the middle of the background", lines[0])
	}
	if !strings.HasSuffix(lines[1], "34") || !strings.HasSuffix(lines[2], "56") || len(lines[1]) != len(lines[2]) {
		t.Errorf("popup rows %q / %q must line up under the first", lines[1], lines[2])
	}
	// A narrow popup row is padded to the popup's width, so the background to its
	// right stays in its own column.
	if l := strings.Split(overlay("aaaaaaaa\nbbbbbbbb", "WIDE\nx"), "\n"); l[0] != "aaWIDEaa" || l[1] != "bbx   bb" {
		t.Errorf("uneven popup rows: %q", l)
	}
	short := overlay("ab\n"+strings.Repeat("x", 10), "WIDE")
	if l := strings.Split(short, "\n")[0]; !strings.Contains(l, "WIDE") || !strings.HasPrefix(l, "ab") {
		t.Errorf("short background line %q: want it padded to the popup's column", l)
	}
}

// TestRefreshStatusReadsOnTick: the periodic refresh reads the status when its
// timer fires (the timer is the seam) and hands the line to the model as a
// statusMsg; the model then asks for the next tick.
func TestRefreshStatusReadsOnTick(t *testing.T) {
	orig := tickFn
	var gotDelay time.Duration
	tickFn = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		gotDelay = d
		return func() tea.Msg { return fn(time.Now()) }
	}
	defer func() { tickFn = orig }()
	msg := refreshStatus()()
	s, ok := msg.(statusMsg)
	if !ok || !strings.Contains(string(s), "Free:") {
		t.Fatalf("tick delivered %#v, want a statusMsg with the status line", msg)
	}
	if gotDelay <= 0 {
		t.Errorf("tick delay %v", gotDelay)
	}
	m := fixedModel()
	next, cmd := m.Update(s)
	if next.(model).status != string(s) || cmd == nil {
		t.Errorf("status %q, next tick %v; want the line stored and a new tick", next.(model).status, cmd)
	}
}

// TestPlural: the suffix for a count.
func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "s", 1: "", 2: "s"} {
		if got := plural(n); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestYesNoButtonsMarkTheChoice: the selected button carries the marker.
func TestYesNoButtonsMarkTheChoice(t *testing.T) {
	if y := yesNoButtons(true); !strings.Contains(y, "▸ Yes") || strings.Contains(y, "▸ No") {
		t.Errorf("Yes selected: %q", y)
	}
	if n := yesNoButtons(false); !strings.Contains(n, "▸ No") || strings.Contains(n, "▸ Yes") {
		t.Errorf("No selected: %q", n)
	}
}

// TestExecProcessDefaultHandsTheTerminalOver: the default execProcess builds
// a Bubble Tea command around the editor without starting it, and the
// terminal-handing command gives the program the TUI's streams unless it names
// its own.
func TestExecProcessDefaultHandsTheTerminalOver(t *testing.T) {
	if cmd := execProcess(exec.Command("true"), func(error) tea.Msg { return nil }); cmd == nil {
		t.Fatal("execProcess returned no command")
	}
	var in, out, errw bytes.Buffer
	c := handedTerminal{exec.Command("true")}
	c.SetStdin(&in)
	c.SetStdout(&out)
	c.SetStderr(&errw)
	if c.Stdin != &in || c.Stdout != &out || c.Stderr != &errw {
		t.Error("the program was not given the terminal's streams")
	}
	own := handedTerminal{exec.Command("true")}
	var mine bytes.Buffer
	own.Stdin, own.Stdout, own.Stderr = &mine, &mine, &mine
	own.SetStdin(&in)
	own.SetStdout(&out)
	own.SetStderr(&errw)
	if own.Stdin != &mine || own.Stdout != &mine || own.Stderr != &mine {
		t.Error("a program's own streams were replaced")
	}
}

// TestEditConfigDryRunOnlyLogsTheIntent: in a dry run "Edit config" opens no
// editor and creates no config; it records what it would have opened.
func TestEditConfigDryRunOnlyLogsTheIntent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor")
	oldLogDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldLogDir }()
	dryRun = true
	defer func() { dryRun = false }()
	orig := execProcess
	execProcess = func(*exec.Cmd, tea.ExecCallback) tea.Cmd {
		t.Error("a dry run started the editor")
		return nil
	}
	defer func() { execProcess = orig }()

	if _, ok := editConfig()().(editorDoneMsg); !ok {
		t.Fatal("a dry run's edit did not finish with editorDoneMsg")
	}
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Errorf("a dry run created the config: %v", err)
	}
	var log []byte
	filepath.WalkDir(logDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			log = append(log, b...)
		}
		return nil
	})
	if !strings.Contains(string(log), "would open myeditor on "+configPath()) {
		t.Errorf("the dry run logged %q", log)
	}
}

// TestEditorName: $VISUAL wins over $EDITOR, which wins over nano.
func TestEditorName(t *testing.T) {
	for _, c := range []struct{ visual, editor, want string }{
		{"vis", "ed", "vis"}, {"", "ed", "ed"}, {"", "", "nano"},
	} {
		t.Setenv("VISUAL", c.visual)
		t.Setenv("EDITOR", c.editor)
		if got := editorName(); got != c.want {
			t.Errorf("VISUAL=%q EDITOR=%q: %q, want %q", c.visual, c.editor, got, c.want)
		}
	}
}

// shimSudo puts a sudo first on PATH that records its arguments and exits
// with status.
func shimSudo(t *testing.T, status string) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nexit " + status + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// TestDropSudoTicket: at exit the sudo ticket is dropped with `sudo -n -k`; a
// failure is said on w but never stops the exit; a dry run, which primed no
// ticket, runs no sudo at all.
func TestDropSudoTicket(t *testing.T) {
	calls := shimSudo(t, "0")
	var w bytes.Buffer
	DropSudoTicket(&w)
	if b, _ := os.ReadFile(calls); strings.TrimSpace(string(b)) != "-n -k" || w.Len() != 0 {
		t.Errorf("sudo calls %q, output %q; want one `-n -k` and nothing said", b, w.String())
	}

	failing := shimSudo(t, "1")
	w.Reset()
	DropSudoTicket(&w)
	if b, _ := os.ReadFile(failing); strings.TrimSpace(string(b)) != "-n -k" || !strings.Contains(w.String(), "couldn't drop the sudo ticket") {
		t.Errorf("sudo calls %q, output %q; want the failure said", b, w.String())
	}

	dryCalls := shimSudo(t, "0")
	dryRun = true
	defer func() { dryRun = false }()
	w.Reset()
	DropSudoTicket(&w)
	if _, err := os.Stat(dryCalls); !os.IsNotExist(err) || w.Len() != 0 {
		t.Errorf("a dry run ran sudo (%v) or said %q", err, w.String())
	}
}

// TestWaitForDownloadsSaysWhyItWaits: a deletion that is still in flight after
// a moment is named on w, and the wait ends when it does; with nothing in
// flight there is no output.
func TestWaitForDownloadsSaysWhyItWaits(t *testing.T) {
	var quiet bytes.Buffer
	waitForDownloads(&quiet)
	if quiet.Len() != 0 {
		t.Errorf("nothing in flight, yet it said %q", quiet.String())
	}

	release, started, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	step := downloadsRunning.Step(downloads.Plan{}, nil, func() downloads.Options {
		close(started)
		<-release
		return downloads.Options{DryRun: true}
	})
	go func() { step(context.Background(), steps.Env{}); close(finished) }()
	<-started
	var w bytes.Buffer
	waited := make(chan struct{})
	go func() { waitForDownloads(&w); close(waited) }()
	select {
	case <-waited:
		t.Fatal("waitForDownloads returned with a deletion in flight")
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForDownloads did not return once the deletion ended")
	}
	<-finished
	if !strings.Contains(w.String(), "waiting for the downloads deletion to stop") {
		t.Errorf("said %q, want why it waited", w.String())
	}
}
