package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/proc"
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
	// onAccept, if set, runs when confirm is answered yes, before the step
	// launches (the audit's snapshot commit; see stepResult.commit).
	onAccept func() error

	// needsSudo steps are grouped so we can prime `sudo -v` once up front
	// (see primeSudo) instead of prompting mid-run behind a spinner. They are
	// also the only steps whose child gets SUDO_ASKPASS (see stepEnv).
	needsSudo bool

	// category buckets this step's log entries (see logPath); zero value
	// resolves to logCategoryCleanup via stepCategory.
	category logCategory
	// alwaysLog logs this step's outcome even on success in a real run
	// (normally only failures are logged), and a SKIP entry when its confirm
	// is declined — for steps whose outcome must stay on record: anything that
	// removes or overwrites something, the system upgrade, and the AUR audit.
	// Without it, what happened is only traceable through pacman's own log.
	alwaysLog bool

	// run, if set, makes this an in-process step: instead of exec'ing
	// command/args, launch runs run() off the UI thread as a tea.Cmd and its
	// result finishes the step (see handleStepResult). In-process steps run
	// even in a dry run, so run must be read-only apart from state it gates on
	// dryRun itself. There's no child to signal: an abort cancels ctx, so run
	// must return promptly once it's done, and its late result is dropped.
	run func(ctx context.Context) stepResult
	// aurAudit marks the AUR audit step (auditStep), whose lookup activate
	// starts early as an auditFetch; launch then runs that fetch's run.
	aurAudit bool
}

// stepResult is what an in-process step returns. output is shown in the
// output pane and logged like a command's output; err fails the step.
// confirmNext, if set, puts a confirm in front of the *next* step and makes
// it optional, so declining skips just that step (the audit uses this to
// gate topgrade on a high-severity finding). commit, if set, is deferred work
// that must wait for that answer: it becomes the next step's onAccept, so it
// runs only if the confirm is accepted and is dropped on "no" or an abort.
// Without a confirmNext it runs as soon as the result arrives. (The audit's
// snapshot save: see saveSnapshot.) insertNext, if set, is a step to run
// right after this one, inserted into the queue when the result arrives (the
// orphan probe's removal: see findOrphans); it carries its own confirm. Both
// are ignored on a result with err.
type stepResult struct {
	output      string
	err         error
	confirmNext string
	commit      func() error
	insertNext  *step
}

// stepCategory is s.category, defaulting to logCategoryCleanup when unset.
func stepCategory(s step) logCategory {
	if s.category == "" {
		return logCategoryCleanup
	}
	return s.category
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
func runStep(s step, ch chan<- proc.Msg, quit <-chan struct{}) *proc.Child {
	return proc.New(procOptions()).Start(stepSpec(s), ch, quit)
}

// stepSpec is the command a step runs. A dry run reports the step's own
// command line, without the sudo flag added here.
func stepSpec(s step) proc.Spec {
	name, args := s.command, s.args
	if name == "sudo" {
		// -A: read the password via SUDO_ASKPASS (a TUI popup), never the tty.
		// Without our askpass, -A would fall back to sudo.conf's `Path
		// askpass` (a GUI helper, if one is configured) or fail; -n instead
		// relies on the primed ticket and fails cleanly when it's gone.
		flag := "-n"
		if s.needsSudo && askpassWrapper != "" {
			flag = "-A"
		}
		args = append([]string{flag}, s.args...)
	}
	return proc.Spec{
		Name: name, Args: args, Env: stepEnv(s),
		Display: s.command + " " + strings.Join(s.args, " "),
	}
}

// stepEnv is the environment a step's child runs with: archmaid's own, without
// any SUDO_ASKPASS, plus our askpass wrapper for steps that use sudo (sudo
// steps, and topgrade, which runs sudo itself). Everything a step starts
// inherits it, down to AUR build scripts, so the fewer steps that carry the
// socket's location the better; one the user's shell exported is dropped too.
func stepEnv(s step) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SUDO_ASKPASS=") {
			env = append(env, kv)
		}
	}
	if s.needsSudo && askpassWrapper != "" {
		env = append(env, "SUDO_ASKPASS="+askpassWrapper)
	}
	return env
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

// ── task queues ──────────────────────────────────────────────────────────────
//
// Each menu action returns the steps it wants to run. Optional/destructive
// bits become per-step confirm prompts instead of the bash script's inline
// `gum confirm` calls, so the model can drive them uniformly.

func fullCleanupSteps() []step {
	steps := []step{
		// The AUR audit runs first, directly in front of topgrade, so a
		// maintainer change can gate the update before yay builds anything
		// (see auditStep). Read-only; one network call.
		auditStep(),
		// Then topgrade: update the system, then clean up the result. It's
		// also non-destructive and abortable, so it doubles as a soft buffer
		// before the destructive steps begin. needsSudo so credentials are
		// primed + kept alive — topgrade shells out to sudo pacman itself.
		// alwaysLog: it changes more than any other step (a full system
		// upgrade), so its output is kept even when it succeeds.
		//
		// hyprpm is excluded and run as the LAST step instead: hyprpm drops the
		// sudo ticket on exit (NSys::root::dropSudo → sudo -k), which would kill
		// the primed credentials mid-queue and force a re-prompt for every
		// later sudo step.
		{
			title: "Updating system with topgrade…", logLabel: "Topgrade completed",
			command: "topgrade", args: []string{"-y", "--disable", "hyprpm"}, needsSudo: true,
			category: logCategoryTopgrade, alwaysLog: true,
		},
		{
			title: "Cleaning package cache…", logLabel: "Old package cache cleaned",
			command: "sudo", args: []string{"paccache", "-r"}, needsSudo: true,
		},
		{
			title: "Deleting entire package cache…", logLabel: "Entire package cache removed",
			command: "sudo", args: []string{"paccache", "-rk0"}, needsSudo: true,
			confirm:   "Delete ENTIRE package cache? (can't reinstall without internet)",
			optional:  true,
			alwaysLog: true,
		},
		// Orphans are looked for here, after the update, not when the queue
		// is built: an upgrade can orphan packages of its own, and the list
		// the removal acts on must be the one its confirm showed. The probe
		// puts the removal step right after itself when it finds any (see
		// findOrphans), so building this queue runs nothing.
		orphanProbeStep(),
		{
			title: "Cleaning AUR cache…", logLabel: "AUR cache cleaned",
			command: "yay", args: []string{"-Sc", "--noconfirm"},
		},
		// Temp files are aged out by the system's own tmpfiles.d rules (on
		// Arch: /tmp after 10 days, /var/tmp after 30), not a hand-rolled
		// find/rm. The rules know what must survive — the sockets and lock
		// dirs of running sessions, and their x/X exclusions — where a blanket
		// `rm -rf` of anything two days old pulled those out from under live
		// programs.
		{
			title: "Cleaning old temp files…", logLabel: "Temp files cleaned per tmpfiles.d rules",
			command: "sudo", args: []string{"systemd-tmpfiles", "--clean"}, needsSudo: true,
			alwaysLog: true,
		},
		{
			title: "Vacuuming logs…", logLabel: "Old logs removed",
			command: "sudo", args: []string{"journalctl", "--vacuum-time=2weeks"}, needsSudo: true,
			alwaysLog: true,
		},
		{
			title: "Checking package integrity…", logLabel: "Package integrity check completed",
			command: "sudo", args: []string{"pacman", "-Qkq"}, needsSudo: true,
		},
	}

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
	return []step{
		// The same AUR audit gate as Full Cleanup: topgrade runs yay, so a
		// maintainer change must be able to stop the update (see auditStep).
		auditStep(),
		// needsSudo: topgrade shells out to sudo internally, so prime +
		// keep-alive credentials (otherwise its sudo prompt lands raw on the
		// terminal).
		{
			title: "Updating system with topgrade…", logLabel: "Topgrade completed",
			command: "topgrade", args: []string{"-y"}, needsSudo: true,
			category: logCategoryTopgrade, alwaysLog: true,
		},
	}
}

func clearUserCacheSteps() []step {
	return []step{{
		title: "Cleaning user cache…", logLabel: "User cache cleared",
		command: "bash", args: []string{"-c", "rm -rf \"$HOME/.cache/\"*"},
		confirm:   "Clear all user cache?",
		alwaysLog: true,
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
//  1. Validate with su (see suCheck) — single-shot PAM, burns nothing.
//  2. Only a known-correct password reaches `sudo -S -v`, which then always
//     succeeds: mints the ticket, burns nothing, and clears stale failures.
//
// Stderr is captured so a pam_faillock lockout notice ("account locked…")
// can be surfaced verbatim instead of the generic wrong-password error. A
// mid-run askpass popup validates the same way (see answerAskpass).
func authSudo(password string) tea.Cmd {
	return func() tea.Msg {
		if detail, err := suCheck(password); err != nil {
			return sudoResultMsg{err: err, detail: detail}
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

// suCheck validates password as the current user's with
// `su -s /usr/bin/true <user>`: single-shot PAM with no retry loop, and on Arch
// su's PAM stack doesn't include pam_faillock at all, so a wrong entry burns
// nothing. -s runs true in place of the login shell, so none of the user's
// shell startup files run (`su -c true` still had zsh read ~/.zshenv). On
// failure, detail is a lockout notice from su's stderr, if there was one.
func suCheck(password string) (detail string, err error) {
	self, err := user.Current()
	if err != nil {
		return "", err
	}
	check := exec.Command("su", "-s", "/usr/bin/true", self.Username)
	check.Stdin = strings.NewReader(password + "\n")
	var stderr bytes.Buffer
	check.Stderr = &stderr
	if err := check.Run(); err != nil {
		return lockoutNotice(stderr.String()), err
	}
	return "", nil
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
	kind     string // "repair" | "downloads" | "services" | "audit"
	pending  []step // steps to run (repair, downloads)
	services []string
	report   string // rendered AUR-audit report (audit)
	notice   string // set when there's nothing to do
	// commit (audit) accepts the report's high findings into the snapshot (`a`)
	commit func() error
	// candidates (repair) are what the user-level scan flagged; they still
	// need the root recheck (discoverRepair) before anything is reinstalled
	candidates []string
}

type editorDoneMsg struct{ err error }

// repairScanWorkers is how many `pacman -Qkq` Repair's user-level scan runs
// side by side. The check is stat-bound and split over every installed
// package, so it scales with cores up to about 16 (1422 packages: 850 ms in
// one process, 501 ms at 4, 369 ms at 8, 316 ms at 16); more only adds
// processes. A variable so tests can pin the chunking.
var repairScanWorkers = min(runtime.NumCPU(), 16)

// repairErr words a Repair scan failure for the notice. shortErr's "no answer
// within 20s" is the audit's own deadline, so a timeout here says only that.
func repairErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return shortErr(err)
}

// scanRepairCandidates is Repair's first stage, run as the user with no sudo:
// every package with a file the user can't stat (brokenCandidates). Nothing
// flagged means nothing is broken, which ends Repair without a password
// prompt; otherwise the candidates go to discoverRepair, which needs root.
func scanRepairCandidates() tea.Cmd {
	return func() tea.Msg {
		cands, err := brokenCandidates(repairScanWorkers)
		if err != nil {
			return discoveryMsg{kind: "repair", notice: "Couldn't scan for broken packages: " + repairErr(err)}
		}
		if len(cands) == 0 {
			return discoveryMsg{kind: "repair", notice: "No broken packages found."}
		}
		return discoveryMsg{kind: "repair", candidates: cands}
	}
}

// discoverRepair runs after the sudo ticket is primed (see model.scanAfterAuth):
// it rechecks the user scan's candidates as root, and only the packages it
// confirms are offered for reinstalling.
func discoverRepair(candidates []string) tea.Cmd {
	return func() tea.Msg {
		pkgs, err := brokenPackages(candidates)
		if err != nil {
			return discoveryMsg{kind: "repair", notice: "Couldn't scan for broken packages: " + repairErr(err)}
		}
		if len(pkgs) == 0 {
			return discoveryMsg{kind: "repair", notice: "No broken packages found."}
		}
		return discoveryMsg{kind: "repair", pending: []step{{
			title: "Reinstalling packages…", logLabel: "Package repair completed",
			command: "sudo", args: append([]string{"pacman", "-S", "--noconfirm", "--"}, pkgs...),
			needsSudo: true, optional: true, alwaysLog: true,
			confirm: fmt.Sprintf("Reinstall %d broken package(s)?", len(pkgs)),
		}}}
	}
}

// brokenCandidates lists, as the user, the packages pacman -Qkq flags. It
// lists the installed packages (pacman -Qq), splits them into up to workers
// contiguous chunks and checks those in parallel, one `pacman -Qkq -- <chunk>`
// each (exec'd directly, no shell). A missing file fails stat for anyone, so
// every truly broken package is in the result — but so is any package with a
// file in a root-only directory (/boot is 0700, /var/lib/nfs/statd is
// rpcuser's): amd-ucode and nfs-utils are flagged on every run. So the result
// is only a candidate list for brokenPackages. Chunks are merged in order, so
// it is what a single `pacman -Qkq` over everything would report.
func brokenCandidates(workers int) ([]string, error) {
	installed, err := qkqOutput("pacman", "-Qq")
	if err != nil {
		return nil, err
	}
	chunks := chunkStrings(nonEmptyLines(installed), workers)
	found := make([][]string, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Go(func() {
			out, err := qkqOutput("pacman", append([]string{"-Qkq", "--"}, chunk...)...)
			found[i], errs[i] = parseBrokenPackages(out), err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return slices.Concat(found...), nil
}

// chunkStrings splits s into at most n contiguous chunks of near-equal size.
func chunkStrings(s []string, n int) [][]string {
	n = max(1, min(n, len(s)))
	var chunks [][]string
	for i := range n {
		if lo, hi := i*len(s)/n, (i+1)*len(s)/n; hi > lo {
			chunks = append(chunks, s[lo:hi])
		}
	}
	return chunks
}

// brokenPackages rechecks the user scan's candidates as root
// (`sudo -n pacman -Qkq -- <candidates>`) and returns those that really have
// missing files: root can stat into the directories the user couldn't, so the
// false positives drop out. Only the candidates are checked, which takes ~50 ms
// where a full scan as root took ~850. -n: the ticket is already primed, and
// nothing may prompt on the raw tty.
func brokenPackages(candidates []string) ([]string, error) {
	out, err := qkqOutput("sudo", append([]string{"-n", "pacman", "-Qkq", "--"}, candidates...)...)
	if err != nil {
		return nil, err
	}
	return parseBrokenPackages(out), nil
}

// qkqOutput runs a pacman query (proc.Query) and returns its stdout. pacman
// -Qkq exits 1 when it finds missing files, so an error with a report on
// stdout is a result, not a failure; an error with nothing on stdout is one,
// reported as the command's stderr when it has any.
func qkqOutput(name string, args ...string) (string, error) {
	out, err := proc.Query(context.Background(), name, args...)
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if msg := strings.TrimSpace(string(exit.Stderr)); msg != "" {
				return "", errors.New(msg)
			}
		}
		return "", err
	}
	return string(out), nil
}

// parseBrokenPackages picks the packages out of `pacman -Qkq` output, which
// prints one "name /path/to/missing/file" line per missing file and nothing for
// healthy packages (plain -Qk prints a summary line for every installed
// package, ~1400 of them, to report a handful). A package with several missing
// files is listed once, in first-seen order. Lines whose second field isn't an
// absolute path (stray warnings) are ignored.
func parseBrokenPackages(out string) []string {
	var pkgs []string
	seen := map[string]bool{}
	for _, line := range nonEmptyLines(out) {
		name, path, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(strings.TrimSpace(path), "/") || seen[name] {
			continue
		}
		seen[name] = true
		pkgs = append(pkgs, name)
	}
	return pkgs
}

// orphanProbeStep is Full Cleanup's orphan check: an in-process step that runs
// after the update and, if it finds orphans, puts their removal right after
// itself (see findOrphans). It only reads, so it runs in a dry run too.
func orphanProbeStep() step {
	return step{
		title: "Finding orphaned packages…", logLabel: "Orphaned packages checked",
		category: logCategoryOrphans,
		run:      findOrphans,
	}
}

// findOrphans lists the orphans as they are now — after topgrade, which can
// orphan packages itself — and previews what `pacman -Rns` would take with
// them. With any found, the result inserts the removal step after the probe,
// with exactly these names and a confirm that shows both counts; with none,
// nothing follows. A failing query fails the step and inserts nothing: no
// removal is offered without its preview.
func findOrphans(ctx context.Context) stepResult {
	orphans, err := orphanPackages(ctx)
	if err != nil {
		return stepResult{output: "Couldn't list orphaned packages: " + err.Error(), err: err}
	}
	if len(orphans) == 0 {
		return stepResult{output: "No orphaned packages."}
	}
	cascade, err := orphanRemovalPreview(ctx, orphans)
	if err != nil {
		return stepResult{output: "Couldn't preview the orphan removal: " + err.Error(), err: err}
	}
	msg := fmt.Sprintf("Remove %d orphaned package(s)?", len(orphans))
	if total := len(cascade); total > len(orphans) {
		msg = fmt.Sprintf("Remove %d orphaned package(s) — %d total once dependencies cascade?",
			len(orphans), total)
	}
	out := fmt.Sprintf("%d orphaned package(s): %s\npacman -Rns would remove %d package(s)",
		len(orphans), strings.Join(orphans, " "), len(cascade))
	if len(cascade) > 0 {
		out += ":\n  " + strings.Join(cascade, "\n  ")
	}
	return stepResult{output: out, insertNext: &step{
		title: "Removing orphaned packages…", logLabel: "Orphaned packages removed",
		command: "sudo", args: append([]string{"pacman", "-Rns", "--noconfirm", "--"}, orphans...),
		needsSudo: true, optional: true,
		confirm:   msg,
		category:  logCategoryOrphans,
		alwaysLog: true,
	}}
}

// orphanPackages lists packages installed as dependencies that nothing needs
// anymore (pacman -Qdtq). pacman reports "none" by exiting 1 without a word,
// so that is an empty list; any other failure is an error.
func orphanPackages(ctx context.Context) ([]string, error) {
	out, err := proc.Query(ctx, "pacman", "-Qdtq")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && len(exit.Stderr) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// orphanRemovalPreview lists what `pacman -Rns <orphans>` will actually
// remove: -s recursively drops each target's own dependencies too, once
// nothing else needs them, so the true total can run well past len(orphans)
// (a real run confirmed 23 and removed 46). --print performs no changes and
// needs no root; it conflicts with -n/--nosave, so this probe uses -Rs, not
// -Rns — the dropped -n only affects whether removed configs are kept as
// .pacsave, not what gets selected.
func orphanRemovalPreview(ctx context.Context, orphans []string) ([]string, error) {
	out, err := proc.Query(ctx, "pacman", append([]string{"-Rs", "--print", "--"}, orphans...)...)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// servicesExclude is systemd-internal/oneshot noise that is never offered for
// disabling.
var servicesExclude = regexp.MustCompile(`systemd-|getty@|emergency|rescue|initrd|plymouth|shadow|pkgstats|man-db|` +
	`mkinitcpio|modprobe@|audit|dirmngr|gpg-agent|keyboxd|dm-event|lm_sensors|nmb|archlinux-keyring-wkd-sync`)

// parseInactiveServices picks the unit names out of
// `systemctl list-units --type=service --all --state=inactive --plain --no-legend`
// (columns UNIT LOAD ACTIVE SUB DESCRIPTION). Units that aren't loaded
// (not-found, masked, …) can't be enabled and are dropped; they print with a
// leading "●" marker that must not be mistaken for the name. Templates and
// instances ("name@.service", "name@arg.service") are dropped too: the
// unit-file listing this replaces only ever matched plain units. The result is
// filtered through servicesExclude and sorted.
func parseInactiveServices(out string) []string {
	var units []string
	for _, line := range nonEmptyLines(out) {
		f := strings.Fields(strings.TrimLeft(line, "●* \t"))
		if len(f) < 2 || f[1] != "loaded" || !strings.HasSuffix(f[0], ".service") ||
			strings.Contains(f[0], "@") || servicesExclude.MatchString(f[0]) {
			continue
		}
		units = append(units, f[0])
	}
	sort.Strings(units)
	return slices.Compact(units)
}

// enabledServices keeps the units `systemctl is-enabled` reports as exactly
// "enabled" (not enabled-runtime, static, masked, …). One batched call prints
// one state per unit; if the line count doesn't match (a unit systemctl
// couldn't resolve prints nothing on stdout) it falls back to one call each.
func enabledServices(units []string) []string {
	if len(units) == 0 {
		return nil
	}
	out, _ := proc.Query(context.Background(), "systemctl", append([]string{"is-enabled", "--"}, units...)...)
	states := nonEmptyLines(string(out))
	if len(states) != len(units) {
		states = states[:0]
		for _, u := range units {
			o, _ := proc.Query(context.Background(), "systemctl", "is-enabled", "--", u)
			states = append(states, strings.TrimSpace(string(o)))
		}
	}
	var enabled []string
	for i, u := range units {
		if states[i] == "enabled" {
			enabled = append(enabled, u)
		}
	}
	return enabled
}

// discoverServices finds services that are enabled but not running. Listing
// loaded units is ~3 ms where `list-unit-files` costs ~430 ms, so it asks for
// the inactive units first and checks only those with one is-enabled call.
func discoverServices() tea.Cmd {
	return func() tea.Msg {
		out, _ := proc.Query(context.Background(), "systemctl", "list-units", "--type=service", "--all",
			"--state=inactive", "--plain", "--no-legend")
		svcs := enabledServices(parseInactiveServices(string(out)))
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
			appendLog(logCategoryCleanup, "DRYRUN", "Edit topgrade config", "would open "+editorName()+" on "+path)
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
