package ui

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/realdriver"
	"github.com/Enzord758/archmaid/internal/engine"
)

// The second quit signal's forced exit (signals.go, exitNow), in plain Go:
// no display, a fake Driver, os.Exit stood in for.

// exitDriver is the Driver the first signal closed: its record is written
// (Aborted closed) and KillNow is counted.
type exitDriver struct{ killed atomic.Int32 }

func (d *exitDriver) Events() <-chan engine.Event { return nil }
func (d *exitDriver) Send(engine.Command) bool    { return false }
func (d *exitDriver) Close()                      {}
func (d *exitDriver) Aborted() <-chan struct{}    { return closedCh() }
func (d *exitDriver) KillNow()                    { d.killed.Add(1) }

// forcedExit runs exitNow for sig with d as the closing Driver and atExit,
// and returns the exit code it asked for.
func forcedExit(t *testing.T, d engine.Driver, atExit func(), sig os.Signal) int {
	t.Helper()
	code := -1
	s := &signals{closing: make(chan engine.Driver, 1), atExit: atExit, exit: func(c int) { code = c }}
	s.closing <- d
	s.exitNow(sig)
	return code
}

// TestForcedExitRemovesAskpass: two SIGHUPs (the terminal archmaid-gui ran
// from closing) exit without Run returning, so mainReal's deferred
// Wiring.Close never runs: the forced exit itself closes the askpass
// Server, so no socket or wrapper is left in the runtime dir, after it has
// killed what the run started.
func TestForcedExitRemovesAskpass(t *testing.T) {
	runtime := t.TempDir()
	wr := realdriver.Start(realdriver.Config{RuntimeDir: runtime, Exe: "/nonexistent/archmaid-gui"})
	defer wr.Close()
	if !wr.Askpass() {
		t.Fatal("no askpass Server")
	}
	left := func() []string {
		m, err := filepath.Glob(filepath.Join(runtime, "archmaid-askpass-*"))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if len(left()) != 1 {
		t.Fatalf("askpass dirs %q, want one", left())
	}
	d := &exitDriver{}
	if code := forcedExit(t, d, wr.CloseAskpass, syscall.SIGHUP); code != 128+int(syscall.SIGHUP) {
		t.Fatalf("exit code %d", code)
	}
	if d.killed.Load() != 1 {
		t.Error("the run's processes were not killed")
	}
	if m := left(); len(m) != 0 {
		t.Fatalf("left behind after the forced exit: %q", m)
	}
}

// TestForcedExitDoesNotWaitForCleanup: an atExit that hangs delays the
// forced exit by cleanupWait at most.
func TestForcedExitDoesNotWaitForCleanup(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	code := forcedExit(t, &exitDriver{}, func() { <-block }, syscall.SIGTERM)
	if code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code %d", code)
	}
	if took := time.Since(start); took > cleanupWait+2*time.Second {
		t.Fatalf("the forced exit took %v", took)
	}
}
