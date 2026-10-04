package audit

import (
	"context"
	"sync/atomic"
	"time"
)

// Fetch is an audit started ahead of the point that needs it: Run on its own
// goroutine, its result published by closing Done. The TUI starts one the
// moment Full Cleanup or Run topgrade is chosen, so the lookup runs while the
// password prompt is open, and its audit step Waits for it instead of
// fetching again (one RPC per run). It only reads: the snapshot commit is
// still the Result's, so nothing is saved before the gate is answered.
type Fetch struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool // Cancel ran: the fetch is abandoned, whatever Run returned
	cfg       Config
	done      chan struct{} // closed once res and err are set
	res       Result
	err       error
}

// Prefetch starts Run(cfg) now, on its own goroutine, with the clock read as
// it starts. cfg is read by the caller, before the goroutine starts.
func Prefetch(cfg Config) *Fetch {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Fetch{cancel: cancel, cfg: cfg, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		f.res, f.err = Run(ctx, f.cfg, time.Now())
	}()
	return f
}

// Done is closed once the audit has finished (or given up after Cancel).
func (f *Fetch) Done() <-chan struct{} { return f.done }

// Cancel abandons the audit: it returns promptly with context.Canceled.
// Safe to call more than once, and after it finished: once cancelled, Wait
// never hands out the result, even one that completed first, so an abandoned
// fetch can't be committed.
func (f *Fetch) Cancel() {
	f.cancelled.Store(true)
	f.cancel()
}

// Wait returns the audit's result once it's done. If ctx ends first (an
// abort), it cancels the audit and returns at once with ctx's error and a
// Result that carries only the Config. After Cancel it returns
// context.Canceled and such a Result too. Neither has a commit.
func (f *Fetch) Wait(ctx context.Context) (Result, error) {
	select {
	case <-f.done:
		if f.cancelled.Load() {
			return Result{cfg: f.cfg}, context.Canceled
		}
		return f.res, f.err
	case <-ctx.Done():
		f.Cancel()
		return Result{cfg: f.cfg}, ctx.Err()
	}
}
