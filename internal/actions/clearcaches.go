package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// Clear caches (owner decision 2026-10-18; it replaces "Clear user cache"
// and "Delete entire package cache"): it measures both caches first, as the
// user and read-only, then offers a pick of two rows, nothing ticked. The
// pick IS the destructive confirm (the rows show the sizes and the
// package-cache row carries its warning), so the steps it builds have no
// confirm of their own. Only the ticked rows run; the password is asked
// after the pick, and only if the package cache was ticked (the engine
// plans the pick's queue: the user-cache step alone needs no root).

// Pick keys (NeedsSelection.Items): short, single-token and stable.
const (
	PickUserCache = "~/.cache"
	PickPkgCache  = "pkgcache"
)

// wipeDryArgv is the dry run the package-cache row is built from: what
// `paccache -rk0` would remove, listed and counted, removing nothing. The
// cache is world-readable, so it runs as the user, with no root and no
// password. --nocolor keeps the summary line free of escapes.
var wipeDryArgv = []string{"--nocolor", "-dk0"}

// wipeTimeout bounds the dry run: a cache on a hung mount must not leave the
// action on "Measuring…" forever. A variable so a test can shorten it.
var wipeTimeout = 60 * time.Second

// userCacheTimeout bounds the walk of ~/.cache. A variable for tests.
var userCacheTimeout = 10 * time.Second

// pkgWarning stays visible on the package-cache row.
// userKeepNote is the ~/.cache row's subtitle: the step is
// rm -rf "$HOME/.cache/"*, which leaves dot entries alone (owner decision
// 2026-10-18: keep that). The size above does not count them either.
const userKeepNote = "Hidden files are kept"

const pkgWarning = "Can't reinstall or downgrade those packages without internet"

// cacheMeasure is one cache's measurement: bytes and a count when it
// worked, else why not.
type cacheMeasure struct {
	bytes uint64
	files int    // package files (package cache only)
	fail  string // the look failed: why (one line)
	log   string // the command and its raw output, for the discovery's log
	slow  bool   // the walk ran out of time: the folder exists, the size is unknown
}

func (m cacheMeasure) empty() bool { return m.fail == "" && !m.slow && m.bytes == 0 }

// measureUserCache sizes what `rm -rf "$HOME/.cache/"*` removes: the
// entries of $HOME/.cache whose names don't start with a dot.
func measureUserCache(ctx context.Context) cacheMeasure {
	home := os.Getenv("HOME")
	if home == "" {
		return cacheMeasure{fail: "no home folder"}
	}
	dir := filepath.Join(home, ".cache")
	qctx, cancel := context.WithTimeout(ctx, userCacheTimeout)
	defer cancel()
	_, n, err := sysstat.EntriesUsage(qctx, dir)
	switch {
	case err == nil:
		return cacheMeasure{bytes: n, log: fmt.Sprintf("$ measure ~/.cache\n%d bytes\n", n)}
	case errors.Is(err, os.ErrNotExist):
		return cacheMeasure{log: "$ measure ~/.cache\nno such folder\n"} // nothing to clear
	case errors.Is(qctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return cacheMeasure{slow: true, log: "$ measure ~/.cache\ntimed out\n"}
	}
	return cacheMeasure{fail: clipErr(err), log: "$ measure ~/.cache\nerror: " + err.Error() + "\n"}
}

// measurePkgCache is paccache's read-only dry run as the user.
func measurePkgCache(ctx context.Context, env Env) cacheMeasure {
	cmd := "$ paccache " + strings.Join(wipeDryArgv, " ")
	fail := func(text, log string) cacheMeasure {
		return cacheMeasure{fail: sanitize.OneLine(text), log: cmd + "\n" + log}
	}
	if _, err := env.lookPath("paccache"); err != nil {
		return fail("paccache is not installed (pacman-contrib)", "error: "+err.Error()+"\n")
	}
	qctx, cancel := context.WithTimeout(ctx, wipeTimeout)
	defer cancel()
	out, err := proc.Query(qctx, "paccache", wipeDryArgv...)
	if err != nil {
		if errors.Is(qctx.Err(), context.DeadlineExceeded) {
			err = context.DeadlineExceeded // killed by the deadline: exec only says "signal: killed"
		}
		return fail(clipErr(err), string(out)+"error: "+err.Error()+"\n")
	}
	n, b, ok := report.ParsePaccacheDry(string(out))
	if !ok {
		return fail("couldn't read paccache's dry-run summary", string(out))
	}
	return cacheMeasure{bytes: b, files: n, log: cmd + "\n" + string(out)}
}

// discoverClearCaches measures both caches (side by side: the package
// cache's dry run can take a while) and offers the pick.
func discoverClearCaches(ctx context.Context, env Env) Discovery {
	var user, pkg cacheMeasure
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); user = measureUserCache(ctx) }()
	go func() { defer wg.Done(); pkg = measurePkgCache(ctx, env) }()
	wg.Wait()

	userRow := PickRow{Label: "~/.cache", Note: userKeepNote}
	switch {
	case user.fail != "":
		userRow.Disabled, userRow.Note = true, "Couldn't measure it: "+user.fail
	case user.empty():
		userRow.Disabled, userRow.Note = true, "Empty: nothing to clear"
	case user.slow:
		userRow.Label += " — size unknown (too big to measure quickly)"
	default:
		userRow.Label += " — " + report.HumanBytes(user.bytes)
	}
	pkgRow := PickRow{Label: "Entire package cache"}
	switch {
	case pkg.fail != "":
		pkgRow.Disabled, pkgRow.Note = true, "Couldn't measure it: "+pkg.fail
	case pkg.empty():
		pkgRow.Disabled, pkgRow.Note = true, "Empty: nothing to delete"
	default:
		pkgRow.Label += fmt.Sprintf(" — %s (%d %s)", report.HumanBytes(pkg.bytes), pkg.files, plural(pkg.files, "file", "files"))
		pkgRow.Note = pkgWarning
	}

	chk := Checked{
		Summary: "Measured the caches: " + userSummary(user) + "; " + pkgSummary(pkg) + ".",
		Details: []string{"~/.cache: " + userSummary(user), "package cache: " + pkgSummary(pkg)},
		Log:     user.log + pkg.log,
	}
	if userRow.Disabled && pkgRow.Disabled {
		n := Notice{Text: "Nothing to clear. ~/.cache: " + userRow.Note + ". Package cache: " + pkgRow.Note + ".",
			Details: chk.Details, Log: chk.Log}
		if user.fail != "" || pkg.fail != "" {
			n.Err = sanitize.OneLine("Couldn't measure the caches: " + userRow.Note + "; " + pkgRow.Note)
		}
		return n
	}
	return NeedsSelection{
		Items:   []string{PickUserCache, PickPkgCache},
		Rows:    []PickRow{userRow, pkgRow},
		Steps:   ClearCachesSteps,
		Checked: chk,
	}
}

func userSummary(m cacheMeasure) string {
	switch {
	case m.fail != "":
		return "~/.cache couldn't be measured (" + m.fail + ")"
	case m.slow:
		return "~/.cache too big to measure quickly"
	}
	return "~/.cache " + report.HumanBytes(m.bytes)
}

func pkgSummary(m cacheMeasure) string {
	if m.fail != "" {
		return "package cache couldn't be measured (" + m.fail + ")"
	}
	return fmt.Sprintf("package cache %s, %d %s", report.HumanBytes(m.bytes), m.files, plural(m.files, "file", "files"))
}

// ClearCachesSteps builds the queue for a pick: ~/.cache first (as the
// user), then the package cache (root). Anything else picked is ignored;
// nothing picked is an empty queue, which runs nothing. Neither step has a
// confirm: the pick was it.
func ClearCachesSteps(picked []string) []steps.Step {
	var out []steps.Step
	for _, p := range picked {
		if p == PickUserCache {
			out = append(out, userCacheStep())
		}
	}
	for _, p := range picked {
		if p == PickPkgCache {
			out = append(out, pkgCacheWipeStep())
		}
	}
	return out
}

func userCacheStep() steps.Step {
	return steps.Step{
		ID:    steps.IDUserCache,
		Title: "Cleaning user cache…", LogLabel: "User cache cleared",
		Argv:      []string{"bash", "-c", "rm -rf \"$HOME/.cache/\"*"},
		AlwaysLog: true,
	}
}

// pkgCacheWipeStep removes every cached package: the root helper's
// paccache-wipe verb (sudo paccache -rk0 in the TUI); it takes no argument.
func pkgCacheWipeStep() steps.Step {
	return steps.Step{
		ID: steps.IDPaccacheWipe, Verb: steps.VerbPaccacheWipe,
		Title: "Deleting entire package cache…", LogLabel: "Entire package cache removed",
		Argv:      []string{"sudo", "paccache", "-rk0"},
		NeedsPriv: true, Optional: true, AlwaysLog: true,
	}
}
