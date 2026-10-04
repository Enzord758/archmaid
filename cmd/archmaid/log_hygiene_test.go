package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useLogDir points the log dir at dir for one test.
func useLogDir(t *testing.T, dir string) {
	t.Helper()
	old := logDir
	logDir = dir
	t.Cleanup(func() { logDir = old })
}

// TestViewLogCreatesNoRunDir: browsing the logs is read-only. the store creates a run
// directory only when it writes, so just opening View log leaves no empty
// <timestamp>/ behind to take one of the five prune slots from a real run.
func TestViewLogCreatesNoRunDir(t *testing.T) {
	useLogDir(t, t.TempDir())

	m := fixedModel()
	next, _ := m.openLogPicker()
	m = next.(model)
	for _, c := range logCategoryLabels {
		next, _ = m.openLogCategory(c.ID)
		_ = next.(model).View()
	}

	if _, err := os.Stat(filepath.Join(logDir, runDirName())); !os.IsNotExist(err) {
		t.Fatalf("viewing logs created the run directory (stat err = %v)", err)
	}
}

// TestLogsAreSanitized: what a step streamed (colours, a progress bar, an
// OSC 52 clipboard write) lands in the log as plain text, and an old log that
// still holds raw escapes is shown plain by View log.
func TestLogsAreSanitized(t *testing.T) {
	useLogDir(t, t.TempDir())

	m := fixedModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: step{title: "up", logLabel: "Topgrade completed",
		category: logCategoryTopgrade, alwaysLog: true}, status: statusRunning}}
	m.current = 0
	for _, line := range []string{"\x1b[1m:: \x1b[0mupgrading", "10%\r100%", "\x1b]52;c;ZXZpbA==\x07done"} {
		next, _ := m.handleStream(streamBatchMsg{lines: []string{line}})
		m = next.(model)
	}
	if got := m.buf.String(); got != "\x1b[1m:: \x1b[0mupgrading\n100%\ndone\n" {
		t.Fatalf("output pane = %q: colours kept, everything else stripped", got)
	}
	m.handleStream(streamBatchMsg{done: true})

	data, err := os.ReadFile(logPath(logCategoryTopgrade))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.ContainsAny(log, "\x1b\r") {
		t.Fatalf("log still holds escapes or CRs: %q", log)
	}
	if !strings.Contains(log, ":: upgrading\n100%\ndone\n") {
		t.Fatalf("log lost the text: %q", log)
	}

	old := "[2026-09-28 00:58:00] OK: Topgrade completed\n\x1b[32mok\x1b[0m\x1b]0;title\x07\n"
	if err := os.WriteFile(logPath(logCategoryCleanup), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readLog(logCategoryCleanup); strings.ContainsAny(got, "\x1b\x07") || !strings.Contains(got, "ok\n") {
		t.Fatalf("View log shows %q, want the old log as plain text", got)
	}
}
