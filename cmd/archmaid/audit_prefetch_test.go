package main

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/audit"
)

// ── the AUR audit, fetched behind the password prompt ─────────────────────────
//
// Full Cleanup and Run topgrade start their audit's lookup the moment they're
// chosen (auditFetch), so it runs while the password prompt is open, and the
// audit step awaits it instead of fetching again. Abandoning the action before
// or during the step cancels it.

// chooseAction activates the menu item labelled sub and resolves its sudo
// probe, landing on the password prompt (uncached), the gate or the queue.
// The returned Cmd is whatever the probe's Update returned.
func chooseAction(t *testing.T, sub string) (model, tea.Cmd) {
	t.Helper()
	m, probe := activateMenu(t, initialModel(), sub)
	if m.auditFetch == nil {
		t.Fatalf("%s should start the audit fetch when chosen", sub)
	}
	next, cmd := m.Update(probe().(sudoCachedMsg))
	return next.(model), cmd
}

// activateMenu presses enter on m's menu item labelled sub. An action that
// audits (Full Cleanup, Run topgrade) starts its auditFetch right here; it is
// reaped when the test ends (reapAuditFetch), whatever the test does with it.
// Activate menu actions through this, never with a bare Update(enter).
func activateMenu(t *testing.T, m model, sub string) (model, tea.Cmd) {
	t.Helper()
	m.cursor = menuIndex(t, sub)
	next, cmd := m.Update(key("enter"))
	m = next.(model)
	reapAuditFetch(t, m.auditFetch)
	return m, cmd
}

// reapAuditFetch cancels f when the test ends and waits for its goroutine to
// return. PATH and ARCHMAID_SHIM_LOG are process-wide and only read when the
// fetch execs pacman, so a fetch that outlives its test can run the *next*
// test's pacman shim and land a stray "pacman -Qqm" in that test's exec log.
// Registered after the test's shims, it runs before their PATH is restored.
// A nil f (an action that doesn't audit) is a no-op.
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

// awaitFetch waits for f to finish, failing if it takes longer than within.
func awaitFetch(t *testing.T, f *auditFetch, within time.Duration) {
	t.Helper()
	start := time.Now()
	select {
	case <-f.Done():
		if d := time.Since(start); d > within {
			t.Fatalf("audit fetch finished %s later, want within %s", d, within)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the audit fetch never finished")
	}
}

// TestFullCleanupFetchesAuditBehindPrompt: by the time the password prompt is
// up, the audit's lookup is already under way, and nothing is saved yet.
func TestFullCleanupFetchesAuditBehindPrompt(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, fullCleanupShims(false, "", ""))
	requests, _ := fakeAUR(t, map[string]aurInfo{"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer"), NumVotes: 9}})

	m, _ := chooseAction(t, "Full Cleanup")
	if m.screen != screenPassword {
		t.Fatalf("screen = %d, want the password prompt", m.screen)
	}
	awaitFetch(t, m.auditFetch, 5*time.Second)
	if res, err := m.auditFetch.Wait(context.Background()); err != nil || res.Total != 1 {
		t.Fatalf("fetch = %+v, %v", res, err)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d AUR requests while the prompt is open, want 1", n)
	}
	if _, state := loadBaseline(); state != audit.BaselineMissing {
		t.Fatal("the prefetch must not save the snapshot")
	}
	update(m, key("esc")) // leave the fetch cancelled
}

// TestFullCleanupHitsTheAUROnce: a whole Full Cleanup run, password and all,
// asks the AUR exactly once: the audit step uses the prefetched lookup.
func TestFullCleanupHitsTheAUROnce(t *testing.T) {
	transcriptEnv(t, false)
	shimLog := installShims(t, fullCleanupShims(false, "", ""))
	requests, _ := fakeAUR(t, map[string]aurInfo{"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer"), NumVotes: 9}})

	d := run(t, shimLog, "Full Cleanup",
		Answer{"password", "", "correct"},
		Answer{"confirm", "ENTIRE package cache", "no"},
	)
	if got := d.Transcript(); d.m.queue[0].status != steps.OK {
		t.Fatalf("audit step status=%d\n%s", d.m.queue[0].status, got)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("%d AUR requests for one Full Cleanup, want 1", n)
	}
	if d.m.auditFetch != nil {
		t.Fatal("the finished run should have dropped its audit fetch")
	}
}

// TestEscOnPasswordCancelsAuditFetch: backing out of the password prompt
// abandons the action, so the lookup is cancelled, not left to run on.
func TestEscOnPasswordCancelsAuditFetch(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, fullCleanupShims(false, "", ""))
	arrived := hangingAUR(t)
	base := runtime.NumGoroutine()

	m, _ := chooseAction(t, "Full Cleanup")
	if m.screen != screenPassword {
		t.Fatalf("screen = %d, want the password prompt", m.screen)
	}
	waitArrived(t, arrived)
	f := m.auditFetch

	m = update(m, key("esc"))
	if m.screen != screenMenu || m.auditFetch != nil {
		t.Fatalf("esc: screen=%d fetch=%v, want the menu and no fetch", m.screen, m.auditFetch)
	}
	awaitFetch(t, f, cancelSlack)
	if _, err := f.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch err = %v, want context.Canceled", err)
	}
	settleGoroutines(t, base, "esc on the password prompt")
}

// TestDeclinedGateCancelsAuditFetch: "no" on the cached-sudo gate cancels the
// lookup too (a dry run, so the probe reports cached without sudo).
func TestDeclinedGateCancelsAuditFetch(t *testing.T) {
	transcriptEnv(t, true)
	installShims(t, fullCleanupShims(true, "", ""))
	arrived := hangingAUR(t)

	m, _ := chooseAction(t, "Full Cleanup")
	if m.screen != screenConfirm || !m.gateActive {
		t.Fatalf("screen = %d, want the proceed gate", m.screen)
	}
	waitArrived(t, arrived)
	f := m.auditFetch

	m = update(m, key("n"))
	if m.screen != screenMenu || m.auditFetch != nil {
		t.Fatalf("declined gate: screen=%d fetch=%v", m.screen, m.auditFetch)
	}
	awaitFetch(t, f, cancelSlack)
	if _, err := f.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch err = %v, want context.Canceled", err)
	}
}

// TestAbortCancelsPrefetchedAudit: Run topgrade awaits the same prefetched
// lookup; aborting while it's still out cancels it, the step returns at once
// without a second request, and its late result is dropped.
func TestAbortCancelsPrefetchedAudit(t *testing.T) {
	transcriptEnv(t, true) // dry run: cached probe, and topgrade only reported
	installShims(t, Shims{"pacman": pacmanRules("", "", "victim")})
	arrived := hangingAUR(t)
	base := runtime.NumGoroutine()

	m, cmd := chooseAction(t, "Run topgrade")
	if m.screen != screenRunning || m.current != 0 || cmd == nil {
		t.Fatalf("screen=%d current=%d, want the audit step running", m.screen, m.current)
	}
	f := m.auditFetch
	result := make(chan stepResultMsg, 1)
	go func() { result <- cmd().(stepResultMsg) }()
	waitArrived(t, arrived)
	// A step that refetched instead of awaiting would reach the AUR within
	// this; TestFullCleanupHitsTheAUROnce is the deterministic guard.
	select {
	case <-arrived:
		t.Fatal("the audit step fetched again instead of awaiting the prefetch")
	case <-time.After(200 * time.Millisecond):
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	m = update(m, key("y"))
	if m.screen != screenMenu || m.auditFetch != nil || m.stepCancel != nil {
		t.Fatalf("abort: screen=%d fetch=%v", m.screen, m.auditFetch)
	}
	start := time.Now()
	select {
	case msg := <-result:
		if d := time.Since(start); d > cancelSlack {
			t.Fatalf("audit step returned %s after the abort, want within %s", d, cancelSlack)
		}
		if got := update(m, msg); got.queue[0].status != steps.Aborted || got.errs != 0 {
			t.Fatalf("late result applied: status=%d errs=%d", got.queue[0].status, got.errs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the audit step ran on after the abort")
	}
	awaitFetch(t, f, cancelSlack)
	if _, err := f.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch err = %v, want context.Canceled", err)
	}
	settleGoroutines(t, base, "an abort mid-audit")
}
