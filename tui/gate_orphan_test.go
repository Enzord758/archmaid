package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── proceed-gate (cached-sudo path) ───────────────────────────────────────────
//
// A steps-action can carry a gate string (only Full Cleanup does). When sudo is
// uncached the password prompt is the gate; when it's cached, the gate renders
// as a confirm popup over the menu before anything runs.

// menuIndex finds the menu row whose label contains sub.
func menuIndex(t *testing.T, sub string) int {
	t.Helper()
	for i, it := range menu {
		if strings.Contains(it.label, sub) {
			return i
		}
	}
	t.Fatalf("no menu item labelled %q", sub)
	return -1
}

// gateArmedModel activates Full Cleanup from the menu (arming its gate) and
// resolves the dry-run sudo probe, landing on the gate confirm.
func gateArmedModel(t *testing.T) model {
	t.Helper()
	shimPacman(t, "") // keep the orphan query hermetic (and off the real pacman)
	m := initialModel()
	m.cursor = menuIndex(t, "Full Cleanup")

	next, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("activating Full Cleanup should return the sudo probe cmd")
	}
	// dryRun makes the probe report cached without shelling out.
	return update(next.(model), cmd().(sudoCachedMsg))
}

// TestGateShownWhenSudoCached: cached sudo + a gated action → the proceed-gate
// confirm appears over the menu, defaulting to No, with no queue built yet.
func TestGateShownWhenSudoCached(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	got := gateArmedModel(t)
	if got.screen != screenConfirm || !got.gateActive {
		t.Fatalf("expected gate confirm (screenConfirm, gateActive), got screen=%d gateActive=%v",
			got.screen, got.gateActive)
	}
	if got.confirmMsg != "Proceed with full cleanup?" {
		t.Fatalf("confirmMsg = %q, want the Full Cleanup gate text", got.confirmMsg)
	}
	if got.confirmYes {
		t.Fatal("gate should default to No")
	}
	if got.bg != screenMenu {
		t.Fatalf("gate should float over the menu, bg=%d", got.bg)
	}
	if len(got.queue) != 0 {
		t.Fatalf("no queue may exist before the gate is answered, got %d steps", len(got.queue))
	}
}

// TestGateNoReturnsToMenu: declining the gate goes back to the menu and runs
// nothing.
func TestGateNoReturnsToMenu(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	got := update(gateArmedModel(t), key("n"))
	if got.screen != screenMenu {
		t.Fatalf("gate 'no' should return to the menu, got screen=%d", got.screen)
	}
	if got.gateActive {
		t.Fatal("gateActive should be cleared after resolving")
	}
	if len(got.queue) != 0 {
		t.Fatalf("nothing may run after a declined gate, got %d queued steps", len(got.queue))
	}
}

// TestGateYesStartsQueue: accepting the gate starts the queue.
func TestGateYesStartsQueue(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	armed := gateArmedModel(t)
	pending := len(armed.pending)
	got := update(armed, key("y"))
	if got.screen != screenRunning {
		t.Fatalf("gate 'yes' should start the queue, got screen=%d", got.screen)
	}
	if got.gateActive {
		t.Fatal("gateActive should be cleared after resolving")
	}
	if len(got.queue) != pending {
		t.Fatalf("queue has %d steps, want the %d pending ones", len(got.queue), pending)
	}
}

// TestNoGateForUngatedAction: cached sudo + an action without a gate runs
// straight to the queue — the gate confirm is Full Cleanup–only.
func TestNoGateForUngatedAction(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	m.pending = []step{{title: "fix", logLabel: "fix", command: "sudo", needsSudo: true}}
	got := update(m, sudoCachedMsg{cached: true})
	if got.screen != screenRunning {
		t.Fatalf("ungated action should run immediately, got screen=%d", got.screen)
	}
	if got.gateActive {
		t.Fatal("gateActive must stay false without a gate")
	}
}

// TestStaleGateClearedByActivate: activate resets m.gate before dispatch, so a
// leftover gate from a previous action never gates a different one.
func TestStaleGateClearedByActivate(t *testing.T) {
	dryRun = true
	defer func() { dryRun = false }()

	m := initialModel()
	m.gate = "stale gate from an earlier action"
	m.cursor = menuIndex(t, "topgrade (system update)")

	next, _ := m.Update(key("enter"))
	got := next.(model)
	if got.gate != "" {
		t.Fatalf("activate should clear a stale gate, got %q", got.gate)
	}
	// topgrade primes sudo (needsSudo) — under dry-run the probe reports cached,
	// so completing it runs the queue with no gate confirm.
	got = update(got, sudoCachedMsg{cached: true})
	if got.screen != screenRunning {
		t.Fatalf("ungated action should run after the sudo probe, got screen=%d", got.screen)
	}
}

// TestGateUncachedShowsPasswordNotGate: with sudo uncached the password prompt
// itself is the gate — no extra confirm even for a gated action.
func TestGateUncachedShowsPasswordNotGate(t *testing.T) {
	m := initialModel()
	m.gate = "Proceed with full cleanup?"
	m.pending = []step{{title: "x", logLabel: "x", command: "sudo", needsSudo: true}}

	got := update(m, sudoCachedMsg{cached: false})
	if got.screen != screenPassword {
		t.Fatalf("uncached sudo should show the password prompt, got screen=%d", got.screen)
	}
	if got.gateActive {
		t.Fatal("the gate confirm must not activate on the uncached path")
	}
}

// ── orphan removal in Full Cleanup ────────────────────────────────────────────
//
// fullCleanupSteps queries pacman -Qdtq at build time; the tests shim pacman on
// PATH so no real package state is involved.

// shimPacman installs a fake pacman on PATH whose -Qdtq prints the given lines.
func shimPacman(t *testing.T, qdtqOutput string) {
	t.Helper()
	dir := t.TempDir()
	// Embed the payload as printf escapes so the shim emits real newlines.
	payload := strings.ReplaceAll(qdtqOutput, "\n", "\\n")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"-Qdtq\" ]; then printf '%s'; fi\n", payload)
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// findStep returns the index of the step whose title contains sub, or -1.
func findStep(steps []step, sub string) int {
	for i, s := range steps {
		if strings.Contains(s.title, sub) {
			return i
		}
	}
	return -1
}

// TestFullCleanupTopgradeFirst: topgrade leads the queue (update first, then
// clean up the result) regardless of orphans.
func TestFullCleanupTopgradeFirst(t *testing.T) {
	shimPacman(t, "")
	steps := fullCleanupSteps()
	if len(steps) == 0 || !strings.Contains(steps[0].title, "topgrade") {
		t.Fatalf("first step = %+v, want topgrade", steps[0])
	}
}

// TestOrphanStepSplicedIn: with orphans reported, an optional sudo step is
// inserted carrying the real package list and count.
func TestOrphanStepSplicedIn(t *testing.T) {
	shimPacman(t, "liba\nlibb\n")

	steps := fullCleanupSteps()
	i := findStep(steps, "orphaned")
	if i == -1 {
		t.Fatalf("no orphan step in %d-step queue", len(steps))
	}
	s := steps[i]
	if s.command != "sudo" || !s.needsSudo || !s.optional {
		t.Fatalf("orphan step should be optional sudo, got %+v", s)
	}
	joined := strings.Join(s.args, " ")
	if !strings.HasPrefix(joined, "pacman -Rns --noconfirm ") {
		t.Fatalf("args = %v, want pacman -Rns --noconfirm …", s.args)
	}
	if !strings.HasSuffix(joined, "liba libb") {
		t.Fatalf("package list not spliced in, args = %v", s.args)
	}
	if s.confirm != "Remove 2 orphaned package(s)?" {
		t.Fatalf("confirm = %q, want the real count", s.confirm)
	}
}

// TestOrphanStepOmittedWhenNone: with no orphans the step is left out entirely
// and the queue is one shorter.
func TestOrphanStepOmittedWhenNone(t *testing.T) {
	shimPacman(t, "")
	without := fullCleanupSteps()
	if i := findStep(without, "orphaned"); i != -1 {
		t.Fatalf("orphan step present at %d despite no orphans", i)
	}

	shimPacman(t, "liba\n")
	with := fullCleanupSteps()
	if len(with) != len(without)+1 {
		t.Fatalf("queue sizes: with=%d without=%d, want exactly one more with orphans",
			len(with), len(without))
	}
}

// shimPacmanCascade extends shimPacman with an -Rs --print response, so
// orphanRemovalTotal's cascade preview can be driven independently of -Qdtq.
func shimPacmanCascade(t *testing.T, qdtqOutput, rsPrintOutput string) {
	t.Helper()
	dir := t.TempDir()
	qPayload := strings.ReplaceAll(qdtqOutput, "\n", "\\n")
	rPayload := strings.ReplaceAll(rsPrintOutput, "\n", "\\n")
	script := fmt.Sprintf("#!/bin/sh\n"+
		"if [ \"$1\" = \"-Qdtq\" ]; then printf '%s'; fi\n"+
		"if [ \"$1\" = \"-Rs\" ]; then printf '%s'; fi\n", qPayload, rPayload)
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestOrphanStepShowsCascadeTotal: when -Rns's recursive dependency cleanup
// (-s) would remove more than the direct orphans, the confirm discloses the
// real total instead of just the count the user is used to skimming past.
func TestOrphanStepShowsCascadeTotal(t *testing.T) {
	shimPacmanCascade(t, "liba\nlibb\n", "liba-1\nlibb-1\nlibc-1\nlibd-1\n")

	steps := fullCleanupSteps()
	i := findStep(steps, "orphaned")
	if i == -1 {
		t.Fatalf("no orphan step in %d-step queue", len(steps))
	}
	want := "Remove 2 orphaned package(s) — 4 total once dependencies cascade?"
	if got := steps[i].confirm; got != want {
		t.Fatalf("confirm = %q, want %q", got, want)
	}
}

// TestOrphanStepNoCascadeKeepsPlainCount: when the cascade preview reports no
// extra packages, the confirm stays the plain count (no confusing "N total"
// clause when N equals the direct count).
func TestOrphanStepNoCascadeKeepsPlainCount(t *testing.T) {
	shimPacmanCascade(t, "liba\nlibb\n", "liba-1\nlibb-1\n")

	steps := fullCleanupSteps()
	i := findStep(steps, "orphaned")
	if i == -1 {
		t.Fatalf("no orphan step in %d-step queue", len(steps))
	}
	want := "Remove 2 orphaned package(s)?"
	if got := steps[i].confirm; got != want {
		t.Fatalf("confirm = %q, want %q", got, want)
	}
}

// TestOrphanRemovalTotalFallsBackOnProbeFailure: if the --print preview
// itself can't run, the count silently falls back to the direct orphan list
// rather than corrupting the confirm text.
func TestOrphanRemovalTotalFallsBackOnProbeFailure(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := orphanRemovalTotal([]string{"liba", "libb"}); got != 2 {
		t.Fatalf("orphanRemovalTotal = %d, want 2 (fallback to len(orphans))", got)
	}
}
