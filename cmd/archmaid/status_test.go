package main

import "testing"

// BenchmarkSystemStatus is the perf guard's handle on the status line; the
// formatting and no-fork tests live with the code in internal/sysstat.
func BenchmarkSystemStatus(b *testing.B) {
	for i := 0; i < b.N; i++ {
		systemStatus()
	}
}
