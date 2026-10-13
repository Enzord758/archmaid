package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// drain reads ch to its done, failing t if that takes longer than 5 s, and
// returns the lines (each Msg's Replace applied: the final lines) and the
// done's error.
func drain(t *testing.T, ch <-chan Msg) ([]string, error) {
	t.Helper()
	var lines []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if msg.Done {
				return lines, msg.Err
			}
			if msg.Replace > len(lines) {
				t.Fatalf("Replace %d of %d lines", msg.Replace, len(lines))
			}
			lines = append(lines[:len(lines)-msg.Replace], msg.Line)
		case <-timeout:
			t.Fatalf("no done after 5s (lines so far %q)", lines)
		}
	}
}

// requireNothingAfterDone fails t if ch carries anything more.
func requireNothingAfterDone(t *testing.T, ch <-chan Msg) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("message after the done: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

// killHolder SIGKILLs the background process a test command reported with a
// "holder <pid>" line, so a test never leaves its `sleep 30` behind.
func killHolder(t *testing.T, line string) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "holder "))
	if err != nil || pid <= 0 {
		t.Fatalf("no holder pid in %q", line)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

// waitGoroutines fails t unless the goroutine count falls back to base.
func waitGoroutines(t *testing.T, base int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines: %d before, %d after — leaked %d", base, n, n-base)
	}
}

func bash(script string) Spec { return Spec{Name: "bash", Args: []string{"-c", script}} }

// TestStartStreamsInOrderThenOneDone: Start emits the output lines in order,
// stdout and stderr through one pipe, followed by exactly one done.
func TestStartStreamsInOrderThenOneDone(t *testing.T) {
	ch := make(chan Msg, 16)
	if c := New(Options{}).Start(bash(`printf 'one\ntwo\n'; echo three >&2`), ch, nil); c == nil {
		t.Fatal("did not start")
	}
	lines, err := drain(t, ch)
	if err != nil {
		t.Fatalf("command should succeed, got %v", err)
	}
	if strings.Join(lines, ",") != "one,two,three" {
		t.Fatalf("lines = %q, want one, two, three", lines)
	}
	requireNothingAfterDone(t, ch)
}

// TestStartFailureKeepsExitError: a failing command's output still arrives,
// and its done carries the *exec.ExitError with the exit code.
func TestStartFailureKeepsExitError(t *testing.T) {
	ch := make(chan Msg, 16)
	New(Options{}).Start(bash("echo boom; exit 3"), ch, nil)
	lines, err := drain(t, ch)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("done err = %v, want exit status 3", err)
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Fatalf("captured output = %q, want [boom]", lines)
	}
}

// TestStartNotFound: a command that can't start returns no Child, and its
// error arrives as the done.
func TestStartNotFound(t *testing.T) {
	ch := make(chan Msg, 1)
	if c := New(Options{}).Start(Spec{Name: "archmaid-no-such-command"}, ch, nil); c != nil {
		t.Fatalf("got a child %+v for a missing command", c)
	}
	if lines, err := drain(t, ch); !errors.Is(err, exec.ErrNotFound) || len(lines) != 0 {
		t.Fatalf("got %q, %v; want a bare done with ErrNotFound", lines, err)
	}
}

// TestStartChild: the Child names the started process, with the start time
// /proc gives it, and the child gets exactly Spec.Env.
func TestStartChild(t *testing.T) {
	ch := make(chan Msg, 4)
	quit := make(chan struct{})
	defer close(quit)
	c := New(Options{KillGrace: time.Second}).Start(
		Spec{Name: "sh", Args: []string{"-c", `echo "$ARCHMAID_PROC_TEST"; exec sleep 30`}, Env: []string{"ARCHMAID_PROC_TEST=env-ok"}},
		ch, quit)
	if c == nil {
		t.Fatal("did not start")
	}
	defer c.Stop()
	if msg := <-ch; msg.Line != "env-ok" {
		t.Fatalf("child saw %q, want Spec.Env", msg.Line)
	}
	info, err := Read(c.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if c.Start == 0 || c.Start != info.Start || info.PPid != os.Getpid() {
		t.Fatalf("child %+v, /proc says %+v", c, info)
	}
}

// TestDryRunDoesNotExecute is the safety-critical test: in a dry run nothing
// starts; the command is reported as one line, then a clean done.
func TestDryRunDoesNotExecute(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	ch := make(chan Msg, 8)
	c := New(Options{DryRun: true}).Start(Spec{Name: "touch", Args: []string{marker}, Display: "touch the marker"}, ch, nil)
	if c != nil {
		t.Fatalf("dry run returned a child %+v", c)
	}
	lines, err := drain(t, ch)
	if err != nil {
		t.Fatalf("dry-run step should not error, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("dry run executed the command: %s was created", marker)
	}
	if len(lines) != 1 || lines[0] != "[dry-run] would run: touch the marker" {
		t.Fatalf("lines = %q, want the one would-run line with Display", lines)
	}
	requireNothingAfterDone(t, ch)

	// Without a Display, the name and arguments are reported.
	New(Options{DryRun: true}).Start(Spec{Name: "paccache", Args: []string{"-r"}}, ch, nil)
	if lines, _ := drain(t, ch); len(lines) != 1 || lines[0] != DryRunPrefix+"paccache -r" {
		t.Fatalf("lines = %q, want the command line", lines)
	}
}

// TestDryRunReleasedByQuit: a dry run's report gives up once quit closes.
func TestDryRunReleasedByQuit(t *testing.T) {
	base := runtime.NumGoroutine()
	quit := make(chan struct{})
	New(Options{DryRun: true}).Start(Spec{Name: "true"}, make(chan Msg), quit)
	close(quit)
	waitGoroutines(t, base, 2*time.Second)
}

// TestStartNotHeldByBackgroundChild: a command that starts something in the
// background that keeps the output pipe (a daemon, a helper topgrade leaves
// running) is done once the child exits — within WaitDelay, not when the
// background process lets go. Every line the child wrote comes before exactly
// one done, which carries the child's own exit status (a clean exit is a
// success, not exec's ErrWaitDelay), and nothing is left running behind it.
func TestStartNotHeldByBackgroundChild(t *testing.T) {
	const waitDelay = 200 * time.Millisecond
	for _, tc := range []struct {
		exit int
		want func(error) bool
	}{
		{0, func(err error) bool { return err == nil }},
		{3, func(err error) bool {
			var ee *exec.ExitError
			return errors.As(err, &ee) && ee.ExitCode() == 3
		}},
	} {
		t.Run(fmt.Sprintf("exit%d", tc.exit), func(t *testing.T) {
			base := runtime.NumGoroutine()
			ch := make(chan Msg, 16)
			script := fmt.Sprintf(`echo one; sleep 30 & echo "holder $!"; echo two; exit %d`, tc.exit)
			began := time.Now()
			if c := New(Options{WaitDelay: waitDelay}).Start(bash(script), ch, nil); c == nil {
				t.Fatal("command did not start")
			}

			var lines []string
			var done Msg
			timeout := time.After(5 * time.Second)
		read:
			for {
				select {
				case msg := <-ch:
					if msg.Done {
						done = msg
						break read
					}
					lines = append(lines, msg.Line)
					if strings.HasPrefix(msg.Line, "holder ") {
						killHolder(t, msg.Line)
					}
				case <-timeout:
					t.Fatalf("no done after 5s (lines so far %q): the background sleep held the step open", lines)
				}
			}
			elapsed := time.Since(began)

			if len(lines) != 3 || lines[0] != "one" || !strings.HasPrefix(lines[1], "holder ") || lines[2] != "two" {
				t.Fatalf("lines = %q, want one, holder <pid>, two", lines)
			}
			if !tc.want(done.Err) {
				t.Fatalf("done err = %v, want the child's own exit %d", done.Err, tc.exit)
			}
			// The sleep really held the pipe (else this proves nothing), and
			// only for about WaitDelay.
			if elapsed < waitDelay || elapsed > waitDelay+2*time.Second {
				t.Fatalf("done after %v, want about WaitDelay (%v)", elapsed, waitDelay)
			}
			requireNothingAfterDone(t, ch)
			waitGoroutines(t, base, 2*time.Second)
		})
	}
}
