package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ── sudo -A on every sudo step ────────────────────────────────────────────────
//
// A sudo step must exec `sudo -A <args>` so any needed password is read via
// SUDO_ASKPASS (a TUI popup), never the tty. Asserted with a PATH shim
// standing in for the real binary, so nothing privileged runs.

// runStep starts s's child as the Driver does (internal/driver's startStep:
// Deps.Priv's command line, through internal/proc), with the Priv main gives
// it, privileged().
func runStep(s steps.Step, ch chan<- proc.Msg, quit <-chan struct{}) *proc.Child {
	opts := proc.Options{DryRun: dryRun, WaitDelay: stepWaitDelay, KillGrace: sigkillGrace}
	return proc.New(opts).Start(privileged().Command(s.Argv, s.NeedsPriv), ch, quit)
}

// shimEchoArgs installs a fake `name` on PATH that prints its argv and exits 0.
func shimEchoArgs(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	// printf, not echo: echo would swallow a leading -n as its own flag.
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withAskpassWrapper wires askpass up for privileged() for the test's duration.
func withAskpassWrapper(t *testing.T, path string) {
	t.Helper()
	old := askpassWrapper
	askpassWrapper = path
	t.Cleanup(func() { askpassWrapper = old })
}

// TestSudoStepGetsDashA: a sudo step's argv is rewritten to `sudo -A <args>`.
func TestSudoStepGetsDashA(t *testing.T) {
	shimEchoArgs(t, "sudo")
	withAskpassWrapper(t, "/wrapper")

	ch := make(chan proc.Msg, 8)
	runStep(steps.Step{Argv: []string{"sudo", "paccache", "-r"}, NeedsPriv: true}, ch, nil)
	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("shimmed sudo should exit 0, got %v", err)
	}
	if len(lines) != 1 || lines[0] != "-A paccache -r" {
		t.Fatalf("sudo argv = %v, want [-A paccache -r]", lines)
	}
}

// TestSudoStepWithoutAskpassGetsDashN: with askpass unwired (no
// XDG_RUNTIME_DIR), -A would fall back to sudo.conf's `Path askpass` helper or
// fail; the step runs `sudo -n` instead — the primed ticket or a clean failure,
// never a prompt.
func TestSudoStepWithoutAskpassGetsDashN(t *testing.T) {
	shimEchoArgs(t, "sudo")
	withAskpassWrapper(t, "")

	ch := make(chan proc.Msg, 8)
	runStep(steps.Step{Argv: []string{"sudo", "paccache", "-r"}, NeedsPriv: true}, ch, nil)
	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("shimmed sudo should exit 0, got %v", err)
	}
	if len(lines) != 1 || lines[0] != "-n paccache -r" {
		t.Fatalf("sudo argv = %v, want [-n paccache -r]", lines)
	}
}

// TestNonSudoStepUnchanged: a non-sudo command keeps its argv untouched.
func TestNonSudoStepUnchanged(t *testing.T) {
	shimEchoArgs(t, "topgrade")

	ch := make(chan proc.Msg, 8)
	runStep(steps.Step{Argv: []string{"topgrade", "-y"}}, ch, nil)
	lines, err := drainStep(ch)
	if err != nil {
		t.Fatalf("shimmed topgrade should exit 0, got %v", err)
	}
	if len(lines) != 1 || lines[0] != "-y" {
		t.Fatalf("topgrade argv = %v, want [-y] (no -n injected)", lines)
	}
}

// TestTopgradeStepsNeedSudo: topgrade shells out to sudo itself, so both the
// standalone action and Full Cleanup's first step must be marked needsSudo to
// get priming + keep-alive.
func TestTopgradeStepsNeedSudo(t *testing.T) {
	if s := standaloneTopgrade(); !s.NeedsPriv {
		t.Fatal("standalone topgrade step must be needsSudo")
	}
	shimPacman(t, "")
	list := fullCleanupSteps()
	if i := findStep(list, "topgrade"); i == -1 || !list[i].NeedsPriv {
		t.Fatal("Full Cleanup's topgrade step must be needsSudo")
	}
}
