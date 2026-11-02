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
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's two helper calls (docs/design/packages-contract.md,
// section 3):
//
//	archmaid-helper plan-install [--asdeps] name...      as the user, no pkexec (PlanPackages)
//	pkexec archmaid-helper pkg-install H [--asdeps] name...   (Run, Argv from st.PkgPlan)
//	archmaid-helper plan-remove [-n] name...             as the user
//	pkexec archmaid-helper pkg-remove H [-n] name...
//
// as plan-orphans / remove-orphans: the plan call reports the transaction
// and its hash, the root call recomputes it under lock and runs nothing
// unless it still hashes to H.

// MaxPkgNames is the most names one Packages call takes (the helper's
// MaxPkgNames; the GUI's install list is capped at it).
const MaxPkgNames = 32

// The helper's fixed option tokens.
const (
	flagAsDeps     = "--asdeps"
	flagWithConfig = "-n"
)

var (
	// repoPattern is a sync repository's name as the client accepts it.
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9@._+-]{1,64}$`)
	// reasonPattern is a refusal reason as the client accepts it.
	reasonPattern = regexp.MustCompile(`^[a-z-]{1,32}$`)
)

func isPkgPlanVerb(v string) bool { return v == steps.VerbPlanInstall || v == steps.VerbPlanRemove }
func isPkgRunVerb(v string) bool  { return v == steps.VerbPkgInstall || v == steps.VerbPkgRemove }

// PkgPlanHash is the canonical hash of a Packages plan, the helper's
// PkgPlanHash (Dev 3, 52cc27b) byte for byte: sha256 over
//
//	archmaid pkg plan v1
//	mode install|install-asdeps|remove|remove-n
//	target <name>        sorted
//	item <item>          sorted; install "repo name version", remove "name version"
//
// Sizes, OldVersion, Target flags and warnings are not part of it. The
// "install-asdeps" spelling is this contract's for the helper's asdeps
// variant, which Dev 3 adds (#231b).
func PkgPlanHash(p steps.PkgPlan) string {
	mode := "install"
	switch {
	case p.Mode == steps.PkgRemove && p.WithConfig:
		mode = "remove-n"
	case p.Mode == steps.PkgRemove:
		mode = "remove"
	case p.AsDeps:
		mode = "install-asdeps"
	}
	items := make([]string, len(p.Rows))
	for i, r := range p.Rows {
		items[i] = r.Name + " " + r.Version
		if p.Mode == steps.PkgInstall {
			items[i] = r.Repo + " " + items[i]
		}
	}
	slices.Sort(items)
	h := sha256.New()
	fmt.Fprint(h, "archmaid pkg plan v1\n")
	fmt.Fprintf(h, "mode %s\n", mode)
	for _, t := range slices.Sorted(slices.Values(p.Targets)) {
		fmt.Fprintf(h, "target %s\n", t)
	}
	for _, it := range items {
		fmt.Fprintf(h, "item %s\n", it)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// CheckPkgPlan reports whether p is a plan the helper could have made:
// 1–MaxPkgNames valid, distinct Targets; Rows with valid distinct names,
// versions and (install only) repos, every target a Target row and no
// other row one; no negative size; install has no OldVersion and no
// WithConfig, remove no AsDeps and no repo; Hash the PkgPlanHash of it.
func CheckPkgPlan(p steps.PkgPlan) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadPlan, fmt.Sprintf(format, a...))
	}
	if !hashPattern.MatchString(p.Hash) {
		return bad("hash %q is not sha256:<64 hex>", sanitize.Field(p.Hash, 80))
	}
	if p.Mode != steps.PkgInstall && p.Mode != steps.PkgRemove {
		return bad("unknown mode %d", p.Mode)
	}
	if p.Mode == steps.PkgInstall && p.WithConfig || p.Mode == steps.PkgRemove && p.AsDeps {
		return bad("an option that does not belong to this mode")
	}
	if n := len(p.Targets); n == 0 || n > MaxPkgNames {
		return bad("%d targets, want 1–%d", n, MaxPkgNames)
	}
	isTarget := map[string]bool{}
	for _, n := range p.Targets {
		if !namePattern.MatchString(n) {
			return bad("invalid package name %q", sanitize.Field(n, 60))
		}
		if isTarget[n] {
			return bad("%q targeted twice", n)
		}
		isTarget[n] = true
	}
	seen := map[string]bool{}
	for _, r := range p.Rows {
		switch {
		case !namePattern.MatchString(r.Name):
			return bad("invalid package name %q", sanitize.Field(r.Name, 60))
		case !versionPattern.MatchString(r.Version):
			return bad("invalid version %q", sanitize.Field(r.Version, 60))
		case r.OldVersion != "" && !versionPattern.MatchString(r.OldVersion):
			return bad("invalid old version %q", sanitize.Field(r.OldVersion, 60))
		case seen[r.Name]:
			return bad("%q listed twice", r.Name)
		case r.Download < 0 || r.Installed < 0:
			return bad("%q has a negative size", r.Name)
		case r.Target != isTarget[r.Name]:
			return bad("%q: target flag disagrees with the targets", r.Name)
		case p.Mode == steps.PkgInstall && !repoPattern.MatchString(r.Repo):
			return bad("%q has an invalid repo %q", r.Name, sanitize.Field(r.Repo, 60))
		case p.Mode == steps.PkgInstall && r.OldVersion != "":
			return bad("%q is an update (%s → %s), not an install", r.Name, r.OldVersion, r.Version)
		case p.Mode == steps.PkgRemove && r.Repo != "":
			return bad("%q: a remove row has no repo", r.Name)
		}
		seen[r.Name] = true
	}
	for n := range isTarget {
		if !seen[n] {
			return bad("target %q is not in the rows", n)
		}
	}
	if PkgPlanHash(p) != p.Hash {
		return bad("the hash is not the hash of the listed packages")
	}
	return nil
}

// CheckPkgRequest reports whether req is one the helper takes: a known
// mode, 1–MaxPkgNames valid distinct names, only the options of its mode.
// Its error is a *steps.PkgRefusal with RefuseBadRequest.
func CheckPkgRequest(req steps.PkgRequest) error {
	bad := func(format string, a ...any) error {
		return &steps.PkgRefusal{Reason: steps.RefuseBadRequest, Detail: fmt.Sprintf(format, a...)}
	}
	switch {
	case req.Mode != steps.PkgInstall && req.Mode != steps.PkgRemove:
		return bad("unknown mode")
	case req.Mode == steps.PkgInstall && req.WithConfig, req.Mode == steps.PkgRemove && req.AsDeps:
		return bad("an option that does not belong to this mode")
	case len(req.Names) == 0 || len(req.Names) > MaxPkgNames:
		return bad("%d packages, want 1–%d", len(req.Names), MaxPkgNames)
	}
	seen := map[string]bool{}
	for _, n := range req.Names {
		switch {
		case !namePattern.MatchString(n):
			return bad("invalid package name %q", sanitize.Field(n, 60))
		case seen[n]:
			return bad("%q named twice", n)
		}
		seen[n] = true
	}
	return nil
}

// pkgArgs is pkg-install's / pkg-remove's arguments, from st.PkgPlan only:
// the plan must pass CheckPkgPlan, be of the step's verb's mode, and its
// Targets be st.Items.
func pkgArgs(st steps.Step) ([]string, error) {
	p := st.PkgPlan
	if p == nil {
		return nil, fmt.Errorf("%w: %s needs the plan from the plan verb (step %q has none)",
			ErrUnsupported, st.Verb, st.ID)
	}
	if err := CheckPkgPlan(*p); err != nil {
		return nil, err
	}
	want := steps.PkgInstall
	if st.Verb == steps.VerbPkgRemove {
		want = steps.PkgRemove
	}
	if p.Mode != want {
		return nil, fmt.Errorf("%w: a %s plan on %s", ErrBadPlan, modeName(p.Mode), st.Verb)
	}
	if !slices.Equal(st.Items, p.Targets) {
		return nil, fmt.Errorf("%w: the step's items are not the plan's targets", ErrBadPlan)
	}
	args := []string{p.Hash}
	if p.AsDeps {
		args = append(args, flagAsDeps)
	}
	if p.WithConfig {
		args = append(args, flagWithConfig)
	}
	return append(args, p.Targets...), nil
}

func modeName(m steps.PkgMode) string {
	if m == steps.PkgRemove {
		return "remove"
	}
	return "install"
}

// PlanPackages runs the helper's plan-install or plan-remove as the calling
// user — directly, never through pkexec — and returns its plan, checked
// (CheckPkgPlan). A refusal (updates pending, a conflict, a protected
// package, a name not found…) is a *steps.PkgRefusal; any other failure is
// an ordinary error. No install or remove may be offered on an error. It is
// what a front end hands the actions as Env.PlanPackages under polkit.
func PlanPackages(ctx context.Context, req steps.PkgRequest) (steps.PkgPlan, error) {
	return planPackages(ctx, HelperPath, req)
}

func planPackages(ctx context.Context, helper string, req steps.PkgRequest) (steps.PkgPlan, error) {
	if err := CheckPkgRequest(req); err != nil {
		return steps.PkgPlan{}, err
	}
	verb, flag := steps.VerbPlanInstall, flagAsDeps
	if req.Mode == steps.PkgRemove {
		verb, flag = steps.VerbPlanRemove, flagWithConfig
	}
	args := []string{verb}
	if req.AsDeps || req.WithConfig {
		args = append(args, flag)
	}
	args = append(args, req.Names...)
	cmd := exec.CommandContext(ctx, helper, args...)
	cmd.WaitDelay = 5 * time.Second
	stderr := &tail{max: stderrMax}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return steps.PkgPlan{}, fmt.Errorf("%s: %w", verb, err)
	}
	if err := cmd.Start(); err != nil {
		return steps.PkgPlan{}, fmt.Errorf("can't start the root helper's %s: %w", verb, err)
	}
	d := newDecoder(steps.Step{Verb: verb})
	readLines(stdout, d, func([]string, bool) {}, func(bool) {})
	waitErr := cmd.Wait()
	return d.pkgPlanResult(req, exitCode(waitErr), waitErr, stderr.String())
}

// ParsePkgPlan reads a whole plan-install / plan-remove output stream (as
// from a run that exited 0) with the parser PlanPackages uses, for tests
// that tie the helper's output to this parser.
func ParsePkgPlan(verb string, req steps.PkgRequest, r io.Reader) (steps.PkgPlan, error) {
	d := newDecoder(steps.Step{Verb: verb})
	readLines(r, d, func([]string, bool) {}, func(bool) {})
	return d.pkgPlanResult(req, 0, nil, "")
}

// pkgPlanResult is a plan verb's verdict: a refusal as *steps.PkgRefusal,
// else exactly one valid plan for req.
func (d *decoder) pkgPlanResult(req steps.PkgRequest, exit int, waitErr error, stderr string) (steps.PkgPlan, error) {
	o := d.outcome(exit, waitErr, stderr)
	var pr *steps.PkgRefusal
	switch {
	case errors.As(o.Err, &pr):
		return steps.PkgPlan{}, pr
	case o.Err != nil:
		return steps.PkgPlan{}, fmt.Errorf("%s: %w", d.verb, o.Err)
	case d.pkg == nil:
		return steps.PkgPlan{}, fmt.Errorf("%s: %w: no plan event", d.verb, ErrProtocol)
	}
	p := *d.pkg
	if p.Mode != req.Mode || p.WithConfig != req.WithConfig || p.AsDeps != req.AsDeps ||
		!sameSet(p.Targets, req.Names) {
		return steps.PkgPlan{}, fmt.Errorf("%s: %w: the plan is not for the request", d.verb, ErrBadPlan)
	}
	return p, nil
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// pkgRow is a rows entry of the plan event.
type pkgRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Old     string `json:"old"`
	Repo    string `json:"repo"`
	Target  bool   `json:"target"`
	DL      int64  `json:"dl"`
	Inst    int64  `json:"inst"`
}

// readPkgPlan takes a Packages verb's plan event. A plan verb needs exactly
// one, valid; a root verb reports one only when it found the confirmed plan
// stale, and it is kept for the refusal (it must be valid too, or dropped
// with a protocol error).
func (d *decoder) readPkgPlan(line []byte) {
	var e struct {
		Mode       *string   `json:"mode"`
		WithConfig bool      `json:"with_config"`
		AsDeps     bool      `json:"asdeps"`
		Targets    *[]string `json:"targets"`
		Items      *[]string `json:"items"`
		Total      *int      `json:"total"`
		Hash       *string   `json:"hash"`
		Rows       *[]pkgRow `json:"rows"`
		Warnings   []string  `json:"warnings"`
	}
	if !d.decode(line, &e) {
		return
	}
	switch {
	case d.pkg != nil:
		d.fail("a second plan event")
		return
	case e.Mode == nil || e.Targets == nil || e.Items == nil || e.Total == nil || e.Hash == nil || e.Rows == nil:
		d.fail("a plan event without mode, targets, items, total, hash and rows")
		return
	case *e.Total != len(*e.Items) || len(*e.Rows) != len(*e.Items):
		d.fail("plan event: total, items and rows disagree")
		return
	}
	p := steps.PkgPlan{WithConfig: e.WithConfig, AsDeps: e.AsDeps, Targets: slices.Clone(*e.Targets), Hash: *e.Hash}
	switch *e.Mode {
	case "install":
		p.Mode = steps.PkgInstall
	case "remove":
		p.Mode = steps.PkgRemove
	default:
		d.fail("plan event: unknown mode %q", sanitize.Field(*e.Mode, 20))
		return
	}
	for i, r := range *e.Rows {
		if (*e.Items)[i] != r.Name+" "+r.Version {
			d.fail("plan event: item %d is not its row", i)
			return
		}
		p.Rows = append(p.Rows, steps.PkgRow{Name: r.Name, Version: r.Version, OldVersion: r.Old, Repo: r.Repo,
			Target: r.Target, Download: r.DL, Installed: r.Inst})
	}
	for _, w := range e.Warnings {
		p.Warnings = append(p.Warnings, sanitize.Clip(sanitize.OneLine(w), 200))
	}
	if err := CheckPkgPlan(p); err != nil {
		d.fail("%v", err)
		return
	}
	d.pkg = &p
}

// refusal is the *steps.PkgRefusal a pkg verb's failed result stands for,
// or nil when it is not one: a result with a reason, or the stale exit of
// a root verb that ran nothing (like remove-orphans).
func (d *decoder) refusal() *steps.PkgRefusal {
	res := d.res
	if res == nil || res.OK || !(isPkgPlanVerb(d.verb) || isPkgRunVerb(d.verb)) {
		return nil
	}
	reason := steps.RefuseReason(res.Reason)
	if reason == "" && res.Code == exitStale && res.ChildExit == nil && isPkgRunVerb(d.verb) {
		reason = steps.RefuseStale
	}
	if reason == "" || !reasonPattern.MatchString(string(reason)) {
		return nil
	}
	r := &steps.PkgRefusal{Reason: reason, Names: res.Names, Count: res.Count, Detail: res.Error}
	if r.Detail == "" {
		r.Detail = string(reason)
	}
	if reason == steps.RefuseStale {
		r.Plan = d.pkg
	}
	return r
}
