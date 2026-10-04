package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// dryRun is set by --dryrun: mutating commands are logged instead of executed.
var dryRun bool

// logDir overrides the log directory (tests set it). Empty means the default,
// ~/.local/state/archmaid — a user-writable location, no root/system dir needed.
var logDir string

// runStamp names this run's log directory (YYYYMMDD-HHMM). Fixed for the
// process so every entry from one run lands under the same directory.
var runStamp = time.Now().Format("20060102-1504")

// logCategory buckets a step's log entries so one run's output doesn't land
// in a single undifferentiated file. Every step falls into exactly one
// bucket; unset step.logCategory defaults to logCategoryCleanup.
type logCategory string

const (
	logCategoryTopgrade logCategory = "topgrade"
	logCategoryOrphans  logCategory = "orphans"
	logCategoryCleanup  logCategory = "cleanup" // default: everything else
	logCategoryAudit    logCategory = "audit"   // AUR audit results (see audit.go)
)

// logCategoryLabels drives the "View log" category picker, in display order.
var logCategoryLabels = []struct {
	id    logCategory
	label string
}{
	{logCategoryTopgrade, "Topgrade"},
	{logCategoryOrphans, "Orphan removal"},
	{logCategoryCleanup, "Cleanup"},
	{logCategoryAudit, "AUR audit"},
}

// categoryLabel is cat's display name, falling back to the raw value for any
// category that isn't in logCategoryLabels (shouldn't happen in practice).
func categoryLabel(cat logCategory) string {
	for _, c := range logCategoryLabels {
		if c.id == cat {
			return c.label
		}
	}
	return string(cat)
}

// logKeepRuns is how many run directories pruneLogs keeps; older ones are
// removed entirely. dryrun's directory is exempt — it's a single reused slot,
// not a growing history.
const logKeepRuns = 5

// runDirPattern matches a run directory name (runStamp's own format), so
// pruneLogs only ever touches directories it created itself.
var runDirPattern = regexp.MustCompile(`^\d{8}-\d{4}$`)

// resolveLogDir is the override when set, otherwise ~/.local/state/archmaid.
func resolveLogDir() string {
	if logDir != "" {
		return logDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "archmaid")
}

// runDirName is the directory this process's log entries land under: a fixed
// "dryrun" slot (reused/appended across dry runs) or this run's timestamp.
func runDirName() string {
	if dryRun {
		return "dryrun"
	}
	return runStamp
}

// logPath returns the file a given category logs to for this run:
// ~/.local/state/archmaid/<run>/<category>.log. It creates nothing: reading a
// log (View log) must not leave an empty run directory behind, which would
// take one of pruneLogs' logKeepRuns slots from a real run. appendLog creates
// the directory when there is something to write.
func logPath(cat logCategory) string {
	return filepath.Join(resolveLogDir(), runDirName(), string(cat)+".log")
}

// Logs hold full command output (package lists, paths, AUR maintainers), so
// they're private to the user. Directories and files created before this was
// tightened are fixed up on the next write.
const (
	logDirMode  = 0o700
	logFileMode = 0o600
)

// pruneLogs deletes all but the logKeepRuns most recent run directories.
// Called once at startup; a no-op if the log dir doesn't exist yet. The
// dryrun directory and anything that isn't a run directory (stray files, a
// user's own subdirectories) are left alone.
func pruneLogs() {
	dir := resolveLogDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() && runDirPattern.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	if len(runs) <= logKeepRuns {
		return
	}
	sort.Strings(runs) // runStamp's format sorts lexicographically = chronologically
	for _, name := range runs[:len(runs)-logKeepRuns] {
		os.RemoveAll(filepath.Join(dir, name))
	}
}

// appendLog writes a timestamped entry to cat's log. kind is a short tag
// (FAIL, DRYRUN, SKIP, OK). If body is non-empty it's wrapped in an output
// block. Normal runs only log failures and steps marked step.alwaysLog; a dry
// run logs every action.
func appendLog(cat logCategory, kind, label, body string) {
	path := logPath(cat)
	run := filepath.Dir(path)
	if os.MkdirAll(run, logDirMode) != nil {
		return
	}
	os.Chmod(resolveLogDir(), logDirMode) // MkdirAll leaves existing dirs as they were
	os.Chmod(run, logDirMode)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFileMode)
	if err != nil {
		return
	}
	defer f.Close()
	f.Chmod(logFileMode) // a file from an older version may still be 0644
	// Plain text only: a log must not replay escapes in whatever terminal
	// views it later (topgrade's colours, or anything an AUR build printed).
	label, body = Sanitize(label, SanitizePlain), Sanitize(body, SanitizePlain)
	ts := time.Now().Format("2006-01-02 15:04:05")
	if body == "" {
		fmt.Fprintf(f, "[%s] %s: %s\n", ts, kind, label)
		return
	}
	fmt.Fprintf(f, "[%s] %s: %s\n--- %s output ---\n%s--- end output ---\n",
		ts, kind, label, label, body)
}

func readLog(cat logCategory) string {
	b, err := os.ReadFile(logPath(cat))
	if err != nil || len(b) == 0 {
		return statusStyle.Render("Log is empty.")
	}
	return Sanitize(string(b), SanitizePlain) // logs from older versions hold raw escapes
}
