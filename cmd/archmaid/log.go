package main

import (
	"path/filepath"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// The log itself lives in internal/logstore; this file is the TUI's seam to it.

// dryRun is set by --dryrun: mutating commands are logged instead of executed.
var dryRun bool

// logDir overrides the log directory (tests set it). Empty means the default,
// ~/.local/state/archmaid — a user-writable location, no root/system dir needed.
var logDir string

// logCategory and its values are logstore's, under the names the TUI uses.
type logCategory = logstore.Category

const (
	logCategoryTopgrade = logstore.Topgrade
	logCategoryOrphans  = logstore.Orphans
	logCategoryCleanup  = logstore.Cleanup // default: everything else
	logCategoryAudit    = logstore.Audit   // AUR audit results (see audit.go)
)

// logKeepRuns is how many run directories pruneLogs keeps.
const logKeepRuns = logstore.KeepRuns

// logStoreKey is what the package-level store was built from.
type logStoreKey struct {
	dir string
	dry bool
}

var (
	theLogStore *logstore.Store
	theLogKey   logStoreKey
)

// logs is the process's one log store, built from logDir and dryRun. Both are
// plain variables (--dryrun sets one after init, tests set the other), so the
// store is rebuilt if either has changed since it was made; in a real run that
// never happens after startup.
func logs() *logstore.Store {
	key := logStoreKey{logDir, dryRun}
	if theLogStore == nil || key != theLogKey {
		theLogStore = logstore.New(logstore.Options{Dir: logDir, DryRun: dryRun, Now: time.Now})
		theLogKey = key
	}
	return theLogStore
}

// resolveLogDir is the log root: the override when set, otherwise
// ~/.local/state/archmaid.
func resolveLogDir() string { return logs().Root() }

// runDirName is the name of the directory this process's entries land under.
func runDirName() string { return filepath.Base(logs().RunDir()) }

// logPath is the file a category logs to for this run; it creates nothing.
func logPath(cat logCategory) string { return logs().Path(cat) }

// pruneLogs keeps only the logKeepRuns most recent run directories. Called
// once at startup.
func pruneLogs() { logs().Prune(logKeepRuns) }

// appendLog writes a timestamped entry to cat's log (see logstore.Append). A
// failed write is dropped: logging must never break a step.
func appendLog(cat logCategory, kind, label, body string) {
	_ = logs().Append(cat, kind, label, body)
}

// readLog is cat's log for the View log screen, or "Log is empty." styled
// like the status line.
func readLog(cat logCategory) string {
	text, err := logs().Read(cat)
	if err != nil {
		return statusStyle.Render("Log is empty.")
	}
	return text
}

// categoryLabel is cat's display name.
func categoryLabel(cat logCategory) string { return cat.Label() }

// logCategoryLabels drives the "View log" category picker, in display order.
var logCategoryLabels = logstore.Categories
