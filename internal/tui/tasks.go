package tui

import (
	"io"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/frontend"
	"github.com/Enzord758/archmaid/internal/steps"
)

// stepState is a step of the checklist: as the engine planned it, and its
// live status.
type stepState struct {
	step   steps.Step // the plan as the checklist and confirms read it (planned)
	status steps.Status
	plan   engine.PlanStep
}

// stepWaitDelay is how long a step's output pipe is still read after its
// child has exited (driver.Deps.WaitDelay): something the child left
// running in the background can't keep the finished step "running". A var
// so tests can shrink it.
var stepWaitDelay = 2 * time.Second

// sigkillGrace is how long a stopped child gets to honour SIGINT before it
// is killed (driver.Deps.KillGrace). A var so tests can shrink it.
var sigkillGrace = 5 * time.Second

// ── actions ──────────────────────────────────────────────────────────────────
//
// The menu and its queues are data in internal/actions; the engine runs
// them (adapter.go).

type editorDoneMsg struct{ err error }

// repairScanWorkers is how many `pacman -Qkq` Repair's user-level scan runs
// side by side (actions.Env.RepairWorkers). A variable so tests can pin the
// chunking.
var repairScanWorkers = actions.DefaultRepairWorkers

// actionEnv is what the actions get from the TUI: the run's flags and seams,
// and the audit and downloads glue (internal/frontend) on the TUI's
// settings (glue.go). main builds the Session with it, before the program
// starts. What it reads when:
//
//   - here: dryRun, repairScanWorkers, the menu audit's config and the log
//     store the downloads delete records an abort in (both captured, so
//     what uses them later never looks them up);
//   - when the Session builds a queue, on the Driver's loop: the audit
//     step's config, for Full Cleanup and Run topgrade;
//   - when a discovery runs, on a Driver worker: the menu audit (with the
//     captured config), downloadsOptions — so --downloads,
//     $ARCHMAID_DOWNLOADS, $HOME, dryRun and the downloads test seams are
//     read then, not here — and DeleteDownloads, which builds the delete
//     step's Run around the captured store;
//   - when that delete step runs, on a Driver worker: downloadsOptions
//     again (frontend.Deletions.Step).
//
// main checks it with Validate at startup.
func actionEnv() actions.Env {
	return frontend.Glue{
		AuditConfig: currentAuditConfig,
		Render:      renderAudit,
		Downloads:   downloadsOptions,
		Store:       logs(),
		Deletions:   &downloadsRunning,
	}.Env(actions.Env{
		DryRun:        dryRun,
		LookPath:      exec.LookPath,
		RepairWorkers: repairScanWorkers,
	})
}

// execProcess is a seam over tea.ExecProcess so tests can capture the editor
// command without actually handing over the terminal to a real editor.
var execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd { return tea.Exec(handedTerminal{c}, fn) }

// handedTerminal is tea.ExecProcess's command for a program the TUI hands
// its terminal to (the editor): the terminal Run holds exclusive is let go
// while it runs, since an editor may open /dev/tty itself (tcell's do).
type handedTerminal struct{ *exec.Cmd }

func (c handedTerminal) Run() error {
	resume := termGuard.Load().Pause()
	defer resume()
	return c.Cmd.Run()
}

// SetStdin, SetStdout and SetStderr give the program the terminal, unless
// the command already names its own (as tea.ExecProcess does).
func (c handedTerminal) SetStdin(r io.Reader) {
	if c.Stdin == nil {
		c.Stdin = r
	}
}

func (c handedTerminal) SetStdout(w io.Writer) {
	if c.Stdout == nil {
		c.Stdout = w
	}
}

func (c handedTerminal) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = w
	}
}

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
	frontend.EnsureTopgradeConfig(path) // best effort: the editor shows what's there
	return execProcess(exec.Command(editorName(), path), func(err error) tea.Msg {
		return editorDoneMsg{err}
	})
}

// configPath is the topgrade config (frontend.TopgradeConfigPath); "" when
// the home directory is unknown, which creates nothing.
func configPath() string {
	path, _ := frontend.TopgradeConfigPath()
	return path
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
