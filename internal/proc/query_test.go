package proc

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestQuery: stdout is the result, on success and on failure alike; a
// failure keeps the *exec.ExitError (exit code and stderr) for callers that
// read meaning into an exit status, and its text leads with the stderr.
func TestQuery(t *testing.T) {
	ctx := context.Background()
	if out, err := Query(ctx, "sh", "-c", "echo hi; echo noise >&2"); err != nil || string(out) != "hi\n" {
		t.Fatalf("success: got %q, %v; want stdout only", out, err)
	}

	out, err := Query(ctx, "sh", "-c", "echo partial; echo nope >&2; exit 1")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || string(exit.Stderr) != "nope\n" {
		t.Fatalf("failure: err %v, want the ExitError with code 1 and stderr", err)
	}
	if err.Error() != "nope: exit status 1" || string(out) != "partial\n" {
		t.Fatalf("failure: got %q, %q; want the stdout and a stderr-led error", out, err)
	}

	// A silent failure is the bare ExitError (pacman -Qdtq's "none").
	out, err = Query(ctx, "sh", "-c", "exit 1")
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(exit.Stderr) != 0 || len(out) != 0 || err.Error() != "exit status 1" {
		t.Fatalf("silent failure: got %q, %v", out, err)
	}

	if _, err := Query(ctx, "archmaid-no-such-command"); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing command: err %v, want ErrNotFound", err)
	}
}

// TestQueryCancelled: cancelling ctx kills the command.
func TestQueryCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	began := time.Now()
	if _, err := Query(ctx, "sleep", "30"); err == nil {
		t.Fatal("a cancelled query reported success")
	}
	if elapsed := time.Since(began); elapsed > 5*time.Second {
		t.Fatalf("cancelled query took %v", elapsed)
	}
}
