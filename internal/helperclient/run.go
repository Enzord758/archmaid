package helperclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
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
// Cancelling ctx kills pkexec while the dialog is up; once granted, the
// helper runs as root and can't be signalled by the user, so Run waits for
// it (the helper never cuts a running pacman short anyway). A step Argv
// refuses runs nothing and gets a StepExited with that error.
func Run(ctx context.Context, seq uint64, st steps.Step, emit func(engine.Input)) Outcome {
	argv, err := Argv(st)
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: err})
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 5 * time.Second
	stderr := &tail{max: stderrMax}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: fmt.Errorf("helper: %w", err)})
	}
	if err := cmd.Start(); err != nil {
		return exited(seq, emit, Outcome{Exit: -1, Err: fmt.Errorf("can't start %s: %w", Pkexec, err)})
	}

	d := newDecoder(st)
	var batch []string
	flush := func() {
		if len(batch) > 0 {
			emit(engine.StepOutput{Seq: seq, Lines: batch})
			batch = nil
		}
	}
	readLines(stdout, d, func(lines []string, start bool) {
		if start {
			emit(engine.PrivilegeGranted{Seq: seq})
		}
		batch = append(batch, lines...)
	}, func(more bool) {
		if !more || len(batch) >= batchMax {
			flush()
		}
	})
	flush()
	waitErr := cmd.Wait()
	o := d.outcome(exitCode(waitErr), waitErr, stderr.String())
	if o.Check != nil {
		emit(engine.HelperCheck{Seq: seq, Broken: slices.Clone(o.Check.Broken)})
	}
	return exited(seq, emit, o)
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
	case !d.granted:
		o.Err = fmt.Errorf("the root helper never started (%s)%s", exitText(exit, waitErr), stderrText(stderr))
	case d.res == nil:
		o.Err = fmt.Errorf("the root helper ended without a result (%s)%s", exitText(exit, waitErr), stderrText(stderr))
	case d.res.Code != exit:
		o.Err = fmt.Errorf("%w: result code %d, but the helper exited %s", ErrProtocol, d.res.Code, exitText(exit, waitErr))
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
