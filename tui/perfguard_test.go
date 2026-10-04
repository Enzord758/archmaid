package main

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
// capped tail removed — 20k lines cost ~5 s before, ~21 ms after) and the
// status line, which redraws every few seconds.
//
// Timings depend on the machine, so this is opt-in: it runs only with
// ARCHMAID_PERF_GUARD=1 (scripts/check.sh then runs it as its own step), and
// skips under -race or coverage, whose overhead isn't the code's. The
// baselines are the medians of `go test -run '^$' -bench
// 'StreamView|SystemStatus' -count=6` on the dev machine (Ryzen 7 7800X3D,
// 16 threads, go1.27.1); a benchmark fails only above 3× its baseline.
// StreamView20k is the one that catches the quadratic regression: it would
// be ~250× over. Re-measure and update the baselines when the code or the
// machine legitimately changes.
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
	}{
		{"StreamView5k", 5400 * time.Microsecond, BenchmarkStreamView5k},    // limit 16.2 ms
		{"StreamView20k", 20600 * time.Microsecond, BenchmarkStreamView20k}, // limit 61.8 ms
		{"SystemStatus", 28200 * time.Nanosecond, BenchmarkSystemStatus},    // limit 84.6 µs
	} {
		t.Run(bc.name, func(t *testing.T) {
			limit := factor * bc.baseline
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
				t.Fatalf("%v/op is over %d× the baseline %v (limit %v)", best, factor, bc.baseline, limit)
			}
		})
	}
}
