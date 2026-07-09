package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// logPath matches the bash version: ~/.maintenance_tui.log
func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".maintenance_tui.log")
}

// appendLog writes a delimited, timestamped block of a failed step's output —
// error-only, same policy as the bash log_output helper.
func appendLog(label, output string) {
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "[%s] FAIL: %s\n--- %s output ---\n%s--- end output ---\n",
		ts, label, label, output)
}

// appendSkipLog records a step that was skipped (e.g. sudo auth gave up), so
// the log explains why a step didn't run rather than silently omitting it.
func appendSkipLog(label, reason string) {
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "[%s] SKIP: %s (%s)\n", ts, label, reason)
}

func readLog() string {
	b, err := os.ReadFile(logPath())
	if err != nil || len(b) == 0 {
		return statusStyle.Render("Log is empty.")
	}
	return string(b)
}
