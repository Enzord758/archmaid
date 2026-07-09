package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// step is one unit of work. A menu action expands into a queue of these; the
// running screen executes them in order and renders a live checklist.
type step struct {
	title    string   // shown in the checklist and spinner ("Cleaning package cache…")
	logLabel string   // written to the log file on failure ("Old package cache cleaned")
	command  string   // e.g. "sudo"
	args     []string // e.g. {"paccache", "-r"}

	// confirm, if set, is asked before the step runs. optional decides what a
	// "no" means: skip just this step (optional) vs. abort the whole queue.
	confirm  string
	optional bool

	// needsSudo steps are grouped so we can prime `sudo -v` once up front
	// (see primeSudo) instead of prompting mid-run behind a spinner.
	needsSudo bool
}

type stepStatus int

const (
	statusPending stepStatus = iota
	statusRunning
	statusOK
	statusFailed
	statusSkipped
)

// stepState tracks a step's live status for the checklist view.
type stepState struct {
	step   step
	status stepStatus
}

// ── streaming ────────────────────────────────────────────────────────────────
//
// The whole reason to leave gum: a running command's output streams into the
// UI live instead of being swallowed by a spinner and dumped in a pager after.
//
// runStep launches the command and pushes every output line onto ch, then a
// single terminal message with the exit error. Using one ordered channel (line
// messages followed by exactly one done message) keeps stdout, stderr, and the
// exit status strictly ordered — no interleaving races between goroutines.

type streamMsg struct {
	line string // a line of combined stdout+stderr (ignored when done)
	done bool   // true for the final message
	err  error  // non-nil if the command failed (only meaningful when done)
}

// runStep starts the step's command and streams its output on ch, returning the
// running process so the caller can signal it on abort (nil in dry run or if the
// command never starts). Start() happens synchronously so the handle is
// available immediately; scanning and Wait run on a goroutine.
func runStep(s step, ch chan<- streamMsg) *os.Process {
	// Dry run: don't execute anything mutating — just report what would run.
	// The step is logged (as a success) in handleStream's dry-run branch.
	if dryRun {
		go func() {
			ch <- streamMsg{line: "[dry-run] would run: " + s.command + " " + strings.Join(s.args, " ")}
			ch <- streamMsg{done: true}
		}()
		return nil
	}

	name, args := s.command, s.args
	if name == "sudo" {
		// -A: read the password via SUDO_ASKPASS (a TUI popup), never the tty.
		args = append([]string{"-A"}, s.args...)
	}
	cmd := exec.Command(name, args...)

	// Fold stderr into stdout through a single pipe (the bash `2>&1`).
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	if err := cmd.Start(); err != nil {
		go func() { ch <- streamMsg{done: true, err: err} }()
		return nil
	}

	go func() {
		// Scan on a goroutine so cmd.Wait can proceed. close(scanned) lets us
		// guarantee every line is sent before the done message.
		scanned := make(chan struct{})
		go func() {
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines (pacman)
			for sc.Scan() {
				ch <- streamMsg{line: sc.Text()}
			}
			close(scanned)
		}()

		err := cmd.Wait()
		pw.Close() // unblocks the scanner with EOF
		<-scanned  // all lines are on the channel before we report done
		ch <- streamMsg{done: true, err: err}
	}()

	return cmd.Process
}

// waitForStream turns the next channel message into a tea.Msg. Re-issue it
// after every non-done streamMsg to keep pumping the channel through Update.
func waitForStream(ch <-chan streamMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// ── task queues ──────────────────────────────────────────────────────────────
//
// Each menu action returns the steps it wants to run. Optional/destructive
// bits become per-step confirm prompts instead of the bash script's inline
// `gum confirm` calls, so the model can drive them uniformly.

func fullCleanupSteps() []step {
	steps := []step{
		// topgrade runs first: update the system, then clean up the result.
		// It's also non-destructive and abortable, so it doubles as a soft
		// buffer before the destructive steps begin. needsSudo so credentials
		// are primed + kept alive — topgrade shells out to sudo pacman itself.
		//
		// hyprpm is excluded and run as the LAST step instead: hyprpm drops the
		// sudo ticket on exit (NSys::root::dropSudo → sudo -k), which would kill
		// the primed credentials mid-queue and force a re-prompt for every
		// later sudo step.
		{
			title: "Updating system with topgrade…", logLabel: "Topgrade completed",
			command: "topgrade", args: []string{"-y", "--disable", "hyprpm"}, needsSudo: true,
		},
		{
			title: "Cleaning package cache…", logLabel: "Old package cache cleaned",
			command: "sudo", args: []string{"paccache", "-r"}, needsSudo: true,
		},
		{
			title: "Deleting entire package cache…", logLabel: "Entire package cache removed",
			command: "sudo", args: []string{"paccache", "-rk0"}, needsSudo: true,
			confirm:  "Delete ENTIRE package cache? (can't reinstall without internet)",
			optional: true,
		},
	}

	// Orphan removal — queried now so the confirm shows the real count and the
	// package list is spliced into the step. Omitted entirely when there are
	// no orphans.
	if orphans := orphanPackages(); len(orphans) > 0 {
		steps = append(steps, step{
			title: "Removing orphaned packages…", logLabel: "Orphaned packages removed",
			command: "sudo", args: append([]string{"pacman", "-Rns", "--noconfirm"}, orphans...),
			needsSudo: true, optional: true,
			confirm: fmt.Sprintf("Remove %d orphaned package(s)?", len(orphans)),
		})
	}

	steps = append(steps, []step{
		{
			title: "Cleaning AUR cache…", logLabel: "AUR cache cleaned",
			command: "yay", args: []string{"-Sc", "--noconfirm"},
		},
		{
			title: "Cleaning old temp files…", logLabel: "Old temporary files removed",
			command: "sudo", needsSudo: true,
			args: []string{"find", "/tmp", "/var/tmp", "-mindepth", "1", "-maxdepth", "1",
				"-mtime", "+2", "-exec", "rm", "-rf", "{}", "+"},
		},
		{
			title: "Vacuuming logs…", logLabel: "Old logs removed",
			command: "sudo", args: []string{"journalctl", "--vacuum-time=2weeks"}, needsSudo: true,
		},
		{
			title: "Checking package integrity…", logLabel: "Package integrity check completed",
			command: "sudo", args: []string{"pacman", "-Qk"}, needsSudo: true,
		},
	}...)

	// Hyprland plugins go last: hyprpm drops the sudo ticket on exit, so after
	// this step nothing must need sudo anymore (see the topgrade note above).
	if _, err := exec.LookPath("hyprpm"); err == nil {
		steps = append(steps, step{
			title: "Updating Hyprland plugins…", logLabel: "Hyprland plugins updated",
			command: "hyprpm", args: []string{"update"},
		})
	}

	return steps
}

func topgradeSteps() []step {
	// needsSudo: topgrade shells out to sudo internally, so prime + keep-alive
	// credentials (otherwise its sudo prompt lands raw on the terminal).
	return []step{{
		title: "Updating system with topgrade…", logLabel: "Topgrade completed",
		command: "topgrade", args: []string{"-y"}, needsSudo: true,
	}}
}

func clearUserCacheSteps() []step {
	return []step{{
		title: "Cleaning user cache…", logLabel: "User cache cleared",
		command: "bash", args: []string{"-c", "rm -rf \"$HOME/.cache/\"*"},
		confirm: "Clear all user cache?",
	}}
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
	return func() tea.Msg {
		if dryRun {
			return sudoCachedMsg{cached: true} // never prompt during a dry run
		}
		cmd := exec.Command("sudo", "-n", "-v")
		err := cmd.Run()
		return sudoCachedMsg{cached: err == nil}
	}
}

// authSudo validates the password and mints the sudo ticket — in two stages,
// because a wrong password fed to `sudo -S` burns TWO pam_faillock failures
// (sudo's passwd_tries retry re-enters PAM on the EOF'd stdin before giving
// up; measured live). With Arch's default `deny=3`, one mistyped in-app
// password would leave the account one failure from lockout.
//
//  1. Validate via `su <user> -c true` — single-shot PAM, no retry loop (and
//     on Arch, su's PAM stack doesn't include pam_faillock at all, so a wrong
//     entry burns nothing).
//  2. Only a known-correct password reaches `sudo -S -v`, which then always
//     succeeds: mints the ticket, burns nothing, and clears stale failures.
//
// Stderr is captured so a pam_faillock lockout notice ("account locked…")
// can be surfaced verbatim instead of the generic wrong-password error.
func authSudo(password string) tea.Cmd {
	return func() tea.Msg {
		self, err := user.Current()
		if err != nil {
			return sudoResultMsg{err: err}
		}

		check := exec.Command("su", self.Username, "-c", "true")
		check.Stdin = strings.NewReader(password + "\n")
		var checkErr bytes.Buffer
		check.Stderr = &checkErr
		if err := check.Run(); err != nil {
			return sudoResultMsg{err: err, detail: lockoutNotice(checkErr.String())}
		}

		cmd := exec.Command("sudo", "-S", "-v")
		cmd.Stdin = strings.NewReader(password + "\n")
		var sudoErr bytes.Buffer
		cmd.Stderr = &sudoErr
		if err := cmd.Run(); err != nil {
			return sudoResultMsg{err: err, detail: lockoutNotice(sudoErr.String())}
		}
		return sudoResultMsg{}
	}
}

// lockoutNotice extracts a pam_faillock lockout message from PAM stderr, if
// present, so the UI can say "account locked" rather than "wrong password".
func lockoutNotice(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(strings.ToLower(line), "locked") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// sudoKeepAliveMsg drives the keep-alive loop that refreshes the cached sudo
// timestamp while a queue runs, so a long step (topgrade) can't let it lapse
// and force a later step to re-prompt on the tty.
type sudoKeepAliveMsg struct{}

const sudoKeepAliveInterval = 60 * time.Second

func sudoTick() tea.Cmd {
	return tea.Tick(sudoKeepAliveInterval, func(time.Time) tea.Msg { return sudoKeepAliveMsg{} })
}

// refreshSudo extends the cached timestamp non-interactively (a no-op if it has
// already lapsed — a step's `sudo -A` then re-prompts via the popup).
func refreshSudo() tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("sudo", "-n", "-v")
		cmd.Run()
		return nil
	}
}

// ── dynamic actions ──────────────────────────────────────────────────────────
//
// These need a discovery pass before their step queue exists. Discovery is
// read-only (safe even in a dry run), runs off the UI thread as a tea.Cmd, and
// returns a discoveryMsg — either steps to run, a services list for the
// multi-select, or a notice when there's nothing to do.

type discoveryMsg struct {
	kind     string // "repair" | "downloads" | "services"
	pending  []step // steps to run (repair, downloads)
	services []string
	notice   string // set when there's nothing to do
}

type editorDoneMsg struct{ err error }

func discoverRepair() tea.Cmd {
	return func() tea.Msg {
		pkgs := brokenPackages()
		if len(pkgs) == 0 {
			return discoveryMsg{kind: "repair", notice: "No broken packages found."}
		}
		return discoveryMsg{kind: "repair", pending: []step{{
			title: "Reinstalling packages…", logLabel: "Package repair completed",
			command: "sudo", args: append([]string{"pacman", "-S", "--noconfirm"}, pkgs...),
			needsSudo: true, optional: true,
			confirm: fmt.Sprintf("Reinstall %d broken package(s)?", len(pkgs)),
		}}}
	}
}

// brokenPackages lists packages with missing or altered files (pacman -Qk).
func brokenPackages() []string {
	out, _ := exec.Command("bash", "-c",
		`pacman -Qk 2>/dev/null | grep -vE '0 (missing|altered) files' | awk -F: '{print $1}' | sort -u`,
	).Output()
	return nonEmptyLines(string(out))
}

// orphanPackages lists packages installed as dependencies that nothing needs
// anymore (pacman -Qdtq). Fast enough to query synchronously while building the
// Full Cleanup queue. Returns empty when there are none (pacman exits 1).
func orphanPackages() []string {
	out, _ := exec.Command("pacman", "-Qdtq").Output()
	return nonEmptyLines(string(out))
}

func discoverDownloads() tea.Cmd {
	return func() tea.Msg {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Downloads")
		out, _ := exec.Command("bash", "-c",
			fmt.Sprintf(`find %q -maxdepth 1 -mtime +30 2>/dev/null | wc -l`, dir)).Output()
		n := strings.TrimSpace(string(out))
		if n == "" || n == "0" {
			return discoveryMsg{kind: "downloads", notice: "No files older than 30 days in Downloads."}
		}
		return discoveryMsg{kind: "downloads", pending: []step{{
			title: "Deleting old downloads…", logLabel: "Old downloads deleted",
			command: "bash", args: []string{"-c",
				fmt.Sprintf(`find %q -maxdepth 1 -mtime +30 -exec rm -rf {} +`, dir)},
			optional: true,
			confirm:  fmt.Sprintf("Delete %s file(s) older than 30 days in Downloads?", n),
		}}}
	}
}

// servicesPipeline reproduces the bash version: services that are enabled but
// currently inactive, minus systemd-internal/oneshot noise.
const servicesPipeline = `comm -12 ` +
	`<(systemctl list-unit-files --type=service --state=enabled --no-legend | awk '{print $1}' | sort) ` +
	`<(systemctl list-units --type=service --state=inactive --no-legend | awk '{print $1}' | sort) ` +
	`| grep -Ev 'systemd-|getty@|emergency|rescue|initrd|plymouth|shadow|pkgstats|man-db|` +
	`mkinitcpio|modprobe@|audit|dirmngr|gpg-agent|keyboxd|dm-event|lm_sensors|nmb|archlinux-keyring-wkd-sync'`

func discoverServices() tea.Cmd {
	return func() tea.Msg {
		out, _ := exec.Command("bash", "-c", servicesPipeline).Output()
		svcs := nonEmptyLines(string(out))
		if len(svcs) == 0 {
			return discoveryMsg{kind: "services", notice: "No enabled-but-inactive services found."}
		}
		return discoveryMsg{kind: "services", services: svcs}
	}
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
		return func() tea.Msg {
			appendLog("DRYRUN", "Edit topgrade config", "would open "+editorName()+" on "+path)
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
