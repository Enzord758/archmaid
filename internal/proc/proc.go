// Package proc runs archmaid's child processes: a step's command, streamed
// line by line onto a channel (Runner.Start, Next), and stopped on abort
// (Child.Stop); short read-only queries whose stdout is the result (Query);
// and the /proc lookups that tie a process to the step that started it
// (Read). It knows nothing about the UI: the TUI wraps Next in a tea.Cmd.
//
// Children run as plain children of archmaid — same session, same process
// group, no Setsid/Setpgid/Setctty. sudo keys its ticket to the tty and the
// session, so one primed ticket covers every step and everything they spawn
// only while they share archmaid's session. See docs/HANDOVER.md ("Steps run
// as plain children") and TestNoSetsid, which enforces it.
//
// The GUI is the exception (Options.Detach): it has no terminal the user
// sees, so its children run in a session of their own, without one, and
// their sudo can only prompt through SUDO_ASKPASS.
package proc

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

// Spec is one command to run.
type Spec struct {
	Name string   // the program, looked up in PATH
	Args []string // its arguments
	// Env is the child's whole environment; nil inherits archmaid's, minus
	// any SUDO_ASKPASS (see env).
	Env []string
	// Display is what a dry run reports it would run; empty means Name and
	// Args joined by spaces.
	Display string
}

// ErrSudoMayPrompt refuses a sudo that could ask for a password on the
// terminal: one whose first argument is neither -n nor -A (see CheckSudo).
var ErrSudoMayPrompt = errors.New("refusing to run sudo without -n or -A: it could prompt on the terminal")

// isSudo reports whether name runs sudo or sudo-rs (by name or by path).
func isSudo(name string) bool {
	switch filepath.Base(name) {
	case "sudo", "sudo-rs":
		return true
	}
	return false
}

// shells are the shells whose arguments CheckSudo searches for sudo.
var shells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "mksh": true, "fish": true}

// maxWrap bounds how deep CheckSudo follows wrappers (env in a shell in env…).
const maxWrap = 8

// CheckSudo refuses a sudo command (name "sudo" or "sudo-rs", or a path to
// one) whose first argument doesn't keep it off the terminal: -n or
// --non-interactive (never prompt), -A or --askpass (prompt through
// SUDO_ASKPASS), or a cluster of short flags with one of them, such as -nA.
// Any other sudo reads a password from the terminal, under a full-screen UI,
// where the user can't see the prompt and every keystroke goes to sudo. Only
// privilege.Sudo.Command builds the allowed forms.
//
// The same rule holds for a sudo behind env (env [VAR=…] sudo …) and for
// every sudo named in a shell's arguments (sh -c '… sudo …'). The check
// covers direct invocations only: a sudo reached through a script file, a
// variable or any other wrapper (nice, timeout, …) isn't seen. Any other
// command passes.
func CheckSudo(name string, args []string) error { return checkSudo(name, args, 0) }

func checkSudo(name string, args []string, depth int) error {
	if depth > maxWrap {
		return ErrSudoMayPrompt
	}
	switch base := filepath.Base(name); {
	case isSudo(name):
		if sudoWontPrompt(args) {
			return nil
		}
		return ErrSudoMayPrompt
	case base == "env":
		if cmd, rest := envCommand(args); cmd != "" {
			return checkSudo(cmd, rest, depth+1)
		}
	case shells[base]:
		return checkShellWords(shellWords(args), depth+1)
	}
	return nil
}

// sudoValueFlags are sudo's short options that take a value: in a cluster
// such as -nu, everything after one is its value, not more flags.
const sudoValueFlags = "aCcDghpRrTtUu"

// sudoWontPrompt reports whether sudo's first argument keeps it off the
// terminal. -S (password from stdin) cancels -A in the same cluster; only the
// first argument counts, since a later -n could be another option's value
// (sudo -p -n reads the prompt "-n").
func sudoWontPrompt(args []string) bool {
	if len(args) == 0 {
		return false
	}
	a := args[0]
	if a == "--non-interactive" || a == "--askpass" {
		return true
	}
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	askpass, stdin := false, false
	for _, c := range a[1:] {
		switch {
		case c == 'n':
			return true
		case c == 'A':
			askpass = true
		case c == 'S':
			stdin = true
		case strings.ContainsRune(sudoValueFlags, c):
			return askpass && !stdin // the rest is c's value
		}
	}
	return askpass && !stdin
}

// envCommand finds the command env would run: past its options, their
// values and VAR=value assignments. A -S/--split-string value is split into
// words that come first, as env does. "" means env runs no command.
func envCommand(args []string) (cmd string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return args[i+1], args[i+2:]
			}
			return "", nil
		case a == "-S" || a == "--split-string":
			if i+1 < len(args) {
				return envCommand(append(shellWords(args[i+1:i+2]), args[i+2:]...))
			}
			return "", nil
		case strings.HasPrefix(a, "--split-string="):
			return envCommand(append(shellWords([]string{strings.TrimPrefix(a, "--split-string=")}), args[i+1:]...))
		case strings.HasPrefix(a, "-S") && !strings.HasPrefix(a, "--"):
			return envCommand(append(shellWords([]string{a[2:]}), args[i+1:]...))
		case a == "-u" || a == "-C" || a == "-a" || a == "--unset" || a == "--chdir" || a == "--argv0":
			i++ // skip the value
		case strings.HasPrefix(a, "-"), strings.Contains(a, "="):
			// another option, a value attached to one, or an assignment
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// shellWords splits shell text into rough words: quotes and backslashes are
// dropped, and whitespace and the operators ; & | ( ) < > ` $ { } end a word.
// It isn't a shell parser; it only has to find each sudo and the word after
// it.
func shellWords(args []string) []string {
	unquote := strings.NewReplacer(`'`, "", `"`, "", `\`, "")
	var words []string
	for _, a := range args {
		words = append(words, strings.FieldsFunc(unquote.Replace(a), func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(";&|()<>`${}", r)
		})...)
	}
	return words
}

// checkShellWords holds every sudo among words, and every env, to the rule.
func checkShellWords(words []string, depth int) error {
	for i, w := range words {
		if isSudo(w) || filepath.Base(w) == "env" {
			if err := checkSudo(w, words[i+1:], depth); err != nil {
				return err
			}
		}
	}
	return nil
}

// env is the environment spec's child gets: Env, or for a nil Env
// archmaid's own without SUDO_ASKPASS — whatever the command, so no `sudo -A`
// behind env, a shell or a tool that runs sudo itself picks up a helper the
// user's shell exported. That helper is code archmaid knows nothing about;
// archmaid gives its own wrapper explicitly, to the steps that need it. (An
// explicit Env is the caller's, e.g. privilege.Sudo.Env with that wrapper.)
func (s Spec) env() []string {
	if s.Env != nil {
		return s.Env
	}
	environ := os.Environ()
	env := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "SUDO_ASKPASS=") {
			env = append(env, kv)
		}
	}
	return env
}

func (s Spec) display() string {
	if s.Display != "" {
		return s.Display
	}
	return strings.Join(append([]string{s.Name}, s.Args...), " ")
}

// Options are the knobs a Runner starts children with.
type Options struct {
	// DryRun starts nothing: Start reports what it would run as one line
	// ("[dry-run] would run: <Display>"), then a done, and returns nil. A
	// sudo that could prompt is refused (CheckSudo) just as in a real run.
	DryRun bool
	// WaitDelay bounds how long the output pipe is still read after the
	// child exits (exec.Cmd.WaitDelay). Anything the child started in the
	// background — a daemon, a helper topgrade leaves running — inherits
	// the pipe and can hold it open for good; without a bound the child's
	// done never came. Whatever the child wrote before exiting is read
	// within that window; then the pipe is closed, and a background process
	// writing to it later gets SIGPIPE/EPIPE like any writer to a closed
	// pipe. Zero waits for the pipe without bound.
	WaitDelay time.Duration
	// KillGrace is how long a stopped child gets to honour SIGINT before
	// it is SIGKILLed (see Child.Stop).
	KillGrace time.Duration
	// Detach starts the child in a session of its own, with no controlling
	// terminal (detachAttr), and makes Stop signal its whole process group.
	// Its stdin is /dev/null, as every child's. Only the GUI sets it: a GUI
	// started from a shell still has that shell's terminal, and sudo
	// prefers a terminal to SUDO_ASKPASS, so a child sharing it prompted
	// there, out of sight. The TUI's children must share its session (the
	// package doc), so the TUI never sets it.
	Detach bool
}

// DryRunPrefix starts the one line a dry run reports.
const DryRunPrefix = "[dry-run] would run: "

// Runner starts children with one set of Options.
type Runner struct{ opts Options }

// New returns a Runner for opts. Build it where the options are read (the
// UI goroutine), so a goroutine Start spawns never reads a setting a test may
// be swapping.
func New(opts Options) Runner { return Runner{opts: opts} }

// Child is a started process.
type Child struct {
	Pid int
	// Start is the child's start time (Info.Start), read before it can be
	// reaped and its pid reused, so a later request can be tied to this very
	// process. Zero if it couldn't be read.
	Start uint64

	p     *os.Process
	grace time.Duration
	// group: the child leads a process group of its own (Options.Detach),
	// which Stop signals as a whole. escalate starts the group's SIGKILL
	// timer once; groupGone is closed when it is done (GroupStopped).
	group     bool
	escalate  sync.Once
	groupGone chan struct{}
	// reaped is closed once cmd.Wait has returned, so Stop's escalation can
	// stop waiting instead of sleeping out the whole grace.
	reaped chan struct{}
}

// Start runs spec and streams its combined stdout and stderr onto ch: every
// line in order, then exactly one Msg with Done set and the exit error. One
// ordered channel keeps the lines and the exit status strictly ordered — no
// interleaving between goroutines. The child is started synchronously, so the
// returned handle is usable at once; reading and waiting run on goroutines.
// It returns nil in a dry run or when the command can't start (the done
// carries that error) — which includes a sudo that could prompt on the
// terminal (CheckSudo: ErrSudoMayPrompt), refused before anything runs.
//
// quit is the abort signal: once it is closed nobody reads ch anymore, so
// every pending send gives up instead of blocking, and the rest of the
// child's output is drained and discarded. Without that, a full ch blocked
// the scanner, the scanner blocked the pipe copier, and the copier blocked
// cmd.Wait — leaking all three goroutines per aborted step. A nil quit never
// fires.
//
// A clean exit is a success even when something the child left running still
// held the pipe at WaitDelay (exec.ErrWaitDelay is not reported); a failing
// exit keeps its own *exec.ExitError.
func (r Runner) Start(spec Spec, ch chan<- Msg, quit <-chan struct{}) *Child {
	send := func(msg Msg) bool {
		select {
		case ch <- msg:
			return true
		case <-quit:
			return false
		}
	}

	// Before the dry run: a dry run never reports "would run" for a sudo the
	// real run refuses.
	if err := CheckSudo(spec.Name, spec.Args); err != nil {
		go func() { send(Msg{Done: true, Err: err}) }()
		return nil
	}

	if r.opts.DryRun {
		go func() {
			if send(Msg{Line: DryRunPrefix + spec.display()}) {
				send(Msg{Done: true})
			}
		}()
		return nil
	}
	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Env = spec.env()
	// Bound the wait on the pipe once the child itself is gone (see
	// Options.WaitDelay). No SysProcAttr unless detached: the TUI's child
	// must share archmaid's session for the sudo ticket (see the package
	// doc). Stdin stays nil, which is /dev/null.
	cmd.WaitDelay = r.opts.WaitDelay
	if r.opts.Detach {
		cmd.SysProcAttr = detachAttr()
	}

	// Fold stderr into stdout through a single pipe (the shell's `2>&1`).
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	if err := cmd.Start(); err != nil {
		go func() { send(Msg{Done: true, Err: err}) }()
		return nil
	}
	c := &Child{Pid: cmd.Process.Pid, p: cmd.Process, grace: r.opts.KillGrace, reaped: make(chan struct{})}
	if r.opts.Detach {
		c.group, c.groupGone = true, make(chan struct{})
	}
	if info, err := Read(c.Pid); err == nil { // not reaped yet: Wait runs below
		c.Start = info.Start
	}

	go func() {
		// Scan on a goroutine so cmd.Wait can proceed. close(scanned) lets us
		// guarantee every line is sent before the done message.
		scanned := make(chan struct{})
		go func() {
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines (pacman)
			for sc.Scan() {
				if !send(Msg{Line: sc.Text()}) {
					break // aborted: nobody is reading anymore
				}
			}
			// Keep the pipe drained until the child is gone (aborted, or a
			// line over the scanner's limit), so it never blocks on a write
			// and cmd.Wait can return.
			io.Copy(io.Discard, pr)
			close(scanned)
		}()

		err := cmd.Wait()
		close(c.reaped)
		if errors.Is(err, exec.ErrWaitDelay) {
			// The child exited 0; only something it left running still
			// held the pipe. The command itself succeeded.
			err = nil
		}
		pw.Close() // unblocks the scanner with EOF
		<-scanned  // all lines are on the channel before we report done
		send(Msg{Done: true, Err: err})
	}()

	return c
}

// Stop asks the child to stop with SIGINT, like a terminal's ctrl+c, so tools
// such as pacman exit cleanly (sudo relays it to the command it runs), and
// SIGKILLs it if it is still there after the Runner's KillGrace. It returns at
// once. A nil Child, or one with no process behind it, is a no-op. Stopping
// does not release Start's goroutines; closing its quit does.
//
// A detached child (Options.Detach) is stopped with its whole process
// group, as a terminal's ctrl+c stops its foreground job: SIGINT to the
// group, and after the grace SIGKILL to whatever of it is left, the child
// or something it started that outlived it.
func (c *Child) Stop() {
	if c == nil || c.p == nil {
		return
	}
	if c.group {
		c.signalGroup(syscall.SIGINT)
		c.escalate.Do(func() {
			go func() {
				defer close(c.groupGone)
				c.escalateGroup()
			}()
		})
		return
	}
	_ = c.p.Signal(os.Interrupt)
	go escalateKill(c.p, c.grace, c.reaped)
}

// GroupStopped is closed once a Stop of a detached child has run its
// course: nothing is left in its group, or what was got SIGKILL. A Driver
// waits for it in Close, so nothing a step left behind outlives the GUI.
// It is closed at once for a Child that isn't detached (or nil); for a
// detached one it is never closed before Stop.
func (c *Child) GroupStopped() <-chan struct{} {
	if c == nil || !c.group || c.groupGone == nil {
		return closedCh
	}
	return c.groupGone
}

var closedCh = func() chan struct{} { ch := make(chan struct{}); close(ch); return ch }()

// escalateGroup is escalateKill for a detached child's group: SIGKILL to
// the group after the grace. It returns early only when the child is
// reaped and nothing is left in its group.
func (c *Child) escalateGroup() {
	t := time.NewTimer(c.grace)
	defer t.Stop()
	select {
	case <-c.reaped:
		if !groupAlive(c.Pid) {
			return
		}
		<-t.C
	case <-t.C:
		_ = c.p.Kill() // the child itself, even if it left its group
	}
	c.signalGroup(syscall.SIGKILL)
}

// escalateKill force-terminates p if it hasn't exited within grace. p.Kill()
// is a no-op (returns an ignored error) once Start's cmd.Wait has reaped the
// process, and never touches a reused pid. It returns as soon as reaped is
// closed, so the goroutine does not outlive the child by up to grace.
func escalateKill(p *os.Process, grace time.Duration, reaped <-chan struct{}) {
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-reaped:
	case <-t.C:
		_ = p.Kill() // SIGKILL — cannot be caught or ignored
	}
}
