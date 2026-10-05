package tui

import (
	"os"
	"testing"
	"time"
)

// raceEnabled is set by perfguard_race_test.go in a -race build, whose
// slowdown would trip every threshold below.
var raceEnabled bool

// TestPerfGuard fails if the UI's hot paths get much slower: the streaming
// benchmarks (the O(n²) re-split of the output that the batched pump and the
// capped tail removed — 20k lines cost ~5 s before, ~21 ms after), the
// status line, which redraws every few seconds, and choosing Full Cleanup.
//
// Timings depend on the machine, so this is opt-in: it runs only with
// ARCHMAID_PERF_GUARD=1 (scripts/check.sh then runs it as its own step), and
// skips under -race or coverage, whose overhead isn't the code's. The
// baselines are the medians of `go test -run '^$' -bench
// 'StreamView|SystemStatus|ActivateFullCleanup|ViewRunning' -count=6` on the dev machine
// (Ryzen 7 7800X3D, 16 threads, go1.27.1); a benchmark fails only above 3×
// its baseline. StreamView20k is the one that catches the quadratic
// regression: it would be ~250× over. ActivateFullCleanup has a fixed 1 ms
// limit instead: what it guards against is pacman running on the UI thread
// again (the eager orphan query cost 130–200 ms), not a few microseconds.
// ViewRunning (one Running-screen frame, a full output tail) has a fixed
// 600 µs limit too: the frame is redrawn on every batch and spinner tick.
// SystemStatusCache100 reads a fixture cache of 100 packages, not the
// machine's: the walk's cost is linear in the cached files, so the real
// cache made its old baseline (28.2 µs, a just-cleaned cache) fail at 104
// packages with no code change. Its baseline was measured 2026-10-05 with
// a VM running on other cores: single-threaded and stable to ±1 % there.
// Re-measure and update the baselines when the code or the machine
// legitimately changes.
func TestPerfGuard(t *testing.T) {
	if os.Getenv("ARCHMAID_PERF_GUARD") != "1" {
		t.Skip("set ARCHMAID_PERF_GUARD=1 to run the perf guard")
	}
	if raceEnabled || testing.CoverMode() != "" {
		t.Skip("perf guard skipped under -race or coverage: their overhead isn't the code's")
	}
	const factor = 3
	for _, bc := range []struct {
		name     string
		baseline time.Duration // ns/op measured on the dev machine
		bench    func(*testing.B)
		limit    time.Duration // if set, the limit instead of factor × baseline
	}{
		{"StreamView5k", 5400 * time.Microsecond, BenchmarkStreamView5k, 0},                             // limit 16.2 ms
		{"StreamView20k", 20600 * time.Microsecond, BenchmarkStreamView20k, 0},                          // limit 61.8 ms
		{"SystemStatusCache100", 147300 * time.Nanosecond, BenchmarkSystemStatusCache100, 0},            // limit 441.9 µs; 637 allocs
		{"ActivateFullCleanup", 4900 * time.Nanosecond, BenchmarkActivateFullCleanup, time.Millisecond}, // limit 1 ms (fixed)
		{"ViewRunning", 154 * time.Microsecond, BenchmarkViewRunning, 600 * time.Microsecond},           // limit 600 µs (fixed); median of 6: 154 µs, 633 allocs
	} {
		t.Run(bc.name, func(t *testing.T) {
			limit := factor * bc.baseline
			if bc.limit != 0 {
				limit = bc.limit
			}
			// Best of up to three runs, so one noisy run (a busy machine)
			// doesn't fail the guard; a real regression is far over anyway.
			var best time.Duration
			for range 3 {
				r := testing.Benchmark(bc.bench)
				if r.N == 0 {
					t.Fatal("benchmark did not run")
				}
				if got := time.Duration(r.NsPerOp()); best == 0 || got < best {
					best = got
				}
				if best <= limit {
					break
				}
			}
			t.Logf("%v/op (baseline %v, limit %v)", best, bc.baseline, limit)
			if best > limit {
				t.Fatalf("%v/op is over the limit %v (baseline %v)", best, limit, bc.baseline)
			}
		})
	}
}
