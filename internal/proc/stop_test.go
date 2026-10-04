package proc

import (
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// signalled reports whether err is a child's exit by signal sig.
func signalled(err error, sig syscall.Signal) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == sig
}

// TestStopSendsSIGINT: Stop asks with SIGINT first, which a well-behaved child
// dies of long before the grace runs out.
func TestStopSendsSIGINT(t *testing.T) {
	ch := make(chan Msg, 1)
	c := New(Options{KillGrace: time.Minute}).Start(Spec{Name: "sleep", Args: []string{"30"}}, ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	c.Stop()
	if _, err := drain(t, ch); !signalled(err, syscall.SIGINT) {
		t.Fatalf("done err = %v, want death by SIGINT", err)
	}
}

// TestStopEscalatesToSigkill: a child that ignores SIGINT is SIGKILLed after
// the grace period.
func TestStopEscalatesToSigkill(t *testing.T) {
	const grace = 200 * time.Millisecond
	ch := make(chan Msg, 1)
	// An ignored SIGINT survives exec, so the sleep ignores it too. The armed
	// line says the trap is set, so the SIGINT can't land before it.
	c := New(Options{KillGrace: grace}).Start(bash(`trap '' INT; echo armed; exec sleep 30`), ch, nil)
	if c == nil {
		t.Fatal("did not start")
	}
	if msg := <-ch; msg.Line != "armed" {
		t.Fatalf("got %#v, want the armed line", msg)
	}
	began := time.Now()
	c.Stop()
	_, err := drain(t, ch)
	if !signalled(err, syscall.SIGKILL) {
		t.Fatalf("done err = %v, want death by SIGKILL", err)
	}
	if elapsed := time.Since(began); elapsed < grace {
		t.Fatalf("killed after %v, before the %v grace", elapsed, grace)
	}
}

// TestStopNilSafe: stopping no child — a dry run's, one that never started,
// or a handle with no process behind it — is a no-op.
func TestStopNilSafe(t *testing.T) {
	var c *Child
	c.Stop()
	(&Child{Pid: 5_000_100}).Stop()
}

// TestStopDoesNotLeakGoroutines: stopping a child that is still producing
// output, with quit closed as an abort does, lets every goroutine behind it
// finish. Before quit existed nothing read the channel after an abort, so
// once its buffer filled the scanner blocked on the send, the pipe copier
// blocked on the scanner and cmd.Wait on the copier: three goroutines (plus
// the pending Next) leaked per aborted step.
func TestStopDoesNotLeakGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()
	for range 5 {
		ch, quit := make(chan Msg, BatchMax), make(chan struct{})
		c := New(Options{KillGrace: 10 * time.Millisecond}).Start(Spec{Name: "yes"}, ch, quit)
		if c == nil {
			t.Fatal("did not start")
		}
		// A pump waiting for the next batch, exactly as one is when ctrl+c
		// arrives.
		if _, ok := Next(ch, quit, 1); !ok {
			t.Fatal("no first batch")
		}
		go Next(ch, quit, BatchMax)

		// Stop only once the buffer is full, so the child really is blocked
		// behind it (the case that leaked).
		for len(ch) < cap(ch) {
			time.Sleep(time.Millisecond)
		}
		close(quit)
		c.Stop()
	}
	waitGoroutines(t, base, 5*time.Second)
}

// TestStopWithBackgroundChild: stopping a child that left a background
// process holding the output pipe still lets every goroutine behind it
// finish — SIGINT ends the child, and WaitDelay ends the wait on the pipe the
// background process keeps.
func TestStopWithBackgroundChild(t *testing.T) {
	base := runtime.NumGoroutine()
	ch, quit := make(chan Msg, BatchMax), make(chan struct{})
	// exec: SIGINT reaches sleep itself, whose default action is to die.
	c := New(Options{WaitDelay: 100 * time.Millisecond, KillGrace: 10 * time.Millisecond}).Start(
		bash(`sleep 30 & echo "holder $!"; exec sleep 30`), ch, quit)
	if c == nil {
		t.Fatal("did not start")
	}
	select {
	case msg := <-ch:
		killHolder(t, msg.Line)
	case <-time.After(5 * time.Second):
		t.Fatal("no output from the child")
	}

	close(quit)
	c.Stop()
	waitGoroutines(t, base, 3*time.Second)
}
