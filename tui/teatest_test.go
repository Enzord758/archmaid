package main

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
)

// The system status line (disk/ram/cache) is live, so pin it to a constant in
// every integration test — otherwise the golden frames would drift per machine.
const testStatus = " Free: 100G   RAM free: 8G   Cache: 512M"

// fixedModel returns an initial model with a deterministic status line so that
// rendered goldens are stable across machines and runs.
func fixedModel() model {
	m := initialModel()
	m.status = testStatus
	return m
}

// TestMain forces lipgloss to the Ascii (no-color) profile so rendered frames
// are plain text — deterministic and diff-friendly as golden files, regardless
// of the terminal the tests run in.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m.Run()
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
	waitForText(t, tm, "Arch Linux Maintenance")

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

// TestServicesGolden routes into the services multi-select via a discovery
// message, toggles a couple of entries, and golden-compares the checklist.
func TestServicesGolden(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	tm := teatest.NewTestModel(t, fixedModel(), teatest.WithInitialTermSize(80, 24))
	waitForText(t, tm, "Arch Linux Maintenance")

	tm.Send(discoveryMsg{kind: "services", services: []string{
		"bluetooth.service", "cups.service", "docker.service",
	}})
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
