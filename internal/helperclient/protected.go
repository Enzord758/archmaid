package helperclient

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// maxProtected caps the names of a protected event: the static list, the
// kernels, base's dependencies and the installed archmaid* packages are
// far below it.
const maxProtected = 4096

// Protected runs the helper's plan-protected as the calling user (directly,
// never through pkexec: it is read-only and has no polkit action) and
// returns the protected package names: the ones the removal verbs refuse,
// for a front end to grey up front. Any failure (the helper's, a protocol
// error, a missing or invalid event) is an error; a front end then greys
// nothing extra and the removal verbs still enforce the list.
func Protected(ctx context.Context) ([]string, error) {
	return protectedNames(ctx, HelperPath)
}

func protectedNames(ctx context.Context, helper string) ([]string, error) {
	cmd := exec.CommandContext(ctx, helper, steps.VerbPlanProtected)
	cmd.WaitDelay = 5 * time.Second
	stderr := &tail{max: stderrMax}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("plan-protected: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("can't start the root helper's plan-protected: %w", err)
	}
	d := newDecoder(steps.Step{Verb: steps.VerbPlanProtected})
	readLines(stdout, d, func([]string, bool) {}, func(bool) {})
	waitErr := cmd.Wait()
	return d.protectedResult(exitCode(waitErr), waitErr, stderr.String())
}

// ParseProtected reads a whole plan-protected output stream, which exited
// with exit, with the parser Protected uses, for tests that tie the
// helper's output to this parser.
func ParseProtected(r io.Reader, exit int) ([]string, error) {
	d := newDecoder(steps.Step{Verb: steps.VerbPlanProtected})
	readLines(r, d, func([]string, bool) {}, func(bool) {})
	return d.protectedResult(exit, nil, "")
}

// protectedResult is plan-protected's verdict: the run must succeed like
// any helper step and have reported exactly one valid event.
func (d *decoder) protectedResult(exit int, waitErr error, stderr string) ([]string, error) {
	o := d.outcome(exit, waitErr, stderr)
	switch {
	case o.Err != nil:
		return nil, fmt.Errorf("plan-protected: %w", o.Err)
	case d.prot == nil:
		return nil, fmt.Errorf("plan-protected: %w: no protected event", ErrProtocol)
	}
	return slices.Clone(*d.prot), nil
}

// readProtected takes plan-protected's event: only from that verb, at most
// once, "names" present (an array), each a package name, none twice.
func (d *decoder) readProtected(line []byte) {
	var e struct {
		Names *[]string `json:"names"`
	}
	if !d.decode(line, &e) {
		return
	}
	switch {
	case d.verb != steps.VerbPlanProtected:
		d.fail("a protected event from %s", d.verb)
		return
	case d.prot != nil:
		d.fail("a second protected event")
		return
	case e.Names == nil:
		d.fail("a protected event without names")
		return
	case len(*e.Names) > maxProtected:
		d.fail("protected event: %d names", len(*e.Names))
		return
	}
	seen := make(map[string]bool, len(*e.Names))
	for _, n := range *e.Names {
		if !namePattern.MatchString(n) {
			d.fail("protected event: invalid package name %q", sanitize.Field(n, 60))
			return
		}
		if seen[n] {
			d.fail("protected event: %q listed twice", n)
			return
		}
		seen[n] = true
	}
	d.prot = e.Names
}
