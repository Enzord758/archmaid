package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/steps"
)

// isolateDownloads gives the test a scratch $HOME and XDG config dir, and
// clears every downloads override (flag and env), so the resolver never sees
// the real user-dirs.dirs or ~/Download. Returns the scratch home.
func isolateDownloads(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(downloads.EnvVar, "")
	orig := downloadsFlag
	downloadsFlag = ""
	t.Cleanup(func() { downloadsFlag = orig })
	return home
}

// writeUserDirs writes $XDG_CONFIG_HOME/user-dirs.dirs with the given
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDiscoverDownloadsNamesFolder: the notice names the folder it looked in
// (as ~/…), and with no folder at all it says how to set one.
func TestDiscoverDownloadsNamesFolder(t *testing.T) {
	home := isolateDownloads(t)

	n := noticeOf(discoverNow(t, actions.IDDownloads))
	if !strings.Contains(n, "No downloads folder found (set --downloads or ARCHMAID_DOWNLOADS)") {
		t.Fatalf("notice = %q, want the no-folder notice", n)
	}

	mkdirs(t, filepath.Join(home, "Download"))
	n = noticeOf(discoverNow(t, actions.IDDownloads))
	if !strings.Contains(n, "~/Download") {
		t.Fatalf("notice = %q, want it to name ~/Download", n)
	}
}

// ── old-entry deletion ───────────────────────────────────────────────────────
//
// Age is ctime, which a test can't set. So a test builds the "old" part of
// its fixture, takes a mark between two pauses, builds the "new" part, and
// moves the clock (downloadsNow) to mark+30 days: the cutoff then falls on
// the mark, after every old ctime and before every new one (the pauses
// absorb the kernel's coarse timestamp granularity).

// ctimeMark pauses, returns now, and pauses again: a point strictly between
// the ctimes of what was made before it and what is made after.
func ctimeMark(t *testing.T) time.Time {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	mark := time.Now()
	time.Sleep(30 * time.Millisecond)
	return mark
}

// cutoffAt sets the downloads clock so the 30-day cutoff lands on mark.
func cutoffAt(t *testing.T, mark time.Time) {
	t.Helper()
	orig := downloadsNow
	downloadsNow = func() time.Time { return mark.Add(downloads.MaxAge) }
	t.Cleanup(func() { downloadsNow = orig })
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestDownloadsThroughTheUI: discovery → the named confirm → "y" runs the
// in-process delete → summary, with what was deleted on record in
// cleanup.log, once: the engine's OK entry, the step's abort record
// disarmed by the commit.
func TestDownloadsThroughTheUI(t *testing.T) {
	home := isolateDownloads(t)
	oldLog := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = oldLog })
	dl := filepath.Join(home, "Downloads")
	writeFile(t, filepath.Join(dl, "old.iso"), "x")
	cutoffAt(t, ctimeMark(t))

	d := run(t, "", "Delete old downloads", Answer{"confirm", "old.iso", "yes"})
	if d.m.screen != screenSummary || d.m.errs != 0 {
		t.Fatalf("screen=%d errs=%d, want a clean summary\n%s", d.m.screen, d.m.errs, d.Transcript())
	}
	if exists(filepath.Join(dl, "old.iso")) {
		t.Fatal("old.iso survived")
	}
	waitForDownloads(io.Discard) // an abort record, had commit not disarmed it
	log, _ := os.ReadFile(logPath(logCategoryCleanup))
	if !strings.Contains(string(log), "OK: Old downloads deleted") || !strings.Contains(string(log), "deleted: old.iso") {
		t.Fatalf("cleanup.log should record the deletion:\n%s", log)
	}
	if strings.Count(string(log), ": Old downloads deleted") != 1 {
		t.Fatalf("cleanup.log should hold exactly one entry:\n%s", log)
	}
}

// startDownloadsDelete puts four old files in ~/Downloads and runs the
// discovery, returning the delete step's Run on a ctx the test cancels as
// the Driver does (abort: plain context.Canceled; result taken:
// steps.ErrResultTaken); logs go to a scratch dir.
func startDownloadsDelete(t *testing.T) (run func() steps.Result, cancel context.CancelCauseFunc, dl string) {
	t.Helper()
	home := isolateDownloads(t)
	oldLog := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = oldLog })
	dl = filepath.Join(home, "Downloads")
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		writeFile(t, filepath.Join(dl, n), "x")
	}
	cutoffAt(t, ctimeMark(t))
	p, ok := discoverNow(t, actions.IDDownloads).(actions.Pending)
	if !ok || len(p.Steps) != 1 || p.Steps[0].Run == nil {
		t.Fatalf("discovery = %#v, want the one delete step", p)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	step := p.Steps[0]
	return func() steps.Result { return step.Run(ctx, steps.Env{}) }, cancel, dl
}

// TestDownloadsAbortMidDeletionIsLogged: aborting partway through the delete
// drops the step's result, but cleanup.log still names exactly the entries
// that were deleted, and where it stopped.
func TestDownloadsAbortMidDeletionIsLogged(t *testing.T) {
	run, abort, dl := startDownloadsDelete(t)
	reached, resume := make(chan struct{}), make(chan struct{})
	downloadsEntryHook = func(name string) {
		if name == "c.txt" {
			close(reached)
			<-resume
		}
	}
	t.Cleanup(func() { downloadsEntryHook = nil })
	done := make(chan steps.Result, 1)
	go func() { done <- run() }()

	<-reached // a.txt and b.txt are gone
	abort(nil)
	close(resume)
	<-done // dropped, as the engine drops an aborted step's result

	log, _ := os.ReadFile(logPath(logCategoryCleanup))
	for _, want := range []string{"ABORT: Old downloads deleted (aborted)",
		"deleted: a.txt", "deleted: b.txt", "stopped: aborted before c.txt", "2 deleted, 0 kept, 0 failed"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("cleanup.log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(string(log), "deleted: c.txt") || strings.Contains(string(log), "deleted: d.txt") ||
		strings.Count(string(log), ": Old downloads deleted") != 1 {
		t.Fatalf("cleanup.log should hold one entry, naming only a.txt and b.txt:\n%s", log)
	}
	if exists(filepath.Join(dl, "a.txt")) || !exists(filepath.Join(dl, "c.txt")) || !exists(filepath.Join(dl, "d.txt")) {
		t.Fatal("want a.txt deleted, c.txt and d.txt kept")
	}
}

// TestDownloadsAbortAfterTheRunIsLogged: an abort that lands after the delete
// finished but before the engine took its result still leaves the record —
// the result is dropped, so the step writes it.
func TestDownloadsAbortAfterTheRunIsLogged(t *testing.T) {
	run, abort, dl := startDownloadsDelete(t)
	run() // the whole delete runs; its AfterFunc is armed
	abort(nil)
	waitForDownloads(io.Discard)
	log, _ := os.ReadFile(logPath(logCategoryCleanup))
	if !strings.Contains(string(log), "ABORT: Old downloads deleted (aborted)") ||
		!strings.Contains(string(log), "deleted: d.txt") || strings.Count(string(log), ": Old downloads deleted") != 1 {
		t.Fatalf("cleanup.log should hold one abort entry naming every deletion:\n%s", log)
	}
	if exists(filepath.Join(dl, "a.txt")) {
		t.Fatal("a.txt survived")
	}
}

// TestDownloadsResultTakenLogsNoAbort: a result the engine took without
// running its commit (refused, or gated behind a confirm) is released with
// steps.ErrResultTaken, which is no abort: no ABORT record.
func TestDownloadsResultTakenLogsNoAbort(t *testing.T) {
	run, release, _ := startDownloadsDelete(t)
	run()
	release(steps.ErrResultTaken)
	waitForDownloads(io.Discard)
	if log, _ := os.ReadFile(logPath(logCategoryCleanup)); strings.Contains(string(log), "ABORT") {
		t.Fatalf("a taken result logged an abort:\n%s", log)
	}
}

// TestDownloadsAbortLogsWhereItStarted: the abort record is written off the
// model's goroutine (by the step, or by its context.AfterFunc), so it goes to
// the store captured when the step was set up, never to one looked up from
// logDir there. The test keeps moving logDir while the step runs; under
// -race a lookup off the model's goroutine fails it, without -race the
// entry lands in the wrong place.
func TestDownloadsAbortLogsWhereItStarted(t *testing.T) {
	t.Run("mid-deletion", func(t *testing.T) {
		run, abort, _ := startDownloadsDelete(t)
		want := logPath(logCategoryCleanup)
		reached, resume := make(chan struct{}), make(chan struct{})
		downloadsEntryHook = func(name string) {
			if name == "c.txt" {
				close(reached)
				<-resume
			}
		}
		t.Cleanup(func() { downloadsEntryHook = nil })
		done := make(chan steps.Result, 1)
		go func() { done <- run() }()
		<-reached
		abort(nil) // cancels the step's ctx: it logs as it returns

		orig, elsewhere := logDir, []string{t.TempDir(), t.TempDir()}
		close(resume)
		for i, finished := 0, false; !finished; i++ {
			logDir = elsewhere[i%2] // no syscalls here: they'd hide a racy read
			select {
			case <-done:
				finished = true
			default:
				runtime.Gosched()
			}
		}
		logDir = orig

		if log, _ := os.ReadFile(want); !strings.Contains(string(log), "ABORT: Old downloads deleted (aborted)") {
			t.Fatalf("%s lacks the abort record:\n%s", want, log)
		}
		for _, d := range elsewhere {
			if entries, _ := os.ReadDir(d); len(entries) != 0 {
				t.Fatalf("the abort record went to %s, a log dir set after the step started", d)
			}
		}
	})
	t.Run("after the run", func(t *testing.T) {
		run, abort, _ := startDownloadsDelete(t)
		want := logPath(logCategoryCleanup)
		run() // the whole delete runs; its AfterFunc is armed
		logDir = t.TempDir()
		abort(nil) // the AfterFunc writes the record
		waitForDownloads(io.Discard)
		if log, _ := os.ReadFile(want); !strings.Contains(string(log), "ABORT: Old downloads deleted (aborted)") {
			t.Fatalf("%s lacks the abort record:\n%s", want, log)
		}
		if entries, _ := os.ReadDir(logDir); len(entries) != 0 {
			t.Fatal("the abort record went to the log dir set after the step started")
		}
	})
}
