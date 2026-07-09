package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dryRun is set by --dryrun: mutating commands are logged instead of executed.
var dryRun bool

// logDir is where real (non-dry-run) logs live — one timestamped file per run.
// A var so tests can redirect it and so a fallback can replace it when
// /var/log/archmaid hasn't been created (see the install note in the README).
var logDir = "/var/log/archmaid"

// runStamp names this run's log file (YYYYMMDD-HHMM). Fixed for the process so
// every entry from one run lands in the same file.
var runStamp = time.Now().Format("20060102-1504")

// logPath returns the file this run logs to.
//
//   - Real runs → /var/log/archmaid/<YYYYMMDD-HHMM>.log, falling back to the
//     user's state dir when /var/log/archmaid isn't writable.
//   - Dry runs → a single, un-timestamped user file that never touches
//     /var/log and is fine to overwrite.
func logPath() string {
	if dryRun {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".archmaid-dryrun.log")
	}
	return filepath.Join(realLogDir(), runStamp+".log")
}

// realLogDir is logDir when writable, otherwise ~/.local/state/archmaid
// (XDG_STATE_HOME honoured) so the app still logs before /var/log is set up.
func realLogDir() string {
	if ensureWritable(logDir) {
		return logDir
	}
	fallback := filepath.Join(userStateDir(), "archmaid")
	os.MkdirAll(fallback, 0o755)
	return fallback
}

// ensureWritable creates dir if the parent allows and confirms we can write it.
func ensureWritable(dir string) bool {
	if os.MkdirAll(dir, 0o775) != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func userStateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state")
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
