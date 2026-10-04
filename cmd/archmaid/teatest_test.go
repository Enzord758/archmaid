package main

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The system status line (disk/ram/cache) is live, so pin it to a constant in
// every integration test — otherwise the golden frames would drift per machine.
const testStatus = " Free: 100G   RAM avail: 8G   Cache: 512M"

// fixedModel returns an initial model with a deterministic status line so that
// rendered goldens are stable across machines and runs. It runs on a
// fakeDriver: a test feeds it the engine's Events.
func fixedModel() model {
	m := initialModel()
	m.status = testStatus
	m.eng = newEngineState(&fakeDriver{})
	return m
}

// TestMain forces lipgloss to the Ascii (no-color) profile so rendered frames
// are plain text — deterministic and diff-friendly as golden files, regardless
// of the terminal the tests run in.
func TestMain(m *testing.M) {
	// Under `go test` os.Executable() is this test binary, so the SUDO_ASKPASS
	// wrapper startAskpassServer writes execs it with --askpass. Dispatch that
	// exactly as main() does, so a wrapper reaching this binary relays instead of
	// running the whole suite again.
	if len(os.Args) >= 3 && os.Args[1] == "--askpass" {
		prompt := ""
		if len(os.Args) >= 4 {
			prompt = os.Args[3]
		}
		os.Exit(privilege.RunClient(os.Args[2], prompt))
	}

	lipgloss.SetColorProfile(termenv.Ascii)
	// No test may reach the real AUR: Full Cleanup's first step now calls it.
	// Anything that wants the RPC points aurRPCURL at an httptest server
	// (fakeAUR); everything else fails fast against this closed port.
	aurRPCURL = "http://127.0.0.1:1/rpc"
	// No test may write the user's real state dir either (appendLog, and now
	// last-run.json): default to a throwaway one; tests that inspect the logs
	// still point logDir at their own.
	dir, err := os.MkdirTemp("", "archmaid-test-state")
	if err != nil {
		panic(err)
	}
	logDir = dir
	// HOME and the XDG dirs are a temp dir for the whole run, and the real
	// state dir is checked afterwards (internal/testenv).
	code := testenv.Run(m)
	os.RemoveAll(dir)
	os.Exit(code)
}

// waitForText blocks until the program's output contains sub, failing fast if it
// never does. Used to synchronise before capturing a frame.
func waitForText(t *testing.T, tm *teatest.TestModel, sub string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(sub))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
}

// TestMenuGolden drives the whole program headless, moves the menu cursor, and
// golden-compares the rendered menu frame.
func TestMenuGolden(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	tm := teatest.NewTestModel(t, fixedModel(), teatest.WithInitialTermSize(80, 24))
	waitForText(t, tm, "archmaid")

	// move the selection down one row so the golden captures a non-default
	// cursor position too.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Quit()

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(model)
	if fm.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after one Down", fm.cursor)
	}
	teatest.RequireEqualOutput(t, []byte(fm.View()))
}

// TestServicesGolden routes into the services multi-select via the engine's
// NeedsSelection, toggles a couple of entries, and golden-compares the
// checklist.
func TestServicesGolden(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	tm := teatest.NewTestModel(t, fixedModel(), teatest.WithInitialTermSize(80, 24))
	waitForText(t, tm, "archmaid")

	tm.Send(engineEventsMsg{evs: []engine.Event{engine.NeedsSelection{ID: 1, Items: []string{
		"bluetooth.service", "cups.service", "docker.service",
	}}}})
	waitForText(t, tm, "select services to disable")

	// cursor starts on row 0; select it, move down twice, select the third.
	tm.Send(tea.KeyMsg{Type: tea.KeySpace}) // toggle bluetooth
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeySpace}) // toggle docker
	tm.Quit()

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(model)
	if fm.screen != screenServices {
		t.Fatalf("screen = %d, want screenServices", fm.screen)
	}
	if !fm.serviceOn[0] || fm.serviceOn[1] || !fm.serviceOn[2] {
		t.Fatalf("selection = %v, want [true false true]", fm.serviceOn)
	}
	teatest.RequireEqualOutput(t, []byte(fm.View()))
}
