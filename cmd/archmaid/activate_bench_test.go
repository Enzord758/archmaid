package main

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
)

// BenchmarkActivateFullCleanup is the Update cost of choosing Full Cleanup:
// building its queue and starting the audit prefetch, nothing more. Building
// the queue used to list the orphans synchronously (130–200 ms of pacman on
// the UI thread); the orphan probe made that lazy, and TestPerfGuard keeps it
// under 1 ms. PATH is empty, so the prefetch, off the UI thread, fails fast
// instead of running pacman or reaching the AUR.
func BenchmarkActivateFullCleanup(b *testing.B) {
	b.Setenv("PATH", b.TempDir())
	m := initialModel()
	for i, a := range actions.Menu {
		if a.ID == actions.IDFullCleanup {
			m.cursor = i
		}
	}
	for b.Loop() {
		next, _ := m.activate()
		got := next.(model)
		if len(got.pending) == 0 {
			b.Fatal("Full Cleanup queued nothing")
		}
		got.dropAuditFetch()
	}
}
