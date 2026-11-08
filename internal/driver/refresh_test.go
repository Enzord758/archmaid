package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/installed"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The Driver calls Deps.RefreshUpdates once for a real run whose topgrade
// step started (completed, failed), and never for a dry run or an action
// without topgrade.
func TestRefreshUpdatesPerRun(t *testing.T) {
	for _, c := range []struct {
		name string
		opts engine.Options
		step steps.Step
		want int32
	}{
		{"completed", engine.Options{}, shStep(steps.IDTopgrade, "true"), 1},
		{"failed", engine.Options{}, shStep(steps.IDTopgrade, "exit 3"), 1},
		{"dry run", engine.Options{DryRun: true}, shStep(steps.IDTopgrade, "true"), 0},
		{"no topgrade", engine.Options{}, shStep("other", "true"), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var n atomic.Int32
			r := newRig(t, c.opts, Deps{RefreshUpdates: func(context.Context) error { n.Add(1); return nil }},
				action("q", c.step))
			r.run("q", yes)
			waitFor(t, "the refresh", func() bool { return n.Load() >= c.want })
			time.Sleep(50 * time.Millisecond) // a second call would land by now
			if got := n.Load(); got != c.want {
				t.Errorf("RefreshUpdates called %d times, want %d", got, c.want)
			}
		})
	}
}

// A refresh that errors is one WARN line in the run's topgrade log: not a
// Report (the TUI prints those on the terminal after it quits), not the
// run's failure.
func TestRefreshUpdatesErrorIsALogLine(t *testing.T) {
	r := newRig(t, engine.Options{}, Deps{RefreshUpdates: func(context.Context) error { return os.ErrNotExist }},
		action("q", shStep(steps.IDTopgrade, "true")))
	evs := r.run("q", yes)
	if fin := find[engine.Finished](t, evs); fin.Errs != 0 {
		t.Errorf("Finished.Errs = %d, want 0", fin.Errs)
	}
	waitFor(t, "the log line", func() bool {
		s, _ := r.log.Read(logstore.Topgrade)
		return strings.Contains(s, "WARN: Widget update count not refreshed: file does not exist")
	})
	r.d.Close() // the worker is done once Close returns
	if s := r.reports.text(); s != "" {
		t.Errorf("the failed refresh was reported: %q", s)
	}
}

// Where systemctl isn't installed and neither is the check (the TUI over
// ssh on a machine without the package's user unit, or CI): the real
// RefreshUpdates leaves exactly one WARN line naming why, and nothing the
// user sees.
func TestRefreshUpdatesNoSystemctlIsQuiet(t *testing.T) {
	bin := t.TempDir()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sh, filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin) // sh for the step, no systemctl
	old := installed.UpdatesScript
	installed.UpdatesScript = filepath.Join(t.TempDir(), "archmaid-updates-check") // not there
	t.Cleanup(func() { installed.UpdatesScript = old })
	r := newRig(t, engine.Options{}, Deps{RefreshUpdates: RefreshUpdates}, action("q", shStep(steps.IDTopgrade, "true")))
	evs := r.run("q", yes)
	if fin := find[engine.Finished](t, evs); fin.Errs != 0 || fin.Outcome != engine.OutcomeCompleted {
		t.Errorf("Finished = %+v, want a completed run", fin)
	}
	r.d.Close()
	if s := r.reports.text(); s != "" {
		t.Errorf("reported: %q", s)
	}
	s, _ := r.log.Read(logstore.Topgrade)
	if strings.Count(s, "\n") != 1 || !strings.Contains(s, "WARN: Widget update count not refreshed: driver: updates check not started: systemctl:") ||
		!strings.Contains(s, "executable file not found") {
		t.Errorf("topgrade log = %q, want one WARN line saying systemctl isn't there", s)
	}
}

// A WARN line that can't be written is reported, with why the refresh
// failed.
func TestRefreshUpdatesUnloggableIsReported(t *testing.T) {
	r := newRig(t, engine.Options{}, Deps{
		Log:            logstore.New(logstore.Options{Dir: "relative"}), // no state dir: every write fails
		RefreshUpdates: func(context.Context) error { return os.ErrNotExist },
	}, action("q", shStep(steps.IDTopgrade, "true")))
	r.run("q", yes)
	waitFor(t, "the report", func() bool {
		return strings.Contains(r.reports.text(), "driver: writing the topgrade log") &&
			strings.Contains(r.reports.text(), "the update count refresh failed: file does not exist")
	})
}

// shims puts a systemctl that runs body first on PATH, logging its argv to
// the returned file; the unit's script is script ("" none), logging "ran".
func shims(t *testing.T, systemctl, script string) (sysLog, scriptLog string) {
	t.Helper()
	dir := t.TempDir()
	sysLog, scriptLog = filepath.Join(dir, "sys.log"), filepath.Join(dir, "script.log")
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "systemctl"), `echo "$*" >> `+sysLog+"\n"+systemctl)
	old := installed.UpdatesScript
	installed.UpdatesScript = filepath.Join(dir, "missing-check")
	if script != "" {
		installed.UpdatesScript = filepath.Join(dir, "check")
		write(installed.UpdatesScript, `echo ran >> `+scriptLog+"\n"+script)
	}
	t.Cleanup(func() { installed.UpdatesScript = old })
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return sysLog, scriptLog
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestRefreshUpdatesStartsTheUnitNoBlock(t *testing.T) {
	sysLog, scriptLog := shims(t, "exit 0", "")
	if err := RefreshUpdates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(sysLog); got != "--user restart --no-block archmaid-updates-check.service\n" {
		t.Errorf("systemctl got %q", got)
	}
	if readFile(scriptLog) != "" {
		t.Error("the script ran although systemctl worked")
	}
}

func TestRefreshUpdatesFallsBackToTheScript(t *testing.T) {
	sysLog, scriptLog := shims(t, "echo no such unit >&2\nexit 5", "exit 0")
	if err := RefreshUpdates(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the script", func() bool { return readFile(scriptLog) == "ran\n" })
	if strings.Count(readFile(sysLog), "\n") != 1 {
		t.Errorf("systemctl called more than once: %q", readFile(sysLog))
	}
}

func TestRefreshUpdatesNoUnitNoScriptSaysSo(t *testing.T) {
	shims(t, "echo no such unit >&2\nexit 5", "")
	err := RefreshUpdates(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not started") {
		t.Errorf("err = %v, want one line saying the check was not started", err)
	}
}

// A systemctl that hangs does not hang RefreshUpdates: the Driver's context
// ends it (and the 5 s timeout bounds it without one).
func TestRefreshUpdatesHangingSystemctlIsBounded(t *testing.T) {
	shims(t, "exec sleep 30", "")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RefreshUpdates(ctx)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("RefreshUpdates took %v with a hung systemctl", d)
	}
	if err == nil {
		t.Error("want an error for the killed systemctl")
	}
}

// The Driver's Close ends a refresh still waiting on a hung systemctl.
func TestRefreshUpdatesCloseEndsAHungOne(t *testing.T) {
	shims(t, "exec sleep 30", "")
	r := newRig(t, engine.Options{}, Deps{RefreshUpdates: RefreshUpdates}, action("q", shStep(steps.IDTopgrade, "true")))
	r.run("q", yes)
	start := time.Now()
	r.d.Close()
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("Close took %v with a hung systemctl", d)
	}
}
