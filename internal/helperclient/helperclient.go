// Package helperclient runs one root-helper step through pkexec and turns
// the helper's JSON lines into the engine's Inputs. It is what the polkit
// path of a Driver (internal/driver) calls for a step with a Verb:
//
//	in := helperclient.Faillock(user, time.Now()) // answers ReadFaillock first
//	out := helperclient.Run(ctx, seq, step, emit) // emits the Inputs, last StepExited
//
// Argv alone validates a step (and shows its command) without running it.
//
// The command is always exactly
//
//	pkexec /usr/lib/archmaid/archmaid-helper <verb> <args…>
//
// started directly (never through a shell), with the args built from the
// step's typed fields (Verb, Items, Plan) and fixed constants, never from
// Argv or any free text. The one helper call made without pkexec is the
// read-only plan-orphans (PlanOrphans), which the orphan removal is built
// from (plan.go). pkexec is found on PATH like su and sudo (no production
// code runs a privileged binary by absolute path; see internal/testenv), the
// helper by its fixed install path, which is the polkit policy's exec.path.
//
// The package imports internal/engine only for its Input types. engine does
// no IO by contract, so the Driver that calls this lives outside it and
// there is no cycle; producing the Inputs here keeps the line → Input
// mapping in one tested place, and the Driver only forwards them.
package helperclient

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/steps"
)

const (
	// Pkexec is the program Run starts, looked up on PATH.
	Pkexec = "pkexec"
	// HelperPath is the root helper's install path: polkit's exec.path for
	// every action, and the only path the helper runs as root from.
	HelperPath = "/usr/lib/archmaid/archmaid-helper"
	// SchemaVersion is the helper protocol version Run accepts ("v").
	SchemaVersion = 1
	// MaxLineBytes caps one JSON line from the helper. A longer line is
	// refused (not buffered past the cap) and fails the step. The largest
	// real line is a check event over every installed package, well below.
	MaxLineBytes = 512 << 10
	// MaxNames is the most names the helper takes in one call (its
	// maxNames; cmd/archmaid-helper ties the two in a test).
	MaxNames = 256
	// PaccacheKeep is paccache's N: what `paccache -r` (the sudo path's
	// step) keeps by default.
	PaccacheKeep = "3"
	// JournalSpan is vacuum-journal's D: the sudo path's --vacuum-time.
	JournalSpan = "2weeks"
)

// pkexec's exits when it never ran the helper. polkit 127 exits 127 for
// every refusal, cancel and lockout (docs/polkit-live-test.md); its man
// page names 126 for a dismissed dialog, so that counts too.
const (
	exitNotAuthorized = 127
	exitDismissed     = 126
)

// Argv's refusals. Each is returned wrapped with the step's details.
var (
	// ErrNoVerb: the step has no Verb, so the helper can't run it.
	ErrNoVerb = errors.New("helperclient: step has no helper verb")
	// ErrNoAction: the verb has no polkit action (plan-orphans needs no
	// root and is run directly, not through pkexec).
	ErrNoAction = errors.New("helperclient: verb has no polkit action")
	// ErrUnsupported: the step carries no typed field for an argument the
	// verb needs (remove-orphans without a Plan), or the verb is unknown.
	ErrUnsupported = errors.New("helperclient: verb not supported")
	// ErrBadItems: the step's Items are not what the verb takes.
	ErrBadItems = errors.New("helperclient: bad items for verb")
)

// The helper's own name patterns (helper.go), checked here too so a bad
// list is refused before a password prompt, not after. Neither lets a name
// start with '-'. cmd/archmaid-helper's TestClientNamePatternsAreTheHelpers
// ties them to the helper's by behaviour.
var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}$`)
	unitPattern = regexp.MustCompile(`^[A-Za-z0-9:_@][A-Za-z0-9:_.@-]{0,249}\.service$`)
)

// Argv is the command line Run starts for st: Pkexec, HelperPath, st.Verb,
// then the verb's arguments:
//
//	check-files     Items (package names, 0–256; none = every package)
//	repair          Items (package names, 1–256)
//	disable-unit    Items (unit names, 1–256)
//	paccache        PaccacheKeep
//	vacuum-journal  JournalSpan
//	paccache-wipe, clean-tmp: none (Items must be empty)
//	remove-orphans  Plan.Hash (see orphanArgs: the Plan must pass CheckPlan
//	                and be exactly the step's Items and Cascade)
//	full-cleanup    from Parts, the steps one call covers (fullCleanupArgs:
//	                the helper's parts in its order, each valid as its verb)
//
// plan-orphans (ErrNoAction), remove-orphans without a Plan and unknown
// verbs (ErrUnsupported) are refused, as is a step without a Verb
// (ErrNoVerb), with Items its verb doesn't take (ErrBadItems), or with a
// Plan that is bad or on any other verb (ErrBadPlan).
func Argv(st steps.Step) ([]string, error) {
	args, err := verbArgs(st)
	if err != nil {
		return nil, err
	}
	return append([]string{Pkexec, HelperPath, st.Verb}, args...), nil
}

func verbArgs(st steps.Step) ([]string, error) {
	if st.Plan != nil && st.Verb != steps.VerbRemoveOrphans {
		return nil, fmt.Errorf("%w: a plan on %q", ErrBadPlan, st.Verb)
	}
	if len(st.Parts) > 0 && st.Verb != steps.VerbFullCleanup {
		return nil, fmt.Errorf("%w: parts on %q", ErrBadParts, st.Verb)
	}
	switch st.Verb {
	case "":
		return nil, fmt.Errorf("%w (step %q)", ErrNoVerb, st.ID)
	case steps.VerbRemoveOrphans:
		return orphanArgs(st)
	case steps.VerbCheckFiles:
		if len(st.Items) == 0 {
			return nil, nil
		}
		return names(st, namePattern)
	case steps.VerbRepair:
		return names(st, namePattern)
	case steps.VerbDisableUnit:
		return names(st, unitPattern)
	case steps.VerbPaccache:
		return fixed(st, PaccacheKeep)
	case steps.VerbVacuumJournal:
		return fixed(st, JournalSpan)
	case steps.VerbPaccacheWipe, steps.VerbCleanTmp:
		return fixed(st)
	case steps.VerbFullCleanup:
		return fullCleanupArgs(st)
	case steps.VerbPlanOrphans:
		return nil, fmt.Errorf("%w: %s", ErrNoAction, st.Verb)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, st.Verb)
	}
}

// names is st.Items checked as the helper will: 1–MaxNames of them, each
// matching pat (so none starts with '-'), none twice.
func names(st steps.Step, pat *regexp.Regexp) ([]string, error) {
	if n := len(st.Items); n == 0 || n > MaxNames {
		return nil, fmt.Errorf("%w: %s takes 1–%d names, got %d", ErrBadItems, st.Verb, MaxNames, n)
	}
	seen := make(map[string]bool, len(st.Items))
	for _, it := range st.Items {
		if !pat.MatchString(it) {
			return nil, fmt.Errorf("%w: %s: invalid name %q", ErrBadItems, st.Verb, it)
		}
		if seen[it] {
			return nil, fmt.Errorf("%w: %s: %q named twice", ErrBadItems, st.Verb, it)
		}
		seen[it] = true
	}
	return slices.Clone(st.Items), nil
}

// fixed is args for a verb that takes no Items.
func fixed(st steps.Step, args ...string) ([]string, error) {
	if len(st.Items) != 0 {
		return nil, fmt.Errorf("%w: %s takes no items, got %d", ErrBadItems, st.Verb, len(st.Items))
	}
	return args, nil
}

// Faillock answers the engine's ReadFaillock for user at now: a thin
// wrapper over privilege.FaillockStatus. A Driver calls it before each
// helper step under polkit, because a cancelled polkit dialog costs a
// faillock failure like a wrong password.
func Faillock(user string, now time.Time) engine.FaillockRead {
	st, err := privilege.FaillockStatus(user, now)
	return engine.FaillockRead{Failures: st.Failures, Deny: st.Deny, Unlock: st.Unlock, Err: err}
}
