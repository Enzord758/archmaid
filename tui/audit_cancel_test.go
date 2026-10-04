package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── the audit's one cancellable deadline ──────────────────────────────────────
//
// The audit runs in-process, so an abort can't signal it: it has to be
// cancelled. These tests hold the RPC open on a fake AUR that never answers
// and check that cancelling (or the overall deadline) ends the call at once
// and leaves nothing running behind it.

// cancelSlack is how soon after a cancel the audit must have returned.
const cancelSlack = 100 * time.Millisecond

// hangingAUR points aurRPCURL at a server that takes every request and never
// answers it. Each arrival is signalled on the returned channel; a handler
// returns only once its client has gone, so a leaked connection would leave a
// handler goroutine behind too.
func hangingAUR(t *testing.T) (arrived <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{}, 16)
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		// Read the form first: net/http only watches for the client hanging
		// up (cancelling r.Context) once the request body is consumed.
		r.ParseForm()
		ch <- struct{}{}
		<-r.Context().Done()
	})
	return ch
}

func waitArrived(t *testing.T, arrived <-chan struct{}) {
	t.Helper()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the audit's request never reached the fake AUR")
	}
}

// settleGoroutines waits briefly for the goroutine count to fall back to
// base, then fails if it hasn't.
func settleGoroutines(t *testing.T, base int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines: %d before, %d after %s (leaked %d)\n%s",
			base, n, what, n-base, buf[:runtime.Stack(buf, true)])
	}
}

// TestFetchAURInfoReturnsOnCancel: cancelling the ctx abandons a request the
// AUR is sitting on, at once rather than after a timeout.
func TestFetchAURInfoReturnsOnCancel(t *testing.T) {
	arrived := hangingAUR(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fetchAURInfo(ctx, []string{"x"})
		done <- err
	}()
	waitArrived(t, arrived)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if d := time.Since(start); d > cancelSlack {
			t.Fatalf("returned %s after the cancel, want within %s", d, cancelSlack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetchAURInfo ignored the cancel")
	}
}

// TestAuditDeadlineCoversEveryChunk: the deadline is one budget for the whole
// audit, not a fresh timeout per request — two chunks that each fit in it
// alone still fail together.
func TestAuditDeadlineCoversEveryChunk(t *testing.T) {
	auditEnv(t)
	var pkgs []string
	for i := range auditChunk + 1 { // two RPC chunks
		pkgs = append(pkgs, fmt.Sprintf("pkg%d", i))
	}
	shimPacmanForeign(t, strings.Join(pkgs, "\n")+"\n")
	var requests atomic.Int32
	serveAUR(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		r.ParseForm() // see hangingAUR
		// each answer alone takes 60% of the budget
		select {
		case <-time.After(150 * time.Millisecond):
			w.Write([]byte(`{"type":"multiinfo","results":[]}`))
		case <-r.Context().Done():
		}
	})
	old := auditDeadline
	auditDeadline = 250 * time.Millisecond
	t.Cleanup(func() { auditDeadline = old })

	start := time.Now()
	_, err := performAudit(context.Background(), auditNow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the overall deadline to have run out", err)
	}
	if d := time.Since(start); d > auditDeadline+cancelSlack {
		t.Fatalf("audit took %s, want it ended by the %s deadline", d, auditDeadline)
	}
	if n := requests.Load(); n != 2 {
		t.Fatalf("%d requests, want the second chunk to have been cut short", n)
	}
	if got := shortErr(err); got != "no answer within 250ms" {
		t.Fatalf("shortErr = %q", got)
	}
}

// TestAbortCancelsAuditStep: aborting while the audit step waits on the AUR
// ends the step's Cmd at once, its late result is dropped, and nothing it
// started (connection, handler, pacman) is left running.
func TestAbortCancelsAuditStep(t *testing.T) {
	auditEnv(t)
	shimPacmanForeign(t, "victim\n")
	arrived := hangingAUR(t)
	base := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		m := initialModel()
		m.pending = []step{auditStep(), harmlessTopgrade()}
		cmd := m.startQueue()
		result := make(chan stepResultMsg, 1)
		go func() { result <- cmd().(stepResultMsg) }()
		waitArrived(t, arrived)

		start := time.Now()
		m.abortRunning()
		if m.stepCancel != nil {
			t.Fatal("abort must clear stepCancel")
		}
		select {
		case msg := <-result:
			if d := time.Since(start); d > cancelSlack {
				t.Fatalf("audit step returned %s after the abort, want within %s", d, cancelSlack)
			}
			got := update(m, msg)
			if got.screen != screenMenu || got.queue[0].status != statusRunning || got.errs != 0 {
				t.Fatalf("late audit result applied: screen=%d status=%d errs=%d",
					got.screen, got.queue[0].status, got.errs)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the audit step ran on after the abort")
		}
	}
	settleGoroutines(t, base, "3 aborted audits")
}
