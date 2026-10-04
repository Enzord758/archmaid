package audit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// ── the audit's one cancellable deadline, and the prefetch ────────────────────
//
// These hold the RPC open on a fake AUR that never answers and check that a
// cancel (or the overall deadline) ends the audit at once and leaves nothing
// running behind it.

// cancelSlack is how soon after a cancel the audit must have returned.
const cancelSlack = 100 * time.Millisecond

// hangingAUR is an AUR that takes every request and never answers it. Each
// arrival is signalled on the returned channel; a handler returns only once
// its client has gone, so a leaked connection would leave a handler goroutine
// behind too.
func hangingAUR(t *testing.T) (rpcURL string, arrived <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{}, 16)
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// Read the form first: net/http only watches for the client hanging
		// up (cancelling r.Context) once the request body is consumed.
		r.ParseForm()
		ch <- struct{}{}
		<-r.Context().Done()
	})
	return url, ch
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

// returnsWithin waits for done, failing unless it closes within cancelSlack
// of start.
func returnsWithin(t *testing.T, done <-chan struct{}, start time.Time, what string) {
	t.Helper()
	select {
	case <-done:
		if d := time.Since(start); d > cancelSlack {
			t.Fatalf("%s returned %s after the cancel, want within %s", what, d, cancelSlack)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s ignored the cancel", what)
	}
}

// TestFetchReturnsOnCancel: cancelling the ctx abandons a request the AUR is
// sitting on, at once rather than after a timeout.
func TestFetchReturnsOnCancel(t *testing.T) {
	url, arrived := hangingAUR(t)
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		_, err = fetch(ctx, url, []string{"x"})
		close(done)
	}()
	waitArrived(t, arrived)

	start := time.Now()
	cancel()
	returnsWithin(t, done, start, "fetch")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	settleGoroutines(t, base, "a cancelled fetch")
}

// TestRunDeadlineCoversEveryChunk: the deadline is one budget for the whole
// audit, not a fresh timeout per request — two chunks that each fit in it
// alone still fail together.
func TestRunDeadlineCoversEveryChunk(t *testing.T) {
	var pkgs []string
	for i := range chunk + 1 { // two RPC chunks
		pkgs = append(pkgs, fmt.Sprintf("pkg%d", i))
	}
	var requests atomic.Int32
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		r.ParseForm() // see hangingAUR
		// each answer alone takes 60% of the budget
		select {
		case <-time.After(150 * time.Millisecond):
			w.Write([]byte(`{"type":"multiinfo","results":[]}`))
		case <-r.Context().Done():
		}
	})
	cfg, _ := config(t, url, pkgs...)
	cfg.Deadline = 250 * time.Millisecond

	start := time.Now()
	_, err := Run(context.Background(), cfg, now)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the overall deadline to have run out", err)
	}
	if d := time.Since(start); d > cfg.Deadline+cancelSlack {
		t.Fatalf("audit took %s, want it ended by the %s deadline", d, cfg.Deadline)
	}
	if n := requests.Load(); n != 2 {
		t.Fatalf("%d requests, want the second chunk to have been cut short", n)
	}
	if got := ShortErr(err, cfg.Deadline); got != "no answer within 250ms" {
		t.Fatalf("ShortErr = %q", got)
	}
}

// TestRunDeadlineCoversTheForeignQuery: the lister runs under the same
// deadline, and a list cut short by it proves nothing: the audit fails with
// the deadline, not ErrNoForeign.
func TestRunDeadlineCoversTheForeignQuery(t *testing.T) {
	url, reqs, _ := fakeAUR(t, nil)
	cfg, _ := config(t, url)
	cfg.Deadline = 50 * time.Millisecond
	cfg.Foreign = func(ctx context.Context) ([]string, error) {
		<-ctx.Done()
		return nil, nil // like pacman killed by the context: no output
	}
	if _, err := Run(context.Background(), cfg, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if reqs.Load() != 0 {
		t.Fatal("a cut-short audit must not reach the AUR")
	}
}

// TestPrefetchIsRun: a prefetch's result is Run's, and waiting on it again
// returns the same; Done is closed by then.
func TestPrefetchIsRun(t *testing.T) {
	url, reqs, _ := fakeAUR(t, map[string]Info{"p": {Name: "p", Maintainer: strp("alice"), LastModified: aged(24 * 90), NumVotes: 3}})
	cfg, _ := config(t, url, "p")
	f := Prefetch(cfg)
	res, err := f.Wait(context.Background())
	if err != nil || res.Total != 1 || res.Baseline != BaselineMissing {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	select {
	case <-f.Done():
	default:
		t.Fatal("Done must be closed once Wait has returned the result")
	}
	if again, err := f.Wait(context.Background()); err != nil || again.Total != 1 {
		t.Fatalf("second Wait: %+v %v", again, err)
	}
	if n := reqs.Load(); n != 1 {
		t.Fatalf("%d AUR requests, want 1", n)
	}
	// Its commit writes the snapshot the config named.
	if err := res.Commit(false, false)(); err != nil {
		t.Fatal(err)
	}
	if _, state := LoadSnapshot(cfg.SnapshotPath); state != BaselineOK {
		t.Fatal("the prefetch's commit should have written the configured snapshot")
	}
	f.Cancel() // after the fact: harmless
}

// TestPrefetchCancel: Cancel ends a lookup the AUR is sitting on at once,
// with context.Canceled, and nothing is left running.
func TestPrefetchCancel(t *testing.T) {
	url, arrived := hangingAUR(t)
	base := runtime.NumGoroutine()
	cfg, _ := config(t, url, "p")
	f := Prefetch(cfg)
	waitArrived(t, arrived)

	start := time.Now()
	f.Cancel()
	returnsWithin(t, f.Done(), start, "the prefetch")
	if _, err := f.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	settleGoroutines(t, base, "a cancelled prefetch")
}

// TestPrefetchWaitAbort: a Wait whose ctx ends (an abort) returns at once
// with ctx's error, and cancels the lookup too.
func TestPrefetchWaitAbort(t *testing.T) {
	url, arrived := hangingAUR(t)
	base := runtime.NumGoroutine()
	cfg, _ := config(t, url, "p")
	f := Prefetch(cfg)
	waitArrived(t, arrived)

	ctx, abort := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		_, err = f.Wait(ctx)
		close(done)
	}()
	start := time.Now()
	abort()
	returnsWithin(t, done, start, "Wait")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}
	returnsWithin(t, f.Done(), start, "the prefetch")
	settleGoroutines(t, base, "an aborted wait")
}
