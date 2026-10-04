package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// seedHistory points logDir at a scratch state dir holding logKeepRuns+1 run
// directories (one more than a prune keeps), dated well before this process's
// run, and returns a listing of everything in it.
func seedHistory(t *testing.T) (dir string, before []string) {
	t.Helper()
	dir = t.TempDir()
	useLogDir(t, dir)
	for i := range logKeepRuns + 1 {
		r := filepath.Join(dir, time.Date(2020, 1, 1+i, 9, 0, 0, 0, time.UTC).Format("20060102-1504"))
		if err := os.Mkdir(r, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r, "cleanup.log"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, listTree(t, dir)
}

// listTree is every path under dir, relative and sorted.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// missing is what of before is no longer in after.
func missing(before, after []string) []string {
	have := map[string]bool{}
	for _, p := range after {
		have[p] = true
	}
	var gone []string
	for _, p := range before {
		if !have[p] {
			gone = append(gone, p)
		}
	}
	return gone
}

// TestStartupDeletesNothing is the regression test for the deleted run
// folder: every way a start can go before a real run writes anything — a dry
// run (including its own log writes), --help, a bad flag, the askpass relay,
// a real start that only shows the menu — leaves a state dir holding more
// than logKeepRuns runs exactly as it was. The old main() pruned right after
// parseArgs, so --dryrun deleted the oldest run, and ignored unknown flags
// (--help started the TUI and pruned too).
func TestStartupDeletesNothing(t *testing.T) {
	origDry, origDl := dryRun, downloadsFlag
	t.Cleanup(func() { dryRun, downloadsFlag = origDry, origDl })

	for _, tc := range []struct {
		name      string
		args      []string
		code      int
		start     bool
		out, errs string // substrings of stdout and stderr ("" = must be empty)
	}{
		{name: "dryrun", args: []string{"--dryrun"}, start: true},
		{name: "plain start", args: nil, start: true},
		{name: "help", args: []string{"--help"}, out: "Usage: archmaid"},
		{name: "-h", args: []string{"--dryrun", "-h"}, out: "Usage: archmaid"},
		{name: "unknown flag", args: []string{"--bogus"}, code: 2, errs: `unknown option "--bogus"`},
		{name: "typo of dryrun", args: []string{"--dry-run"}, code: 2, errs: `unknown option "--dry-run"`},
		{name: "positional", args: []string{"clean"}, code: 2, errs: `unexpected argument "clean"`},
		{name: "bad downloads", args: []string{"--downloads"}, code: 2, errs: "--downloads needs a folder"},
		{name: "askpass relay", args: []string{"--askpass", filepath.Join(t.TempDir(), "no.sock"), "Password:"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dryRun, downloadsFlag = false, ""
			dir, before := seedHistory(t)
			var stdout, stderr bytes.Buffer
			code, start := startup(tc.args, &stdout, &stderr)
			if code != tc.code || start != tc.start {
				t.Fatalf("startup(%q) = %d, %v; want %d, %v (stderr %q)",
					tc.args, code, start, tc.code, tc.start, stderr.String())
			}
			for _, w := range []struct{ name, got, want string }{
				{"stdout", stdout.String(), tc.out}, {"stderr", stderr.String(), tc.errs},
			} {
				if (w.want == "") != (w.got == "") || !strings.Contains(w.got, w.want) {
					t.Fatalf("%s = %q, want it to contain %q", w.name, w.got, w.want)
				}
			}
			if start && dryRun {
				// What the dry-run TUI then writes: its own dryrun/ slot only.
				_ = logs().Append(logCategoryCleanup, "DRYRUN", "Test step", "would run: true\n")
				_ = logs().WriteLastRun(logstore.RunRecord{Action: "Test", Started: time.Now(),
					Finished: time.Now(), Outcome: logstore.OutcomeOK, DryRun: true})
			}
			after := listTree(t, dir)
			if gone := missing(before, after); len(gone) > 0 {
				t.Fatalf("startup(%q) deleted from the state dir: %v", tc.args, gone)
			}
			for _, p := range missing(after, before) {
				if !strings.HasPrefix(p, "dryrun") {
					t.Fatalf("startup(%q) wrote %s outside dryrun/", tc.args, p)
				}
			}
		})
	}
}

// TestRealRunRotatesHistory: a real run still keeps logKeepRuns runs — the
// rotation happens when it writes its first entry, and counts the new run.
func TestRealRunRotatesHistory(t *testing.T) {
	origDry := dryRun
	logMu.Lock()
	oldStarted, oldStore := logStarted, theLogStore
	logStarted, theLogStore = time.Date(2026, 9, 28, 0, 58, 0, 0, time.UTC), nil
	logMu.Unlock()
	t.Cleanup(func() {
		dryRun = origDry
		logMu.Lock()
		logStarted, theLogStore = oldStarted, oldStore
		logMu.Unlock()
	})
	dryRun = false

	dir, before := seedHistory(t)
	if code, start := startup(nil, &bytes.Buffer{}, &bytes.Buffer{}); !start || code != 0 {
		t.Fatalf("startup() = %d, %v", code, start)
	}
	if gone := missing(before, listTree(t, dir)); len(gone) > 0 {
		t.Fatalf("a real start pruned before writing anything: %v", gone)
	}
	_ = logs().Append(logCategoryCleanup, "FAIL", "Test step", "")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, e := range ents {
		runs = append(runs, e.Name())
	}
	if len(runs) != logKeepRuns || runs[0] != "20200103-0900" || runs[len(runs)-1] != "20260928-0058" {
		t.Fatalf("after the first real entry: %v; want the %d newest, this run included", runs, logKeepRuns)
	}
}
