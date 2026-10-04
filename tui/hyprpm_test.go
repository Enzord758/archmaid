package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hyprpm drops the sudo ticket on exit (NSys::root::dropSudo → sudo -k), so
// Full Cleanup must exclude it from topgrade and run it as the LAST step —
// after everything that needs sudo. These tests pin that arrangement.

// shimExclusivePath points PATH at exactly one temp dir containing stub
// executables for the given names — so LookPath sees only what the test
// provides (a stub pacman is always included, so nothing reaches the real one).
func shimExclusivePath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	stub := "#!/bin/sh\nexit 0\n"
	pacman := "#!/bin/sh\nif [ \"$1\" = \"-Qdtq\" ]; then :; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(pacman), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// TestFullCleanupDisablesHyprpmInTopgrade: the topgrade step must carry
// --disable hyprpm so hyprpm can't kill the ticket mid-queue.
func TestFullCleanupDisablesHyprpmInTopgrade(t *testing.T) {
	shimExclusivePath(t, "hyprpm")

	steps := fullCleanupSteps()
	i := findStep(steps, "topgrade")
	if i == -1 {
		t.Fatalf("no topgrade step in %d-step queue", len(steps))
	}
	tg := steps[i]
	args := strings.Join(tg.args, " ")
	if !strings.Contains(args, "--disable hyprpm") {
		t.Fatalf("topgrade args = %q, want --disable hyprpm", args)
	}
}

// TestHyprpmStepLastWhenInstalled: with hyprpm on PATH, a dedicated hyprpm
// step exists and is the FINAL step of the queue (nothing sudo after it).
func TestHyprpmStepLastWhenInstalled(t *testing.T) {
	shimExclusivePath(t, "hyprpm")

	steps := fullCleanupSteps()
	last := steps[len(steps)-1]
	if last.command != "hyprpm" || len(last.args) != 1 || last.args[0] != "update" {
		t.Fatalf("last step = %+v, want hyprpm update", last)
	}
	if last.needsSudo {
		t.Fatal("hyprpm step must not be needsSudo (it elevates internally)")
	}
}

// TestHyprpmStepOmittedWhenNotInstalled: without hyprpm on PATH the step is
// absent and the queue ends with the integrity check.
func TestHyprpmStepOmittedWhenNotInstalled(t *testing.T) {
	shimExclusivePath(t) // pacman only — no hyprpm

	steps := fullCleanupSteps()
	for _, s := range steps {
		if s.command == "hyprpm" {
			t.Fatalf("hyprpm step present despite hyprpm not installed: %+v", s)
		}
	}
	last := steps[len(steps)-1]
	if !strings.Contains(last.title, "integrity") {
		t.Fatalf("last step = %+v, want the integrity check", last)
	}
}

// TestStandaloneTopgradeKeepsHyprpm: the standalone Run-topgrade action is
// deliberately unmodified — nothing runs after it, so hyprpm inside topgrade
// is harmless there.
func TestStandaloneTopgradeKeepsHyprpm(t *testing.T) {
	tg := standaloneTopgrade()
	if strings.Contains(strings.Join(tg.args, " "), "--disable") {
		t.Fatalf("standalone topgrade args = %v, want no --disable", tg.args)
	}
}
