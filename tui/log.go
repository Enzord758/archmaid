package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dryRun is set by the --dryrun flag: mutating commands are logged instead of
// executed, and the log goes to a separate file.
var dryRun bool

// logPath is ~/.maintenance_tui.log normally, ~/.maintenance_dryrun.log under
// --dryrun so a dry run never touches the real log.
func logPath() string {
	home, _ := os.UserHomeDir()
	name := ".maintenance_tui.log"
	if dryRun {
		name = ".maintenance_dryrun.log"
	}
	return filepath.Join(home, name)
}

// appendLog writes a timestamped entry. kind is a short tag (FAIL, DRYRUN,
// SKIP). If body is non-empty it's wrapped in an output block. Normal runs only
// log failures; a dry run logs every action.
func appendLog(kind, label, body string) {
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05")
	if body == "" {
		fmt.Fprintf(f, "[%s] %s: %s\n", ts, kind, label)
		return
	}
	fmt.Fprintf(f, "[%s] %s: %s\n--- %s output ---\n%s--- end output ---\n",
		ts, kind, label, label, body)
}

func readLog() string {
	b, err := os.ReadFile(logPath())
	if err != nil || len(b) == 0 {
		return statusStyle.Render("Log is empty.")
	}
	return string(b)
}
