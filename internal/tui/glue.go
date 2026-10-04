package tui

import (
	"fmt"
	"io"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/frontend"
)

// ── the TUI's settings for the front-end glue ────────────────────────────────
//
// The audit and downloads glue every front end shares lives in
// internal/frontend. This file holds what is the TUI's own: the command
// line and the test seams it is configured from (package vars that only
// main, before the program starts, and tests set), and the wait for an
// aborted deletion before the process exits.

// aurRPCURL is the AUR RPC endpoint. A var so tests can point it at an
// httptest server.
var aurRPCURL = audit.DefaultRPCURL

// auditDeadline bounds a whole audit (see audit.Config.Deadline). A var so
// tests can shrink it.
var auditDeadline = audit.DefaultDeadline

// currentAuditConfig is every setting an audit reads, taken from the package
// vars: the AUR endpoint, the snapshot file (read, and later written by the
// result's Commit), the deadline, whether this is a dry run, and the log
// store its entries go to. It is called before the program starts
// (actionEnv, for the menu audit) or on the Driver's loop (the audit step
// when a queue is built, startPrefetch for a prefetch), before the audit
// goes off on its own, so the audit never reads those vars while something
// else (a test, mostly) changes them.
func currentAuditConfig() audit.Config {
	return frontend.AuditConfig(logs(), frontend.AuditOptions{
		RPCURL: aurRPCURL, Deadline: auditDeadline, DryRun: dryRun,
	})
}

// startPrefetch starts the prefetch named name (driver.Deps.Prefetch): the
// AUR audit's lookup, so it runs behind the password prompt or the gate;
// nil for a name the TUI doesn't know. It runs on the Driver's loop and
// reads the audit's config there.
var startPrefetch = frontend.Prefetcher(currentAuditConfig)

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

// downloadsOptions gathers the process state the downloads package takes as
// values. It is read when a discovery or step runs, so flags and seams set
// later still count.
func downloadsOptions() downloads.Options {
	o := frontend.DownloadsOptions(downloadsFlag, dryRun)
	o.Now, o.EntryHook = downloadsNow, downloadsEntryHook
	return o
}

// downloadsRunning counts deletions (and their pending abort records) still
// in flight, so main can let them finish before the process exits.
var downloadsRunning frontend.Deletions

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
