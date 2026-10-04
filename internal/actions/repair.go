package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// discoverRepair is Repair's first stage, run as the user with no sudo:
// every package with a file the user can't stat (brokenCandidates). Nothing
// flagged means nothing is broken, which ends Repair without a password
// prompt; otherwise the candidates need the root recheck (rescanRepair).
func discoverRepair(ctx context.Context, env Env) Discovery {
	cands, err := brokenCandidates(ctx, env.repairWorkers())
	if err != nil {
		return Notice{"Couldn't scan for broken packages: " + clipErr(err)}
	}
	if len(cands) == 0 {
		return Notice{"No broken packages found."}
	}
	return NeedsRoot{
		Items:      cands,
		Busy:       "Rechecking flagged packages as root…",
		AuthFailed: "Sudo authentication failed — can't scan for broken packages.",
		Rescan:     rescanRepair(cands),
	}
}

// rescanRepair runs once the sudo ticket is primed: it rechecks the user
// scan's candidates as root, and only the packages it confirms are offered
// for reinstalling.
//
// Under polkit there is no ticket for its `sudo -n`, so it runs nothing
// and returns the recheck as a helper step instead (RepairCheckStep); the
// front end reads the helper's broken list and goes on with RepairStep.
func rescanRepair(candidates []string) func(context.Context, Env) Discovery {
	return func(ctx context.Context, env Env) Discovery {
		if env.Polkit {
			if len(candidates) > HelperMaxNames {
				return Notice{fmt.Sprintf("Too many flagged packages (%d) to recheck through the root helper "+
					"(at most %d); run Repair from the terminal.", len(candidates), HelperMaxNames)}
			}
			return Pending{[]steps.Step{RepairCheckStep(candidates)}}
		}
		pkgs, err := brokenPackages(ctx, candidates)
		if err != nil {
			return Notice{"Couldn't scan for broken packages: " + clipErr(err)}
		}
		if len(pkgs) == 0 {
			return Notice{"No broken packages found."}
		}
		return Pending{[]steps.Step{RepairStep(pkgs)}}
	}
}

// HelperMaxNames is the most package names the root helper takes in one
// call (its maxNames; cmd/archmaid-helper ties the two in a test).
const HelperMaxNames = 256

// RepairCheckStep is Repair's root recheck as a root-helper step (polkit):
// `check-files` over exactly the user scan's candidates, which are its
// Items and its Argv after "--". The helper runs `pacman -Qk -- …` and
// reports the broken ones in its check event. It changes nothing, so it has
// no confirm. Argv names what the helper runs, without sudo.
func RepairCheckStep(candidates []string) steps.Step {
	return steps.Step{
		ID: steps.IDRepairCheck, Verb: steps.VerbCheckFiles,
		Title: "Rechecking flagged packages as root…", LogLabel: "Flagged packages rechecked",
		Argv:      append([]string{"pacman", "-Qk", "--"}, candidates...),
		Items:     slices.Clone(candidates),
		NeedsPriv: true,
	}
}

// RepairStep reinstalls pkgs, the packages the root recheck confirmed
// broken: the step rescanRepair queues under sudo, and the one a polkit
// front end queues after RepairCheckStep reports a non-empty broken list.
func RepairStep(pkgs []string) steps.Step {
	return steps.Step{
		ID: steps.IDRepair, Verb: steps.VerbRepair,
		Title: "Reinstalling packages…", LogLabel: "Package repair completed",
		Argv:      append([]string{"sudo", "pacman", "-S", "--noconfirm", "--"}, pkgs...),
		NeedsPriv: true, Optional: true, AlwaysLog: true,
		Confirm: fmt.Sprintf("Reinstall %d broken package(s)?", len(pkgs)),
	}
}

// clipErr is err's text cut to fit a notice; a timeout says only that.
func clipErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	s := err.Error()
	if len(s) > 70 {
		s = s[:67] + "…"
	}
	return s
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
func brokenCandidates(ctx context.Context, workers int) ([]string, error) {
	installed, err := qkqOutput(ctx, "pacman", "-Qq")
	if err != nil {
		return nil, err
	}
	chunks := chunkStrings(nonEmptyLines(installed), workers)
	found := make([][]string, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Go(func() {
			out, err := qkqOutput(ctx, "pacman", append([]string{"-Qkq", "--"}, chunk...)...)
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
func brokenPackages(ctx context.Context, candidates []string) ([]string, error) {
	out, err := qkqOutput(ctx, "sudo", append([]string{"-n", "pacman", "-Qkq", "--"}, candidates...)...)
	if err != nil {
		return nil, err
	}
	return parseBrokenPackages(out), nil
}

// qkqOutput runs a pacman query (proc.Query) and returns its stdout. pacman
// -Qkq exits 1 when it finds missing files, so an error with a report on
// stdout is a result, not a failure; an error with nothing on stdout is one,
// reported as the command's stderr when it has any.
func qkqOutput(ctx context.Context, name string, args ...string) (string, error) {
	out, err := proc.Query(ctx, name, args...)
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
