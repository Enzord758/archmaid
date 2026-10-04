package tui

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// BenchmarkActivateFullCleanup is the cost of choosing Full Cleanup: the
// model's Update (which only Sends Start) and the Session's Start, which
// builds the queue on the Driver's loop and asks for the sudo probe and the
// audit prefetch as Effects, nothing more. Building the queue used to list
// the orphans synchronously (130–200 ms of pacman on the UI thread); the
// orphan probe made that lazy, and TestPerfGuard keeps it under 1 ms. PATH
// is empty, so nothing could run pacman or reach the AUR anyway.
func BenchmarkActivateFullCleanup(b *testing.B) {
	b.Setenv("PATH", b.TempDir())
	m := fixedModel()
	for i, a := range actions.Menu {
		if a.ID == actions.IDFullCleanup {
			m.cursor = i
		}
	}
	opts := engine.Options{DryRun: dryRun, Env: actionEnv()}
	for b.Loop() {
		next, _ := m.activate()
		_ = next.(model)
		if o := engine.New(opts).Start(actions.IDFullCleanup); len(o.Effects) == 0 && len(o.Events) == 0 {
			b.Fatal("Full Cleanup started nothing")
		}
	}
}
