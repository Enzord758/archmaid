package main

import (
	"bufio"
	"io"
	"os/exec"
	"strings"

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
	dur    string // filled in when the step completes
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

func runStep(s step, ch chan<- streamMsg) {
	go func() {
		cmd := exec.Command(s.command, s.args...)

		// Fold stderr into stdout through a single pipe (the bash `2>&1`).
		pr, pw := io.Pipe()
		cmd.Stdout, cmd.Stderr = pw, pw

		if err := cmd.Start(); err != nil {
			ch <- streamMsg{done: true, err: err}
			return
		}

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
	return []step{
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
		// Orphan removal is dynamic — pacman -Qdtq is queried at build time in
		// the real version and the package list spliced into args here.
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
		{
			title: "Updating system with topgrade…", logLabel: "Topgrade completed",
			command: "topgrade", args: []string{"-y"},
		},
	}
}

func topgradeSteps() []step {
	return []step{{
		title: "Updating system with topgrade…", logLabel: "Topgrade completed",
		command: "topgrade", args: []string{"-y"},
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
type sudoResultMsg struct{ err error }   // result of an actual auth attempt

// checkSudoCached probes whether sudo credentials are already valid, so we can
// skip the password prompt entirely when they are (e.g. re-running within the
// timeout).
func checkSudoCached() tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("sudo", "-n", "-v").Run()
		return sudoCachedMsg{cached: err == nil}
	}
}

// authSudo feeds the password to `sudo -S -v` on stdin. -S makes sudo read the
// password from stdin instead of the tty; -v just validates and refreshes the
// cached timestamp. sudo's own "[sudo] password for…" prompt goes to stderr,
// which we discard.
func authSudo(password string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("sudo", "-S", "-v")
		cmd.Stdin = strings.NewReader(password + "\n")
		cmd.Stderr = io.Discard
		return sudoResultMsg{err: cmd.Run()}
	}
}
