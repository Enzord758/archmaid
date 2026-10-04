package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
	tea "github.com/charmbracelet/bubbletea"
)

// ── proceed-gate (cached-sudo path) ───────────────────────────────────────────
//
// A steps-action can carry a gate string (only Full Cleanup does). When sudo is
// uncached the password prompt is the gate; when it's cached, the gate renders
// as a confirm popup over the menu before anything runs.

// menuIndex finds the menu row whose label contains sub.
func menuIndex(t *testing.T, sub string) int {
	t.Helper()
	for i, it := range actions.Menu {
		if strings.Contains(it.Label, sub) {
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
	shimPacman(t, "") // the audit prefetch runs pacman -Qqm: keep it off the real one
	m, cmd := activateMenu(t, initialModel(), "Full Cleanup")
	if cmd == nil {
		t.Fatal("activating Full Cleanup should return the sudo probe cmd")
	}
	// dryRun makes the probe report cached without shelling out.
	return update(m, cmd().(sudoCachedMsg))
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

// TestGateShownWithoutRoot: a gated queue that needs no root has no password
// prompt to stand in for its gate, so the gate is asked before anything
// runs (no current action is one; the engine does the same).
func TestGateShownWithoutRoot(t *testing.T) {
	m := initialModel()
	m.gate = "Proceed?"
	m.pending = []steps.Step{{ID: "a", Title: "a…", Argv: []string{"true"}}}
	if cmd := m.beginPending(); cmd != nil {
		t.Fatal("beginPending returned a cmd: something started before the gate")
	}
	if m.screen != screenConfirm || !m.gateActive || m.confirmMsg != "Proceed?" || m.bg != screenMenu {
		t.Fatalf("screen=%d gateActive=%v confirmMsg=%q bg=%d, want the gate over the menu",
			m.screen, m.gateActive, m.confirmMsg, m.bg)
	}
	if len(m.queue) != 0 {
		t.Fatalf("queued %d steps before the gate was answered", len(m.queue))
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
	m.pending = []steps.Step{{Title: "fix", LogLabel: "fix", Argv: []string{"sudo"}, NeedsPriv: true}}
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

	shimPacman(t, "") // choosing Run topgrade starts the audit's pacman -Qqm
	m := initialModel()
	m.gate = "stale gate from an earlier action"
	got, _ := activateMenu(t, m, "topgrade (system update)")
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
	m.pending = []steps.Step{{Title: "x", LogLabel: "x", Argv: []string{"sudo"}, NeedsPriv: true}}

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
// Full Cleanup looks for orphans in an in-process step after the update
// (findOrphans), which inserts the removal step behind itself when it finds
// any. Building the queue runs nothing; the tests shim pacman on PATH so no
// real package state is involved.

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
func findStep(list []steps.Step, sub string) int {
	for i, s := range list {
		if strings.Contains(s.Title, sub) {
			return i
		}
	}
	return -1
}

// TestFullCleanupAuditThenTopgrade: the AUR audit leads the queue, directly
// followed by topgrade (update first, then clean up the result), regardless of
// orphans.
func TestFullCleanupAuditThenTopgrade(t *testing.T) {
	shimPacman(t, "")
	list := fullCleanupSteps()
	if len(list) < 2 || list[0].Run == nil || list[0].Category != logCategoryAudit {
		t.Fatalf("first step = %+v, want the in-process AUR audit", list[0])
	}
	if !strings.Contains(list[1].Title, "topgrade") {
		t.Fatalf("second step = %+v, want topgrade (the audit must sit directly in front of it)", list[1])
	}
}

// TestOrphanProbeAfterUpdate: the orphan probe is an in-process step after
// topgrade and both cache steps, directly in front of the AUR cache step —
// where the removal used to sit — and the built queue holds no removal step.
func TestOrphanProbeAfterUpdate(t *testing.T) {
	installShims(t, Shims{"pacman": pacmanRules("liba\nlibb", "liba\nlibb", ""), "hyprpm": nil})
	list := fullCleanupSteps()
	probe := findStep(list, "Finding orphaned")
	if probe == -1 {
		t.Fatalf("no orphan probe in %d-step queue", len(list))
	}
	s := list[probe]
	if s.Run == nil || s.NeedsPriv || s.Confirm != "" || s.Category != logCategoryOrphans {
		t.Fatalf("probe = %+v, want an in-process, sudo-less, unconfirmed orphans step", s)
	}
	for _, before := range []string{"topgrade", "Cleaning package cache", "Deleting entire package cache"} {
		if i := findStep(list, before); i == -1 || i > probe {
			t.Fatalf("step %q at %d, want it before the probe at %d", before, i, probe)
		}
	}
	if i := findStep(list, "AUR cache"); i != probe+1 {
		t.Fatalf("AUR cache step at %d, want directly after the probe (%d)", i, probe+1)
	}
	if i := findStep(list, "Removing orphaned"); i != -1 {
		t.Fatalf("removal step at %d in the built queue: it must come from the probe", i)
	}
	if last := list[len(list)-1]; last.Command() != "hyprpm" {
		t.Fatalf("last step = %+v, want hyprpm", last)
	}
}

// TestFullCleanupStepsRunNothing: building the queue — what activate does on
// the UI thread — execs nothing; the orphan queries used to run here, freezing
// the UI for 130–200 ms.
func TestFullCleanupStepsRunNothing(t *testing.T) {
	shimLog := installShims(t, Shims{"pacman": pacmanRules("liba", "liba\nlibz", "foo"), "hyprpm": nil})
	fullCleanupSteps()
	if b, _ := os.ReadFile(shimLog); len(b) != 0 {
		t.Fatalf("building the Full Cleanup queue ran:\n%s", b)
	}
}

// TestActivateFullCleanupRunsNoPacman: choosing Full Cleanup runs no orphan
// query. The only pacman it starts is the audit's prefetch (-Qqm), on its own
// goroutine.
func TestActivateFullCleanupRunsNoPacman(t *testing.T) {
	transcriptEnv(t, false)
	shimLog := installShims(t, fullCleanupShims(false, "liba\nlibb", "liba\nlibb\nlibc"))
	aurClean(t)

	got, cmd := activateMenu(t, initialModel(), "Full Cleanup")
	if cmd == nil || len(got.pending) == 0 || got.auditFetch == nil {
		t.Fatal("activating Full Cleanup should queue its steps, start the audit prefetch and probe sudo")
	}
	awaitFetch(t, got.auditFetch, 5*time.Second)
	b, _ := os.ReadFile(shimLog)
	if log := strings.TrimSpace(string(b)); log != "pacman -Qqm" {
		t.Fatalf("activate ran:\n%s\nwant only the audit prefetch's pacman -Qqm", log)
	}
}

// TestFindOrphansInsertsRemoval: with orphans found, the probe's result
// inserts the removal with exactly those names, behind a confirm that shows
// the cascade total; the output names what goes.
func TestFindOrphansInsertsRemoval(t *testing.T) {
	shimLog := installShims(t, Shims{"pacman": pacmanRules("liba\nlibb", "liba-1\nlibb-1\nlibc-1\nlibd-1", "")})
	r := findOrphans(context.Background())
	if r.Err != nil || r.InsertNext == nil {
		t.Fatalf("result = %+v, want a removal step to insert", r)
	}
	s := *r.InsertNext
	if s.Command() != "sudo" || !s.NeedsPriv || !s.Optional || !s.AlwaysLog || s.Category != logCategoryOrphans {
		t.Fatalf("removal step = %+v, want an optional, always-logged sudo orphans step", s)
	}
	if got := strings.Join(s.Args(), " "); got != "pacman -Rns --noconfirm -- liba libb" {
		t.Fatalf("args = %q, want exactly the names the probe found", got)
	}
	want := "Remove 2 orphaned package(s) — 4 total once dependencies cascade?"
	if s.Confirm != want {
		t.Fatalf("confirm = %q, want %q", s.Confirm, want)
	}
	// The same lists as data, for a front end's dialog.
	if !slices.Equal(s.Items, []string{"liba", "libb"}) || !slices.Equal(s.Cascade, []string{"liba-1", "libb-1", "libc-1", "libd-1"}) {
		t.Fatalf("items %q, cascade %q", s.Items, s.Cascade)
	}
	for _, line := range []string{"2 orphaned package(s): liba libb", "pacman -Rns would remove 4 package(s):", "  libd-1"} {
		if !strings.Contains(r.Output, line) {
			t.Fatalf("output lacks %q:\n%s", line, r.Output)
		}
	}
	b, _ := os.ReadFile(shimLog)
	if got := string(b); got != "pacman -Qdtq\npacman -Rs --print -- liba libb\n" {
		t.Fatalf("probe ran:\n%s", got)
	}
}

// TestFindOrphansNoCascadeKeepsPlainCount: when the preview adds nothing, the
// confirm stays the plain count (no "N total" clause equal to the count).
func TestFindOrphansNoCascadeKeepsPlainCount(t *testing.T) {
	installShims(t, Shims{"pacman": pacmanRules("liba\nlibb", "liba-1\nlibb-1", "")})
	r := findOrphans(context.Background())
	if r.InsertNext == nil || r.InsertNext.Confirm != "Remove 2 orphaned package(s)?" {
		t.Fatalf("result = %+v, want the plain-count confirm", r)
	}
}

// TestFindOrphansNone: pacman's silent exit 1 means none — a clear line, no
// error, nothing inserted, and no preview run.
func TestFindOrphansNone(t *testing.T) {
	shimLog := installShims(t, Shims{"pacman": pacmanRules("", "", "")})
	r := findOrphans(context.Background())
	if r.Err != nil || r.InsertNext != nil || r.Output != "No orphaned packages." {
		t.Fatalf("result = %+v, want a clean 'none'", r)
	}
	if b, _ := os.ReadFile(shimLog); string(b) != "pacman -Qdtq\n" {
		t.Fatalf("probe ran:\n%s", b)
	}
}

// TestFindOrphansProbeFails: a failing query — the orphan list or the cascade
// preview — fails the step and inserts nothing: no removal without a preview.
func TestFindOrphansProbeFails(t *testing.T) {
	for name, rules := range map[string][]Rule{
		"query": {{Prefix: "-Qdtq", Script: "echo 'error: could not open database' >&2", Exit: 1}},
		"preview": {{Prefix: "-Qdtq", Out: "liba"},
			{Prefix: "-Rs --print", Script: "echo 'error: failed to prepare transaction' >&2", Exit: 1}},
		"query, silent exit 2": {{Prefix: "-Qdtq", Exit: 2}},
	} {
		t.Run(name, func(t *testing.T) {
			installShims(t, Shims{"pacman": rules})
			r := findOrphans(context.Background())
			if r.Err == nil || r.InsertNext != nil {
				t.Fatalf("result = %+v, want a failure with nothing to insert", r)
			}
			if !strings.HasPrefix(r.Output, "Couldn't ") {
				t.Fatalf("output = %q, want the reason", r.Output)
			}
		})
	}
}

// orphanProbeModel is a running queue of [probe, a following step] in a real
// run with the probe launched; it returns the model and the probe's Cmd.
func orphanProbeModel(t *testing.T) (model, tea.Cmd) {
	t.Helper()
	m := initialModel()
	m.pending = []steps.Step{orphanProbeStep(), {Title: "after", LogLabel: "after", Argv: []string{"true"}}}
	cmd := m.startQueue()
	if m.current != 0 || m.queue[0].status != steps.Running || cmd == nil {
		t.Fatalf("the probe should be running, got current=%d", m.current)
	}
	return m, cmd
}

// TestOrphanProbeInsertsConfirmedRemoval: the probe's result puts the removal
// next in the queue and asks its confirm right away, defaulting to No;
// declining skips just the removal (logged as SKIP) and carries on.
func TestOrphanProbeInsertsConfirmedRemoval(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": pacmanRules("liba\nlibb", "liba\nlibb\nlibc", "")})
	m, cmd := orphanProbeModel(t)

	m = update(m, cmd())
	if len(m.queue) != 3 || m.queue[0].status != steps.OK {
		t.Fatalf("queue = %d steps, probe status %d; want the removal inserted after an ok probe", len(m.queue), m.queue[0].status)
	}
	if m.queue[1].step.Title != "Removing orphaned packages…" || m.queue[2].step.Title != "after" {
		t.Fatalf("queue order = %q, %q, want the removal before the rest", m.queue[1].step.Title, m.queue[2].step.Title)
	}
	want := "Remove 2 orphaned package(s) — 3 total once dependencies cascade?"
	if m.screen != screenConfirm || m.confirmMsg != want || m.confirmYes {
		t.Fatalf("screen=%d confirm=%q yes=%v, want %q defaulting to No", m.screen, m.confirmMsg, m.confirmYes, want)
	}

	m = update(m, key("n"))
	if m.queue[1].status != steps.Skipped || m.skipped != 1 || m.current != 2 {
		t.Fatalf("declined removal: status=%d skipped=%d current=%d, want skipped and moved on", m.queue[1].status, m.skipped, m.current)
	}
	data, _ := os.ReadFile(logPath(logCategoryOrphans))
	if !strings.Contains(string(data), "SKIP: Orphaned packages removed (declined)") {
		t.Fatalf("orphans log lacks the declined SKIP:\n%s", data)
	}
	m.abortRunning() // stop the following step's child
}

// TestOrphanProbeNoneInsertsNothing: no orphans → no confirm, no removal; the
// queue just moves on.
func TestOrphanProbeNoneInsertsNothing(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": pacmanRules("", "", "")})
	m, cmd := orphanProbeModel(t)

	m = update(m, cmd())
	if len(m.queue) != 2 || m.queue[0].status != steps.OK || m.current != 1 ||
		m.queue[1].status != steps.Running || m.screen != screenRunning {
		t.Fatalf("queue=%d probe=%d current=%d screen=%d, want the next step running",
			len(m.queue), m.queue[0].status, m.current, m.screen)
	}
	m.abortRunning()
}

// TestOrphanProbeFailureInsertsNothing: a failed probe fails its step (logged
// with pacman's reason) and the queue carries on without a removal.
func TestOrphanProbeFailureInsertsNothing(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Script: "echo 'error: boom' >&2", Exit: 1}}})
	m, cmd := orphanProbeModel(t)

	m = update(m, cmd())
	if len(m.queue) != 2 || m.queue[0].status != steps.Failed || m.errs != 1 || m.current != 1 {
		t.Fatalf("queue=%d probe=%d errs=%d current=%d, want a failed probe and no insert",
			len(m.queue), m.queue[0].status, m.errs, m.current)
	}
	data, _ := os.ReadFile(logPath(logCategoryOrphans))
	if !strings.Contains(string(data), "FAIL: Orphaned packages checked") || !strings.Contains(string(data), "error: boom") {
		t.Fatalf("orphans log lacks the failure:\n%s", data)
	}
	m.abortRunning()
}

// TestAbortDuringOrphanProbe: an abort while the probe's pacman runs kills it
// (ctx), and the late result is dropped by stepSeq: nothing is inserted and
// no confirm pops up over the menu.
func TestAbortDuringOrphanProbe(t *testing.T) {
	transcriptEnv(t, false)
	shimLog := installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Block: true}}})
	m, cmd := orphanProbeModel(t)

	res := make(chan tea.Msg, 1)
	go func() { res <- cmd() }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if b, _ := os.ReadFile(shimLog); len(b) > 0 {
			break // pacman is running
		}
		if time.Now().After(deadline) {
			t.Fatal("the probe's pacman never started")
		}
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	m = update(m, key("y"))
	if m.screen != screenMenu {
		t.Fatalf("screen = %d after the abort, want the menu", m.screen)
	}
	var late tea.Msg
	select {
	case late = <-res:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe kept running after the abort: ctx didn't kill pacman")
	}
	if r := late.(stepResultMsg); r.Err == nil {
		t.Fatalf("aborted probe returned %+v, want an error", r.Result)
	}
	m = update(m, late)
	if len(m.queue) != 2 || m.screen != screenMenu {
		t.Fatalf("late result changed the model: queue=%d screen=%d", len(m.queue), m.screen)
	}
}

// TestTempCleanupDefersToTmpfiles: temp files are aged out by the system's
// tmpfiles.d rules (systemd-tmpfiles --clean), never by a hand-rolled
// find/rm over /tmp that ignores their exclusions for live sockets and lock
// dirs.
func TestTempCleanupDefersToTmpfiles(t *testing.T) {
	shimExclusivePath(t)

	list := fullCleanupSteps()
	i := findStep(list, "temp files")
	if i == -1 {
		t.Fatalf("no temp-file step in %d-step queue", len(list))
	}
	s := list[i]
	if s.Command() != "sudo" || strings.Join(s.Args(), " ") != "systemd-tmpfiles --clean" {
		t.Fatalf("temp step runs %s %v, want sudo systemd-tmpfiles --clean", s.Command(), s.Args())
	}
	if !s.NeedsPriv || !s.AlwaysLog {
		t.Fatalf("temp step needsSudo=%v alwaysLog=%v, want both", s.NeedsPriv, s.AlwaysLog)
	}
}
