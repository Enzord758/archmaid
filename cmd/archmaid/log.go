package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
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
	logMu       sync.Mutex // guards the three below
	theLogStore *logstore.Store
	theLogKey   logStoreKey
	// logStarted names this process's run directory. It is fixed by the
	// first store, so a store rebuilt later (a test moving logDir) keeps the
	// same <timestamp>/ even if the minute has turned since.
	logStarted time.Time
)

// logNow is the clock that stamps the run directory: a seam for tests.
var logNow = time.Now

// logs is the process's one log store, built from logDir and dryRun. Both are
// plain variables (--dryrun sets one after init, tests set the other), so the
// store is rebuilt if either has changed since it was made; in a real run that
// never happens after startup.
//
// It reads logDir and dryRun, so call it on the model's goroutine (or before
// the program starts). Work that runs off it — a Cmd, an in-process step, a
// context.AfterFunc — takes the *logstore.Store captured where it was set up
// instead: a Store never changes once made and is safe from any goroutine.
// The mutex only keeps the cache itself whole.
func logs() *logstore.Store {
	logMu.Lock()
	defer logMu.Unlock()
	key := logStoreKey{logDir, dryRun}
	if theLogStore == nil || key != theLogKey {
		if logStarted.IsZero() {
			logStarted = logNow()
		}
		theLogStore = logstore.New(logstore.Options{Dir: logDir, DryRun: dryRun, Now: runClock(logStarted)})
		theLogKey = key
	}
	return theLogStore
}

// runClock is time.Now, except for its first call: logstore.New reads the
// clock once, first, to name the run directory (see logstore.Options.Now),
// and gets start. Every entry after that is stamped with the real time.
func runClock(start time.Time) func() time.Time {
	var named atomic.Bool
	return func() time.Time {
		if named.CompareAndSwap(false, true) {
			return start
		}
		return time.Now()
	}
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
// failed write is dropped: logging must never break a step. Model goroutine
// only (see logs); elsewhere, Append to a captured store.
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

// queueOutcome classifies a queue that has just ended, first match wins:
// cancelled when no step ran at all (every one skipped, e.g. sudo gave up),
// aborted when a declined required step stopped it, failed on any failed
// step, incomplete when a step was refused privilege (sudo gave up part-way:
// the run is not a clean one, whatever ran fine), otherwise ok.
func (m *model) queueOutcome(declined bool) string {
	ran, refused := false, false
	for _, q := range m.queue {
		switch q.status {
		case steps.OK, steps.Failed:
			ran = true
		case steps.AuthRefused:
			refused = true
		}
	}
	switch {
	case !ran:
		return logstore.OutcomeCancelled
	case declined:
		return logstore.OutcomeAborted
	case m.errs > 0:
		return logstore.OutcomeFailed
	case refused:
		return logstore.OutcomeIncomplete
	}
	return logstore.OutcomeOK
}

// recordRun writes last-run.json for the run in progress, at most once per run
// (activate and resetRun re-arm it). It sits where appendLog does: a tiny
// synchronous write; a failed one is dropped like a failed log write. A dry run
// goes to the dry-run slot, never over the real record (see logstore).
func (m *model) recordRun(outcome string) {
	if m.runDone || m.runAction == "" {
		return
	}
	m.runDone = true
	skipped, refused := 0, 0
	for _, q := range m.queue {
		switch q.status {
		case steps.AuthRefused:
			refused++
			skipped++
		case steps.Skipped, steps.Aborted:
			skipped++
		}
	}
	_ = logs().WriteLastRun(logstore.RunRecord{
		Action: m.runAction, Started: m.runStarted, Finished: time.Now(),
		Outcome: outcome, StepsFailed: m.errs, StepsSkipped: skipped,
		StepsRefused: refused, DryRun: dryRun,
	})
}
