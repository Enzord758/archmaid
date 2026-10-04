package main

import (
	"runtime"
	"testing"
	"time"
)

// TestAbortDoesNotLeakGoroutines: aborting a step that is still producing
// output must let every goroutine behind it finish. Before the fix, nothing
// read m.stream after an abort, so once its buffer filled the scanner blocked
// on the send, the pipe copier blocked on the scanner and cmd.Wait on the
// copier: three goroutines (plus the pending pump) leaked per aborted step.
func TestAbortDoesNotLeakGoroutines(t *testing.T) {
	old := sigkillGrace
	sigkillGrace = 10 * time.Millisecond
	defer func() { sigkillGrace = old }()

	base := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		m := fixedModel()
		m.queue = []stepState{{step: step{title: "spam", logLabel: "spam", command: "yes"}}}
		m.current = -1
		m.screen = screenRunning
		pump := m.advance() // launches `yes`, which floods the stream

		// Take one line through Update like the runtime would, leaving the
		// next pump outstanding, exactly as it is when ctrl+c arrives.
		next, pump := m.Update(pump())
		m = next.(model)
		go pump()

		// Abort only once the stream buffer is full, so the child really is
		// blocked behind it (the case that leaked).
		for len(m.stream) < cap(m.stream) {
			time.Sleep(time.Millisecond)
		}
		m.abortRunning()
	}

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines: %d before, %d after 5 aborted steps — leaked %d", base, n, n-base)
	}
}

// TestAbortStepWithBackgroundChild: aborting a step whose child left a
// background process holding the output pipe still lets every goroutine
// behind the step finish — SIGINT ends the child, and stepWaitDelay ends the
// wait on the pipe the background process keeps.
func TestAbortStepWithBackgroundChild(t *testing.T) {
	oldGrace, oldDelay := sigkillGrace, stepWaitDelay
	sigkillGrace, stepWaitDelay = 10*time.Millisecond, 100*time.Millisecond
	defer func() { sigkillGrace, stepWaitDelay = oldGrace, oldDelay }()

	base := runtime.NumGoroutine()
	m := fixedModel()
	// exec: SIGINT reaches sleep itself, whose default action is to die.
	m.queue = []stepState{{step: step{title: "bg", logLabel: "bg", command: "bash",
		args: []string{"-c", `sleep 30 & echo "holder $!"; exec sleep 30`}}}}
	m.current = -1
	m.screen = screenRunning
	m.advance()
	if m.proc == nil {
		t.Fatal("step did not start")
	}
	select {
	case msg := <-m.stream:
		killHolder(t, msg.line)
	case <-time.After(5 * time.Second):
		t.Fatal("no output from the step")
	}

	m.abortRunning()
	waitGoroutines(t, base, 3*time.Second)
}
