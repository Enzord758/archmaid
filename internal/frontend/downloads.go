package frontend

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── "Delete old downloads": the step glue ────────────────────────────────────
//
// Which folder, what is old and the deletion itself (every safety property:
// ctime, Lstat, os.Root, the mount-point boundary, the re-check before each
// removal) live in internal/downloads. This file turns them into a discovery
// delete step's Run (actions.Env.DeleteDownloads; the discovery is in
// internal/actions) and keeps the abort bookkeeping.

// DownloadsOptions is the downloads resolver's input from the process
// environment ($ARCHMAID_DOWNLOADS, $HOME, the passwd home,
// $XDG_CONFIG_HOME), with the front end's folder (flag: --downloads or the
// Settings page; "" for none) and dry-run flag. Read it when a discovery or
// step runs, so a folder set later still counts. Now and EntryHook are left
// to the caller (nil: the real clock, no hook).
func DownloadsOptions(flag string, dry bool) downloads.Options {
	return downloads.Options{
		Flag:          flag,
		Env:           os.Getenv(downloads.EnvVar),
		Home:          os.Getenv("HOME"),
		PasswdHome:    downloads.PasswdHome(),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		DryRun:        dry,
	}
}

// Deletions counts the downloads deletions still in flight, with their
// pending abort records, so a front end can let them finish before it
// exits (Wait). The zero value is ready; it must not be copied once used.
type Deletions struct{ wg sync.WaitGroup }

// Wait blocks until no deletion is in flight and no abort record is
// pending. After an abort a run stops at the end of the entry it is
// deleting, so this is short.
func (d *Deletions) Wait() { d.wg.Wait() }

// Step is the delete step's Run for plan (actions.Env.DeleteDownloads):
// opts is read as the step runs, store gets the abort record. An abort
// cancels ctx, which stops the deletion between entries: nothing after
// that is touched.
//
// An aborted run's result is dropped and the engine never logs it, so a
// real run puts what it deleted on record itself: when it sees the abort,
// and — should the abort land after it returned but before the engine took
// its result — from a context.AfterFunc. The AfterFunc logs only for an
// abort (StepAborted): once the engine has taken the result its Driver
// cancels ctx with steps.ErrResultTaken, which the AfterFunc lets pass, so
// a result taken without its commit run (refused, or gated behind a
// confirm) leaves no false abort record. The commit just disarms it early.
// Either way the cleanup log gets one entry naming exactly what was
// deleted, in store: both run on a Driver worker, so the front end
// captures store where it sets the step up and never looks it up there.
func (d *Deletions) Step(plan downloads.Plan, store *logstore.Store, opts func() downloads.Options) func(context.Context, steps.Env) steps.Result {
	return func(ctx context.Context, _ steps.Env) steps.Result {
		d.wg.Add(1)
		defer d.wg.Done()
		opt := opts()
		r, err := downloads.Delete(ctx, plan, opt)
		res := steps.Result{Output: r.Output, Err: err}
		if opt.DryRun {
			return res // nothing deleted; the engine logs the DRYRUN entry
		}
		if ctx.Err() != nil {
			logAbortedDownloads(store, res.Output)
			return res
		}
		if ctx.Done() == nil {
			return res // can't be aborted (a test's context.Background())
		}
		d.wg.Add(1) // until the AfterFunc has run or been disarmed
		stop := context.AfterFunc(ctx, func() {
			defer d.wg.Done()
			if StepAborted(ctx) {
				logAbortedDownloads(store, res.Output)
			}
		})
		res.Commit = func() error {
			if stop() {
				d.wg.Done()
			}
			return nil
		}
		return res
	}
}

// StepAborted reports whether an in-process step's ctx was cancelled by an
// abort, as opposed to released after its result was taken
// (steps.ErrResultTaken).
func StepAborted(ctx context.Context) bool {
	return ctx.Err() != nil && !errors.Is(context.Cause(ctx), steps.ErrResultTaken)
}

// logAbortedDownloads records an aborted real run's output — what it deleted
// and where it stopped — in place of the entry the engine would have
// written. A failed write is dropped: logging must never break a step.
func logAbortedDownloads(store *logstore.Store, output string) {
	_ = store.Append(logstore.Cleanup, "ABORT", "Old downloads deleted (aborted)", output)
}
