package tui

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// ── the AUR audit, fetched behind the password prompt ─────────────────────────
//
// Full Cleanup and Run topgrade start their audit's lookup the moment they're
// chosen (the engine's StartPrefetch, carried out by the Driver's Prefetch:
// prefetch in adapter.go), so it runs while the password prompt is open, and
// the audit step awaits it instead of fetching again. When the engine asks
// for it and drops it (an abandoned action, a consumed result) is tested in
// internal/engine; starting and cancelling it in internal/driver.

// reapAuditFetch cancels f when the test ends and waits for its goroutine to
// return. PATH and ARCHMAID_SHIM_LOG are process-wide and only read when the
// fetch execs pacman, so a fetch that outlives its test can run the *next*
// test's pacman shim and land a stray "pacman -Qqm" in that test's exec log.
// Registered after the test's shims, it runs before their PATH is restored.
// A nil f is a no-op.
func reapAuditFetch(t *testing.T, f *auditFetch) {
	t.Helper()
	if f == nil {
		return
	}
	t.Cleanup(func() {
		f.Cancel()
		select {
		case <-f.Done():
		case <-time.After(5 * time.Second):
			t.Error("the audit fetch outlived its test: it ignored the cancel")
		}
	})
}

// TestFullCleanupHitsTheAUROnce: a whole Full Cleanup run, password and all,
// asks the AUR exactly once: the audit step uses the prefetched lookup.
func TestFullCleanupHitsTheAUROnce(t *testing.T) {
	transcriptEnv(t, false)
	shimLog := installShims(t, fullCleanupShims(false, "", ""))
	requests, _ := fakeAUR(t, map[string]aurInfo{"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer"), NumVotes: 9}})

	d := run(t, shimLog, "Full Cleanup",
		Answer{"password", "", "correct"},
	)
	if got := d.Transcript(); d.m.queue[0].status != steps.OK {
		t.Fatalf("audit step status=%d\n%s", d.m.queue[0].status, got)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d AUR requests for one Full Cleanup, want 1", n)
	}
	if len(d.d.fetches) != 0 {
		t.Fatal("the finished run should have dropped its audit fetch")
	}
}
