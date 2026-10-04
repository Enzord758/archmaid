package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
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

// TestStartDeletesNothing is the TUI's half of the regression test for the
// deleted run folder (cmd/archmaid's TestStartupDeletesNothing is the
// command line's): what main does with a start before a real run writes
// anything — Configure, ActionEnv, StartAskpass, NewDriver and Finish, and
// a dry run's own log writes — leaves a state dir holding more than
// logKeepRuns runs exactly as it was, and a dry run writes only dryrun/.
func TestStartDeletesNothing(t *testing.T) {
	origDry, origDl := dryRun, downloadsFlag
	t.Cleanup(func() { dryRun, downloadsFlag = origDry, origDl })

	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprint("dryrun=", dry), func(t *testing.T) {
			dir, before := seedHistory(t)
			Configure(dry, "")
			if dryRun != dry || downloadsFlag != "" {
				t.Fatalf("Configure(%v, \"\"): dryRun %v, downloadsFlag %q", dry, dryRun, downloadsFlag)
			}
			env := ActionEnv()
			if err := env.Validate(); err != nil {
				t.Fatal(err)
			}
			srv := StartAskpass()
			drv := NewDriver(engine.New(engine.Options{DryRun: dry, Env: env}), nil)
			drv.Close()
			StopAskpass(srv)
			var stderr bytes.Buffer
			Finish(&stderr)
			if stderr.Len() != 0 {
				t.Errorf("Finish wrote %q with nothing to report", stderr.String())
			}
			if dry {
				// What the dry-run TUI then writes: its own dryrun/ slot only.
				_ = logs().Append(logCategoryCleanup, "DRYRUN", "Test step", "would run: true\n")
				_ = logs().WriteLastRun(logstore.RunRecord{Action: "Test", Started: time.Now(),
					Finished: time.Now(), Outcome: logstore.OutcomeOK, DryRun: true})
			}
			after := listTree(t, dir)
			if gone := missing(before, after); len(gone) > 0 {
				t.Fatalf("a start deleted from the state dir: %v", gone)
			}
			for _, p := range missing(after, before) {
				if !strings.HasPrefix(p, "dryrun") {
					t.Fatalf("a start wrote %s outside dryrun/", p)
				}
			}
		})
	}
	Configure(true, "/data/dl")
	if !dryRun || downloadsFlag != "/data/dl" {
		t.Errorf("Configure(true, /data/dl): dryRun %v, downloadsFlag %q", dryRun, downloadsFlag)
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
	Configure(false, "")
	if err := ActionEnv().Validate(); err != nil {
		t.Fatal(err)
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
