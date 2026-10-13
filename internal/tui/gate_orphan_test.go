package tui

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
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

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

	tg := newTUIEngineTarget(t, shimLog)
	if _, err := tg.Start("Full Cleanup"); err != nil { // awaits the prefetch
		t.Fatal(err)
	}
	if tg.d.fetches[steps.PrefetchAURAudit] == nil {
		t.Fatal("activating Full Cleanup should start the audit prefetch")
	}
	b, _ := os.ReadFile(shimLog)
	if log := strings.TrimSpace(string(b)); log != "pacman -Qqm --color never" {
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
	if got := string(b); got != "pacman -Qdtq --color never\npacman -Rs --print --color never -- liba libb\n" {
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
	if b, _ := os.ReadFile(shimLog); string(b) != "pacman -Qdtq --color never\n" {
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

// afterStep is a harmless step behind the orphan probe.
var afterStep = steps.Step{ID: "after", Title: "after", LogLabel: "after", Argv: []string{"true"}}

// TestOrphanProbeInsertsConfirmedRemoval: the probe's result puts the removal
// next in the queue and asks its confirm right away, defaulting to No;
// declining skips just the removal (logged as SKIP) and carries on.
func TestOrphanProbeInsertsConfirmedRemoval(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": pacmanRules("liba\nlibb", "liba\nlibb\nlibc", "")}, "true")
	q := startQueue(t, orphanProbeStep(), afterStep)

	m := q.m
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

	m = q.answer("n").m
	if m.queue[1].status != steps.Skipped || m.skipped != 1 || m.queue[2].status != steps.OK {
		t.Fatalf("declined removal: status=%d skipped=%d after=%d, want skipped and moved on", m.queue[1].status, m.skipped, m.queue[2].status)
	}
	data, _ := os.ReadFile(logPath(logCategoryOrphans))
	if !strings.Contains(string(data), "SKIP: Orphaned packages removed (declined)") {
		t.Fatalf("orphans log lacks the declined SKIP:\n%s", data)
	}
}

// TestOrphanProbeNoneInsertsNothing: no orphans → no confirm, no removal; the
// queue just moves on.
func TestOrphanProbeNoneInsertsNothing(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": pacmanRules("", "", "")}, "true")
	m := startQueue(t, orphanProbeStep(), afterStep).m
	if len(m.queue) != 2 || m.queue[0].status != steps.OK || m.queue[1].status != steps.OK || m.screen != screenSummary {
		t.Fatalf("queue=%d probe=%d after=%d screen=%d, want the next step run",
			len(m.queue), m.queue[0].status, m.queue[1].status, m.screen)
	}
}

// TestOrphanProbeFailureInsertsNothing: a failed probe fails its step (logged
// with pacman's reason) and the queue carries on without a removal.
func TestOrphanProbeFailureInsertsNothing(t *testing.T) {
	transcriptEnv(t, false)
	installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Script: "echo 'error: boom' >&2", Exit: 1}}}, "true")
	m := startQueue(t, orphanProbeStep(), afterStep).m
	if len(m.queue) != 2 || m.queue[0].status != steps.Failed || m.errs != 1 || m.queue[1].status != steps.OK {
		t.Fatalf("queue=%d probe=%d errs=%d after=%d, want a failed probe and no insert",
			len(m.queue), m.queue[0].status, m.errs, m.queue[1].status)
	}
	data, _ := os.ReadFile(logPath(logCategoryOrphans))
	if !strings.Contains(string(data), "FAIL: Orphaned packages checked") || !strings.Contains(string(data), "error: boom") {
		t.Fatalf("orphans log lacks the failure:\n%s", data)
	}
}

// TestAbortDuringOrphanProbe: an abort while the probe's pacman runs (the
// real Driver, from newDriver) kills it through the step's ctx, and its late
// result is dropped: nothing is inserted.
func TestAbortDuringOrphanProbe(t *testing.T) {
	transcriptEnv(t, false)
	shimLog := installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Block: true}}})
	probe := actions.Action{ID: "orphanq", Label: "Orphan queue", Steps: func(actions.Env) []steps.Step {
		return []steps.Step{orphanProbeStep(), afterStep}
	}}
	old := actions.Menu
	actions.Menu = append(slices.Clone(old), probe)
	t.Cleanup(func() { actions.Menu = old })

	d := newDriver(engine.New(engine.Options{Env: actionEnv()}), nil)
	t.Cleanup(d.Close)
	d.Send(engine.Start{Action: probe.ID})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if b, _ := os.ReadFile(shimLog); len(b) > 0 {
			break // pacman is running
		}
		if time.Now().After(deadline) {
			t.Fatal("the probe's pacman never started")
		}
	}
	d.Send(engine.Abort{})
	var inserted bool
	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-d.Events():
			switch ev := ev.(type) {
			case engine.StepInserted:
				inserted = true
			case engine.Finished:
				if ev.Outcome != engine.OutcomeAborted {
					t.Fatalf("finished %+v, want aborted", ev)
				}
				done = true
			}
		case <-timeout:
			t.Fatal("the probe kept running after the abort: ctx didn't kill pacman")
		}
	}
	if inserted {
		t.Fatal("the aborted probe's result inserted a step")
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
