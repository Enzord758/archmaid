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
// as plain children").
package proc

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Spec is one command to run.
type Spec struct {
	Name string   // the program, looked up in PATH
	Args []string // its arguments
	// Env is the child's whole environment; nil inherits archmaid's.
	Env []string
	// Display is what a dry run reports it would run; empty means Name and
	// Args joined by spaces.
	Display string
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
	// ("[dry-run] would run: <Display>"), then a done, and returns nil.
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
}

// Start runs spec and streams its combined stdout and stderr onto ch: every
// line in order, then exactly one Msg with Done set and the exit error. One
// ordered channel keeps the lines and the exit status strictly ordered — no
// interleaving between goroutines. The child is started synchronously, so the
// returned handle is usable at once; reading and waiting run on goroutines.
// It returns nil in a dry run or when the command can't start (the done
// carries that error).
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

	if r.opts.DryRun {
		go func() {
			if send(Msg{Line: DryRunPrefix + spec.display()}) {
				send(Msg{Done: true})
			}
		}()
		return nil
	}

	cmd := exec.Command(spec.Name, spec.Args...)
	cmd.Env = spec.Env
	// Bound the wait on the pipe once the child itself is gone (see
	// Options.WaitDelay). No SysProcAttr: the child must share archmaid's
	// session for the sudo ticket (see the package doc).
	cmd.WaitDelay = r.opts.WaitDelay

	// Fold stderr into stdout through a single pipe (the shell's `2>&1`).
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	if err := cmd.Start(); err != nil {
		go func() { send(Msg{Done: true, Err: err}) }()
		return nil
	}
	c := &Child{Pid: cmd.Process.Pid, p: cmd.Process, grace: r.opts.KillGrace}
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
func (c *Child) Stop() {
	if c == nil || c.p == nil {
		return
	}
	_ = c.p.Signal(os.Interrupt)
	go escalateKill(c.p, c.grace)
}

// escalateKill force-terminates p if it hasn't exited within grace. p.Kill()
// is a no-op (returns an ignored error) once Start's cmd.Wait has reaped the
// process, and never touches a reused pid.
func escalateKill(p *os.Process, grace time.Duration) {
	time.Sleep(grace)
	_ = p.Kill() // SIGKILL — cannot be caught or ignored
}
