package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// stepState tracks a step's live status for the checklist view.
type stepState struct {
	step   steps.Step
	status steps.Status
}

// ── streaming ────────────────────────────────────────────────────────────────
//
// The whole reason to leave gum: a running command's output streams into the
// UI live instead of being swallowed by a spinner and dumped in a pager after.
//
// internal/proc runs the child and puts every output line on one ordered
// channel, then a single done with the exit error (see proc.Runner.Start).
// The pump (waitForStream) hands the UI whatever has piled up on the channel
// as one streamBatchMsg rather than one Update per line: a fast producer
// (topgrade, pacman -Qk) emits thousands of lines, and a re-render per line
// made the UI's cost grow with the square of the output. Each batch is
// stamped with the launch it belongs to (seq, from model.stepSeq), so
// handleStream can drop one from an aborted step even after a new run has
// started.

// streamBatchMsg is what the pump delivers: a proc.Batch stamped with the
// launch it belongs to.
type streamBatchMsg struct {
	seq   int      // the launch this batch belongs to
	lines []string // raw output lines, in order (may be empty when done)
	done  bool     // the step's done message ended the batch
	err   error    // the step's exit error (only meaningful when done)
}

// stepWaitDelay is how long a step's output pipe is still read after its
// child has exited (proc.Options.WaitDelay): something the child left running
// in the background can't keep the finished step "running". A var so tests
// can shrink it.
var stepWaitDelay = 2 * time.Second

// procOptions is how this run starts its children, read from the package's
// knobs on the caller's goroutine (a test may swap them between runs).
func procOptions() proc.Options {
	return proc.Options{DryRun: dryRun, WaitDelay: stepWaitDelay, KillGrace: sigkillGrace}
}

// runStep starts the step's command and streams its output on ch (see
// proc.Runner.Start), returning the running child so the caller can stop it
// on abort (nil in a dry run or if the command never starts). Closing quit
// releases its goroutines once nobody reads ch; nil never fires.
func runStep(s steps.Step, ch chan<- proc.Msg, quit <-chan struct{}) *proc.Child {
	return proc.New(procOptions()).Start(stepSpec(s), ch, quit)
}

// stepSpec is the command a step runs: sudo gets -A or -n, and only
// NeedsPriv steps see SUDO_ASKPASS (privilege.Sudo.Command). A dry run
// reports the step's own command line, without the sudo flag.
func stepSpec(s steps.Step) proc.Spec {
	return privileged().Command(s.Argv, s.NeedsPriv)
}

// waitForStream is the pump: proc.Next as a tea.Cmd, its batch stamped with
// seq, the launch that owns ch. Re-issue it after every batch without a done
// to keep pumping the channel through Update. A closed quit (abort) releases
// a pump that is still waiting, with no msg — but a pump that finds a line
// ready as quit closes may still deliver it, after the abort and possibly
// after a new launch: seq is what lets handleStream tell it apart.
func waitForStream(ch <-chan proc.Msg, quit <-chan struct{}, seq int) tea.Cmd {
	return func() tea.Msg {
		b, ok := proc.Next(ch, quit, proc.BatchMax)
		if !ok {
			return nil
		}
		return streamBatchMsg{seq: seq, lines: b.Lines, done: b.Done, err: b.Err}
	}
}

// ── sudo ─────────────────────────────────────────────────────────────────────
//
// Rather than dropping out of the alt-screen, we authenticate inside the TUI:
// a masked textinput collects the password and pipes it to `sudo -S -v`, which
// caches the credential timestamp so the queue's later `sudo` calls run without
// prompting. The password is never logged and the input is reset after use.

type sudoCachedMsg struct{ cached bool } // result of the non-interactive probe
// sudoResultMsg is the result of an auth attempt. detail carries a message
// worth showing verbatim (e.g. an account-lockout notice) instead of the
// generic wrong-password error.
type sudoResultMsg struct {
	err    error
	detail string
}

// checkSudoCached probes whether sudo credentials are already valid, so we can
// skip the password prompt entirely when they are (e.g. re-running within the
// timeout).
func checkSudoCached() tea.Cmd {
	dry, priv := dryRun, privileged()
	return func() tea.Msg {
		if dry {
			return sudoCachedMsg{cached: true} // never prompt during a dry run
		}
		return sudoCachedMsg{cached: priv.Probe(context.Background())}
	}
}

// authSudo validates the password with su and only then mints the sudo
// ticket (privilege.Sudo.Authenticate: a wrong password must never reach
// sudo, which would burn two pam_faillock failures). A mid-run askpass popup
// validates the same way (see answerAskpass).
func authSudo(password string) tea.Cmd {
	priv := privileged()
	return func() tea.Msg {
		detail, err := priv.Authenticate(context.Background(), password)
		return sudoResultMsg{err: err, detail: detail}
	}
}

// sudoKeepAliveMsg drives the keep-alive loop that refreshes the cached sudo
// timestamp while a queue runs, so a long step (topgrade) can't let it lapse
// and force a later step to re-prompt on the tty.
//
// gen ties a tick to the queue that started its loop (model.sudoGen): a queue
// that ends or aborts leaves one tick pending for up to a minute, and if a new
// queue starts meanwhile, that old tick must not keep a second loop alive.
type sudoKeepAliveMsg struct{ gen int }

const sudoKeepAliveInterval = 60 * time.Second

func sudoTick(gen int) tea.Cmd {
	return tickFn(sudoKeepAliveInterval, func(time.Time) tea.Msg { return sudoKeepAliveMsg{gen: gen} })
}

// refreshSudo extends the cached timestamp non-interactively (a no-op if it has
// already lapsed — a step's `sudo -A` then re-prompts via the popup).
func refreshSudo() tea.Cmd {
	priv := privileged()
	return func() tea.Msg {
		priv.Refresh(context.Background())
		return nil
	}
}

// ── actions ──────────────────────────────────────────────────────────────────
//
// The menu and its queues are data in internal/actions. Discovery is
// read-only (safe even in a dry run) and runs off the UI thread as a tea.Cmd;
// its typed result (actions.Discovery) comes back as a discoveryMsg.

// discoveryMsg is a discovery's result. authed marks a NeedsRoot rescan's:
// the sudo ticket is already primed, so its queue starts without a probe.
type discoveryMsg struct {
	d      actions.Discovery
	authed bool
}

type editorDoneMsg struct{ err error }

// repairScanWorkers is how many `pacman -Qkq` Repair's user-level scan runs
// side by side (actions.Env.RepairWorkers). A variable so tests can pin the
// chunking.
var repairScanWorkers = actions.DefaultRepairWorkers

// actionEnv is what the actions get from the TUI, read on the model's
// goroutine: the run's flags and seams, and the audit and downloads glue
// that lives in this package. The menu audit's config and the log store the
// downloads delete records an abort in are captured here, not in the Cmds
// that use them.
func actionEnv() actions.Env {
	cfg, store := currentAuditConfig(), logs()
	return actions.Env{
		DryRun:        dryRun,
		LookPath:      exec.LookPath,
		RepairWorkers: repairScanWorkers,
		AuditStep:     auditStep,
		AuditReport: func(context.Context) actions.Discovery {
			return menuAudit(time.Now(), cfg).discovery()
		},
		Downloads: downloadsOptions,
		DeleteDownloads: func(plan downloads.Plan) func(context.Context, steps.Env) steps.Result {
			return deleteOldDownloads(plan, store)
		},
	}
}

// discover runs f off the UI thread and delivers its result as a
// discoveryMsg.
func discover(f func(context.Context, actions.Env) actions.Discovery, env actions.Env, authed bool) tea.Cmd {
	return func() tea.Msg { return discoveryMsg{d: f(context.Background(), env), authed: authed} }
}

// execProcess is a seam over tea.ExecProcess so tests can capture the editor
// command without actually handing over the terminal to a real editor.
var execProcess = tea.ExecProcess

// editConfig opens ~/.config/topgrade.toml in $EDITOR (creating it with sane
// defaults first), handing over the terminal via tea.ExecProcess. In a dry run
// it logs the intent instead.
func editConfig() tea.Cmd {
	path := configPath()
	if dryRun {
		store := logs() // the Cmd runs off the model's goroutine (see logs)
		return func() tea.Msg {
			_ = store.Append(logCategoryCleanup, "DRYRUN", "Edit topgrade config", "would open "+editorName()+" on "+path)
			return editorDoneMsg{}
		}
	}
	ensureTopgradeConfig(path)
	return execProcess(exec.Command(editorName(), path), func(err error) tea.Msg {
		return editorDoneMsg{err}
	})
}

// ensureTopgradeConfig creates the config with defaults if it doesn't exist,
// leaving an existing file untouched.
func ensureTopgradeConfig(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(defaultTopgradeConfig), 0o644)
	}
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "topgrade.toml")
}

func editorName() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "nano"
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

const defaultTopgradeConfig = `# topgrade configuration
# Documentation: https://github.com/topgrade-rs/topgrade/wiki/Configuration

[misc]
assume_yes = true
cleanup = true

[linux]
aur_command = "yay"

# Disable steps you don't want by setting them to false:
# [skip_steps]
# flatpak = false
`
