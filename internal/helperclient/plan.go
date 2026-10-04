package helperclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The orphan removal under polkit is two helper calls:
//
//	archmaid-helper plan-orphans          as the user, no pkexec (PlanOrphans)
//	pkexec archmaid-helper remove-orphans <hash>    (Run, Argv from st.Plan)
//
// plan-orphans reports the plan (the orphans, everything -Rns cascades
// into, and a hash committing to both); the confirm lists exactly those
// lists; remove-orphans gets only the hash, recomputes the plan as root and
// removes nothing unless it still hashes to it. Here the plan is checked
// twice, when it is read and again by Argv right before pkexec: names and
// versions must be what the helper itself accepts, and the hash must be the
// hash of the lists the confirm showed (PlanHash), so a hash can't authorize
// anything but what was shown.

// ErrBadPlan: a step's or the helper's orphan plan is malformed, its hash
// isn't the hash of its lists, or it isn't what the step's confirm showed.
var ErrBadPlan = errors.New("helperclient: bad orphan plan")

// ErrOrphansChanged is the removal's error when the helper found a
// different plan than the confirmed one (it exited ExitStale having run
// nothing): the system changed between the confirm and the removal.
var ErrOrphansChanged = errors.New("The orphan list changed since you confirmed; nothing was removed")

// exitStale is the helper's ExitStale: state changed since the confirm.
const exitStale = 3

var (
	// hashPattern is the helper's plan hash, exactly.
	hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// versionPattern is the helper's: pacman's %v, [epoch:]pkgver-pkgrel.
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9._:+~-]{1,128}$`)
)

// PlanHash is the helper's canonical plan hash (its PlanHash; the two are
// tied by a test in cmd/archmaid-helper): sha256 over a fixed header, the
// sorted Direct as "target" lines and the sorted Cascade as "remove" lines.
func PlanHash(direct, cascade []string) string {
	t, it := slices.Sorted(slices.Values(direct)), slices.Sorted(slices.Values(cascade))
	h := sha256.New()
	fmt.Fprint(h, "archmaid orphan plan v1\n")
	for _, s := range t {
		fmt.Fprintf(h, "target %s\n", s)
	}
	for _, s := range it {
		fmt.Fprintf(h, "remove %s\n", s)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// CheckPlan reports whether p is a plan the helper could have made: a
// well-formed hash; Direct package names and Cascade "name version" lines
// as the helper's patterns allow them, none twice; and Hash the PlanHash of
// exactly those lists. An empty plan (no orphans) is valid.
func CheckPlan(p steps.OrphanPlan) error {
	if !hashPattern.MatchString(p.Hash) {
		return fmt.Errorf("%w: hash %q is not sha256:<64 hex>", ErrBadPlan, sanitize.Field(p.Hash, 80))
	}
	seen := map[string]bool{}
	for _, n := range p.Direct {
		if !namePattern.MatchString(n) {
			return fmt.Errorf("%w: invalid package name %q", ErrBadPlan, sanitize.Field(n, 60))
		}
		if seen[n] {
			return fmt.Errorf("%w: %q listed twice", ErrBadPlan, n)
		}
		seen[n] = true
	}
	seen = map[string]bool{}
	for _, l := range p.Cascade {
		name, ver, ok := strings.Cut(l, " ")
		if !ok || !namePattern.MatchString(name) || !versionPattern.MatchString(ver) {
			return fmt.Errorf("%w: invalid cascade line %q", ErrBadPlan, sanitize.Field(l, 80))
		}
		if seen[l] {
			return fmt.Errorf("%w: %q listed twice", ErrBadPlan, l)
		}
		seen[l] = true
	}
	if PlanHash(p.Direct, p.Cascade) != p.Hash {
		return fmt.Errorf("%w: the hash is not the hash of the listed packages", ErrBadPlan)
	}
	return nil
}

// orphanArgs is remove-orphans' one argument, the plan hash, from st.Plan
// only: the plan must pass CheckPlan, remove something, and be exactly what
// the confirm lists (st.Items and st.Cascade, which the engine shows).
func orphanArgs(st steps.Step) ([]string, error) {
	p := st.Plan
	if p == nil {
		return nil, fmt.Errorf("%w: %s needs the plan from plan-orphans (step %q has none)",
			ErrUnsupported, st.Verb, st.ID)
	}
	if err := CheckPlan(*p); err != nil {
		return nil, err
	}
	if len(p.Direct) == 0 {
		return nil, fmt.Errorf("%w: the plan removes nothing", ErrBadPlan)
	}
	if !slices.Equal(st.Items, p.Direct) || !slices.Equal(st.Cascade, p.Cascade) {
		return nil, fmt.Errorf("%w: the step's confirm lists are not the plan's", ErrBadPlan)
	}
	return []string{p.Hash}, nil
}

// PlanOrphans runs the root helper's plan-orphans as the calling user —
// directly, never through pkexec: it is read-only and needs no root — and
// returns its plan, checked (CheckPlan). Any failure (the helper's, a
// protocol error, a missing or invalid plan event) is an error, and no
// removal may be offered then. It is what a front end hands the actions
// as Env.PlanOrphans under polkit.
func PlanOrphans(ctx context.Context) (steps.OrphanPlan, error) {
	return planOrphans(ctx, HelperPath)
}

func planOrphans(ctx context.Context, helper string) (steps.OrphanPlan, error) {
	cmd := exec.CommandContext(ctx, helper, steps.VerbPlanOrphans)
	cmd.WaitDelay = 5 * time.Second
	stderr := &tail{max: stderrMax}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return steps.OrphanPlan{}, fmt.Errorf("plan-orphans: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return steps.OrphanPlan{}, fmt.Errorf("can't start the root helper's plan-orphans: %w", err)
	}
	d := newDecoder(steps.Step{Verb: steps.VerbPlanOrphans})
	readLines(stdout, d, func([]string, bool) {}, func(bool) {})
	waitErr := cmd.Wait()
	return d.planResult(exitCode(waitErr), waitErr, stderr.String())
}

// ParsePlan reads a whole plan-orphans output stream (its JSON lines, as
// from a run that exited 0) with the parser PlanOrphans uses. It is for
// tests that tie the helper's output to this parser.
func ParsePlan(r io.Reader) (steps.OrphanPlan, error) {
	d := newDecoder(steps.Step{Verb: steps.VerbPlanOrphans})
	readLines(r, d, func([]string, bool) {}, func(bool) {})
	return d.planResult(0, nil, "")
}

// planResult is plan-orphans' verdict: the run must succeed like any helper
// step and have reported exactly one valid plan.
func (d *decoder) planResult(exit int, waitErr error, stderr string) (steps.OrphanPlan, error) {
	o := d.outcome(exit, waitErr, stderr)
	switch {
	case o.Err != nil:
		return steps.OrphanPlan{}, fmt.Errorf("plan-orphans: %w", o.Err)
	case d.plan == nil:
		return steps.OrphanPlan{}, fmt.Errorf("plan-orphans: %w: no plan event", ErrProtocol)
	}
	return *d.plan, nil
}

// readPlan takes plan-orphans' plan event: at most one, every field
// present, total the cascade's length, and the plan valid (CheckPlan).
// remove-orphans reports the current plan when it finds the confirmed one
// stale; that one is only informative and not kept. Other verbs' plan
// events are ignored, as before.
func (d *decoder) readPlan(line []byte) {
	if d.verb != steps.VerbPlanOrphans {
		return
	}
	var e struct {
		Targets *[]string `json:"targets"`
		Items   *[]string `json:"items"`
		Total   *int      `json:"total"`
		Hash    *string   `json:"hash"`
	}
	if !d.decode(line, &e) {
		return
	}
	switch {
	case d.plan != nil:
		d.fail("a second plan event")
		return
	case e.Targets == nil || e.Items == nil || e.Total == nil || e.Hash == nil:
		d.fail("a plan event without targets, items, total and hash")
		return
	case *e.Total != len(*e.Items):
		d.fail("plan event: total %d, but %d items", *e.Total, len(*e.Items))
		return
	}
	p := steps.OrphanPlan{Hash: *e.Hash, Direct: slices.Clone(*e.Targets), Cascade: slices.Clone(*e.Items)}
	if err := CheckPlan(p); err != nil {
		d.fail("plan event: %v", err)
		return
	}
	d.plan = &p
}

// exitCode is a Wait error's exit status: 0 for nil, -1 when the process
// was killed or never ran.
func exitCode(waitErr error) int {
	var ee *exec.ExitError
	switch {
	case waitErr == nil:
		return 0
	case errors.As(waitErr, &ee):
		return ee.ExitCode()
	}
	return -1
}
