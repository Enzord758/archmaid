package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// dryRun is set by --dryrun: mutating commands are logged instead of executed.
var dryRun bool

// logDir overrides the log directory (tests set it). Empty means the default,
// ~/.local/state/archmaid — a user-writable location, no root/system dir needed.
var logDir string

// runStamp names this run's log file (YYYYMMDD-HHMM). Fixed for the process so
// every entry from one run lands in the same file.
var runStamp = time.Now().Format("20060102-1504")

// logRetention is how long a run log is kept before pruneLogs removes it.
const logRetention = 7 * 24 * time.Hour

// resolveLogDir is the override when set, otherwise ~/.local/state/archmaid.
func resolveLogDir() string {
	if logDir != "" {
		return logDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "archmaid")
}

// logPath returns the file this run logs to, under ~/.local/state/archmaid:
// a timestamped per-run file for real runs, a single overwritable dryrun.log
// for dry runs.
func logPath() string {
	dir := resolveLogDir()
	os.MkdirAll(dir, 0o755)
	if dryRun {
		return filepath.Join(dir, "dryrun.log")
	}
	return filepath.Join(dir, runStamp+".log")
}

// pruneLogs deletes run logs older than logRetention. Called once at startup;
// a no-op if the log dir doesn't exist yet.
func pruneLogs() {
	dir := resolveLogDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-logRetention)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".log" {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
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
