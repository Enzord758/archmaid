package helperclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient/internal/seam"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ErrProtocol wraps every way the helper's output can break the protocol:
// a malformed or over-long line, a line without "v":1, an event out of
// order, a check event that doesn't add up, a result that disagrees with
// the exit status. The step fails closed.
var ErrProtocol = errors.New("helper protocol error")

// ErrNotGranted is Err when pkexec never ran the helper (Outcome.NotGranted).
var ErrNotGranted = errors.New("privilege not granted")

// noAgentText is what pkexec (polkit 127, src/programs/pkexec.c) prints
// when DisableInternalAgent is set and no agent is registered for its
// subject; Err then wraps engine.ErrNoAuthAgent too.
const noAgentText = "No authentication agent found"

// batchMax caps the lines in one StepOutput.
const batchMax = 256

// stderrMax is how much of pkexec's and the helper's stderr is kept (the
// tail), for the error when there is no result line.
const stderrMax = 4 << 10

// Outcome is how a helper step ended. Run has already emitted all of it
// the engine takes as Inputs; the rest is for logs and tests.
type Outcome struct {
	// Granted: the helper's start line arrived (privilege was granted).
	Granted bool
	// NotGranted: pkexec exited 126/127 with no output at all, so the
	// helper never ran (refused, cancelled, locked out) → AuthRefused.
	NotGranted bool
	// Exit is the process's exit status; -1 if it was killed by a signal
	// or never started.
	Exit int
	// Result is the helper's result line, if one arrived.
	Result *Result
	// Check is the helper's check event, kept only when the run succeeded
	// (Err == nil).
	Check *Check
	// Unknown counts events with an unknown "t", which are ignored.
	Unknown int
	// Err is nil only on success: a start line, a result line with ok and
	// code 0, exit 0, and no protocol error.
	Err error
}

// Run runs st through pkexec and the root helper, and emits what happens as
// engine Inputs, in order, on the calling goroutine:
//
//   - engine.PrivilegeGranted{seq} on the helper's start line;
//   - engine.StepOutput{seq, lines} for its output (line events, and the
//     result's skipped entries), sanitized, batched by what is already read;
//   - engine.HelperCheck{seq, broken} for check-files' check event, only
//     once the whole run has succeeded (so never on a failed or broken
//     stream: the engine then reads "no check event" and fails closed);
//   - last, exactly one engine.StepExited{seq, Err, NotGranted}.
//
// It returns when the child has exited and its output is drained.
//
// Cancelling ctx (Stop) kills pkexec only while the polkit dialog is up,
// that is before the helper's start line: the helper never ran, so the step
// is NotGranted ("stopped during the authentication dialog"). Once the
// start line has arrived the helper runs as root and can't be signalled by
// the user, so Run never kills it and never closes its output: it reads to
// EOF and waits, and the Outcome carries the helper's real Result. Err then
// says the step was stopped too late (ErrStoppedLate, wrapping what the
// helper did): a stopped step that went on to change the system must be
// logged as neither a success nor a protocol error.
//
// A step Argv refuses runs nothing and gets a StepExited with that error,
// as does one whose helper isn't installed (ErrHelperMissing).
func Run(ctx context.Context, seq uint64, st steps.Step, emit func(engine.Input)) Outcome {
	argv, err := Argv(st)
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: err})
	}
	if err := helperInstalled(); err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: err})
	}
	// stdout is a pipe of our own, not StdoutPipe: Wait never closes it,
	// and only watch, before the start line, ever does.
	pr, pw, err := os.Pipe()
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: fmt.Errorf("helper: %w", err)})
	}
	defer pr.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = pw
	stderr := &tail{max: stderrMax}
	cmd.Stderr = stderr
	// No Context, so this bounds only the stderr copy once the process has
	// exited; its stdout has been read to EOF by then.
	cmd.WaitDelay = stderrGrace
	err = cmd.Start()
	pw.Close()
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: fmt.Errorf("can't start %s: %w", Pkexec, err)})
	}

	g := &gate{}
	done := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		g.watch(ctx, done, cmd.Process, pr)
	}()

	d := newDecoder(st)
	var batch []string
	flush := func() {
		if len(batch) > 0 {
			emit(engine.StepOutput{Seq: seq, Lines: batch})
			batch = nil
		}
	}
	readLines(pr, d, func(lines []string, start bool) {
		if start {
			g.grant()
			emit(engine.PrivilegeGranted{Seq: seq})
		}
		if evs := d.takeParts(); len(evs) > 0 {
			flush() // the lines before belong to the part before
			for _, e := range evs {
				emit(e.input(seq))
			}
		}
		batch = append(batch, lines...)
	}, func(more bool) {
		if !more || len(batch) >= batchMax {
			flush()
		}
	})
	flush()
	waitErr := cmd.Wait()
	close(done)
	<-watched
	o := g.verdict(d.outcome(exitCode(waitErr), waitErr, stderr.String()))
	if o.Check != nil {
		emit(engine.HelperCheck{Seq: seq, Broken: slices.Clone(o.Check.Broken)})
	}
	return exited(seq, emit, o)
}

// ErrHelperMissing: the root helper isn't installed at HelperPath. pkexec
// would exit 127 for it, which reads as a refused password, so Run checks
// before it starts pkexec.
var ErrHelperMissing = errors.New("archmaid-helper is not installed")

// helperFile is the file helperInstalled checks: HelperPath, except in
// tests, where export_test.go (this package's) or helpertest (other
// packages', through seam) points it at a stand-in. It is never what Run
// starts: Argv always names HelperPath.
var helperFile = HelperPath

// Other packages' tests move helperFile through helpertest, by way of seam,
// which only internal/helperclient's own packages can import.
func init() {
	seam.SetHelperFile = func(path string) func() {
		old := helperFile
		helperFile = path
		return func() { helperFile = old }
	}
}

// helperInstalled is nil when helperFile is a regular file. It only saves
// a password prompt that can't succeed; polkit and the helper's own install
// check still decide what runs.
func helperInstalled() error {
	fi, err := os.Stat(helperFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w at %s", ErrHelperMissing, helperFile)
	case err != nil:
		return fmt.Errorf("can't check the root helper at %s: %w", helperFile, err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%w at %s (not a regular file)", ErrHelperMissing, helperFile)
	}
	return nil
}

// ErrStoppedLate wraps a step's Err when Stop came after the helper's start
// line: the root helper went on and finished its verb anyway.
var ErrStoppedLate = errors.New("stopped, but the root helper had already started")

// killGrace is how long, after pkexec was killed in the dialog, Run waits
// for its stdout to close before closing it itself (a child of pkexec's,
// such as a text agent, may hold it). The helper never ran then.
const killGrace = 2 * time.Second

// stderrGrace bounds the wait for stderr's EOF after the process exited.
const stderrGrace = 2 * time.Second

// gate decides whether a Stop may still kill pkexec: only before the start
// line. grant and watch take the same lock, so a start line read before
// the cancel always wins.
type gate struct {
	mu      sync.Mutex
	granted bool // the start line was read
	stop    bool // ctx was cancelled while the child ran
	killed  bool // pkexec was killed before the start line
}

func (g *gate) grant() {
	g.mu.Lock()
	g.granted = true
	g.mu.Unlock()
}

// watch waits for ctx or done. On a cancel before the start line it kills
// p, then closes r if its EOF doesn't come within killGrace. After the
// start line it only notes the stop.
func (g *gate) watch(ctx context.Context, done <-chan struct{}, p *os.Process, r *os.File) {
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	g.mu.Lock()
	g.stop = true
	if !g.granted {
		// The kill fails only when pkexec is gone, or has already become
		// the root helper (whose start line is then on its way).
		g.killed = p.Kill() == nil
	}
	killed := g.killed
	g.mu.Unlock()
	if !killed {
		return
	}
	select {
	case <-done:
	case <-time.After(killGrace):
		r.Close()
	}
}

// verdict is o, the decoder's outcome, adjusted for a Stop.
func (g *gate) verdict(o Outcome) Outcome {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.killed && !o.Granted:
		// pkexec died in the dialog: whatever its stream and exit say, the
		// helper never ran.
		return Outcome{NotGranted: true, Exit: o.Exit,
			Err: fmt.Errorf("%w: stopped during the authentication dialog", ErrNotGranted)}
	case g.stop && o.Granted:
		o.Check = nil
		if o.Err == nil {
			o.Err = fmt.Errorf("%w and finished: %s", ErrStoppedLate, resultText(o.Result))
		} else {
			o.Err = fmt.Errorf("%w: %w", ErrStoppedLate, o.Err)
		}
	}
	return o
}

// resultText is a successful result, for ErrStoppedLate's message.
func resultText(r *Result) string {
	s := fmt.Sprintf("ok (exit %d", r.Code)
	if r.ChildExit != nil {
		s += fmt.Sprintf(", child exit %d", *r.ChildExit)
	}
	if n := len(r.Skipped); n > 0 {
		s += fmt.Sprintf(", %d skipped", n)
	}
	return s + ")"
}

func exited(seq uint64, emit func(engine.Input), o Outcome) Outcome {
	emit(engine.StepExited{Seq: seq, Err: o.Err, NotGranted: o.NotGranted})
	return o
}

// readLines reads r line by line into d, never holding more than
// MaxLineBytes of one line: a longer line is skipped to its newline and
// fails the step. got gets each line's output; idle is called after each
// line with whether more input is already buffered (so a batch is flushed
// before the next read can block). It reads to EOF even after a protocol
// error, so the child never blocks on a full pipe.
func readLines(r io.Reader, d *decoder, got func([]string, bool), idle func(more bool)) {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	tooLong := false
	for {
		frag, err := br.ReadSlice('\n')
		if !tooLong && len(buf)+len(frag) > MaxLineBytes+1 { // +1: the newline
			tooLong, buf = true, nil
		}
		if !tooLong {
			buf = append(buf, frag...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil { // EOF or a read error: the stream is over
			if len(buf) > 0 || tooLong {
				d.fail("the output ends in an unterminated line")
			}
			if !errors.Is(err, io.EOF) {
				d.fail("reading the output: %v", err)
			}
			return
		}
		if tooLong {
			d.fail("a line over %d bytes", MaxLineBytes)
		} else {
			got(d.feed(buf[:len(buf)-1]))
		}
		buf, tooLong = buf[:0], false
		idle(br.Buffered() > 0)
	}
}

// outcome is the verdict once the child has exited with exit (waitErr
// from Wait) and stderr's tail.
func (d *decoder) outcome(exit int, waitErr error, stderr string) Outcome {
	o := Outcome{Granted: d.granted, Exit: exit, Result: d.res, Unknown: d.unknown}
	switch {
	case d.err != nil:
		o.Err = d.err
	case !d.granted && d.verb != steps.VerbPlanOrphans && (exit == exitNotAuthorized || exit == exitDismissed):
		// No output at all (any line would have been a start line or a
		// protocol error): pkexec never ran the helper. (plan-orphans runs
		// without pkexec, so for it these are just exits.)
		o.NotGranted = true
		o.Err = fmt.Errorf("%w: %s exited %d", ErrNotGranted, Pkexec, exit)
		if exit == exitNotAuthorized && strings.Contains(stderr, noAgentText) {
			// DisableInternalAgent: nobody could ask for the password.
			o.Err = fmt.Errorf("%w: %w (%s exited %d)", ErrNotGranted, engine.ErrNoAuthAgent, Pkexec, exit)
		}
	case !d.granted:
		o.Err = fmt.Errorf("the root helper never started (%s)%s", exitText(exit, waitErr), stderrText(stderr))
	case d.res == nil:
		o.Err = fmt.Errorf("the root helper ended without a result (%s)%s", exitText(exit, waitErr), stderrText(stderr))
	case d.res.Code != exit:
		o.Err = fmt.Errorf("%w: result code %d, but the helper exited %s", ErrProtocol, d.res.Code, exitText(exit, waitErr))
	case d.comb != nil && (d.comb.done > 0 || d.res.OK):
		o.Err = d.combinedOutcome()
	case d.verb == steps.VerbRemoveOrphans && d.res.Code == exitStale && d.res.ChildExit == nil:
		// The plan, recomputed as root, no longer hashes to the confirmed
		// one, and no root command ran.
		o.Err = ErrOrphansChanged
	case !d.res.OK:
		msg := d.res.Error
		if msg == "" {
			msg = "failed"
		}
		o.Err = fmt.Errorf("root helper (exit %d): %s", d.res.Code, sanitize.Clip(msg, 300))
	default:
		o.Check = d.check
	}
	return o
}

func exitText(exit int, waitErr error) string {
	if exit >= 0 {
		return fmt.Sprintf("exit %d", exit)
	}
	if waitErr != nil {
		return sanitize.Clip(sanitize.OneLine(waitErr.Error()), 100)
	}
	return "killed"
}

// stderrText is stderr's last non-empty line, for an error message.
func stderrText(s string) string {
	lines := []string{}
	for _, l := range strings.Split(s, "\n") {
		if l = sanitize.OneLine(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return ": " + sanitize.Clip(lines[len(lines)-1], 200)
}

// tail is an io.Writer that keeps the last max bytes written.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
