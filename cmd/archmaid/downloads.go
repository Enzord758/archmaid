package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// ── "Delete old downloads": the step glue ────────────────────────────────────
//
// Which folder, what is old and the deletion itself (every safety property:
// ctime, Lstat, os.Root, the mount-point boundary, the re-check before each
// removal) live in internal/downloads. This file turns them into a discovery
// Cmd and an in-process step, and keeps the abort bookkeeping.

// downloadsFlag is set by --downloads <dir>. It overrides discovery and may
// point outside $HOME (a data disk), but never at a home folder, an ancestor
// of one, or /.
var downloadsFlag string

// downloadsNow is the clock for the age check — a seam for tests, which can't
// set ctimes and instead move "now".
var downloadsNow = time.Now

// downloadsEntryHook, if set, runs before each entry is handled (before the
// abort check) — a seam for tests to abort mid-deletion.
var downloadsEntryHook func(name string)

// downloadsOptions gathers the process state the package takes as values. It
// is read when a Cmd or step runs, so flags and seams set later still count.
func downloadsOptions() downloads.Options {
	return downloads.Options{
		Flag:          downloadsFlag,
		Env:           os.Getenv(downloads.EnvVar),
		Home:          os.Getenv("HOME"),
		PasswdHome:    downloads.PasswdHome(),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		DryRun:        dryRun,
		Now:           downloadsNow,
		EntryHook:     downloadsEntryHook,
	}
}

// discoverDownloads resolves the folder and lists its old entries. Deleting
// them is an in-process step that re-checks each entry first (see
// deleteOldDownloads). The log store the step records an abort in is captured
// here, on the model's goroutine, not in the Cmd.
func discoverDownloads() tea.Cmd {
	store := logs()
	return func() tea.Msg {
		notice := func(s string) tea.Msg { return discoveryMsg{kind: "downloads", notice: s} }
		opt := downloadsOptions()
		dir, err := downloads.Resolve(opt)
		if err != nil {
			return notice(sanitize.String(err.Error(), sanitize.Plain))
		}
		plan, err := downloads.Scan(dir, downloadsNow(), opt)
		if err != nil {
			return notice(err.Error())
		}
		if len(plan.Names) == 0 {
			return notice("Nothing older than 30 days in " + plan.Shown + ".")
		}
		return discoveryMsg{kind: "downloads", pending: []step{{
			title: "Deleting old downloads…", logLabel: "Old downloads deleted",
			run:      deleteOldDownloads(plan, store),
			optional: true, alwaysLog: true,
			confirm: plan.Confirm(),
		}}}
	}
}

// deleteOldDownloads is the delete step's run. An abort cancels ctx, which
// stops the deletion between entries: nothing after that is touched.
//
// An aborted run's result is dropped (stepSeq), and finishStep never logs
// it, so a real run puts what it deleted on record itself: when it sees the
// abort, and — should the abort land after it returned but before the model
// took its result — from a context.AfterFunc that the result's commit
// (called only when the result is taken) disarms. Either way cleanup.log gets
// one entry naming exactly what was deleted, in store: both run off the
// model's goroutine, so they must not look the store up themselves (logs).
func deleteOldDownloads(plan downloads.Plan, store *logstore.Store) func(context.Context) stepResult {
	return func(ctx context.Context) stepResult {
		downloadsRunning.Add(1)
		defer downloadsRunning.Done()
		opt := downloadsOptions()
		r, err := downloads.Delete(ctx, plan, opt)
		res := stepResult{output: r.Output, err: err}
		if opt.DryRun {
			return res // nothing deleted; finishStep logs the DRYRUN entry
		}
		if ctx.Err() != nil {
			logAbortedDownloads(store, res.output)
			return res
		}
		if ctx.Done() == nil {
			return res // can't be aborted (a test's context.Background())
		}
		downloadsRunning.Add(1) // until the AfterFunc has run or been disarmed
		stop := context.AfterFunc(ctx, func() {
			defer downloadsRunning.Done()
			logAbortedDownloads(store, res.output)
		})
		res.commit = func() error {
			if stop() {
				downloadsRunning.Done()
			}
			return nil
		}
		return res
	}
}

// downloadsRunning counts deletions (and their pending abort records) still
// in flight, so main can let them finish before the process exits.
var downloadsRunning sync.WaitGroup

// waitForDownloads blocks until no deletion is in flight, saying why on w if
// that takes a moment. After an abort a run stops at the end of the entry it
// is deleting, so this is short.
func waitForDownloads(w io.Writer) {
	done := make(chan struct{})
	go func() { downloadsRunning.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		fmt.Fprintln(w, "archmaid: waiting for the downloads deletion to stop…")
		<-done
	}
}

// logAbortedDownloads records an aborted real run's output — what it deleted
// and where it stopped — in place of the entry finishStep would have written.
// Like appendLog, a failed write is dropped.
func logAbortedDownloads(store *logstore.Store, output string) {
	_ = store.Append(logCategoryCleanup, "ABORT", "Old downloads deleted (aborted)", output)
}
