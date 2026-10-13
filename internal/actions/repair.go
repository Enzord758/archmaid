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

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysbin"
)

// discoverRepair is Repair's first stage, run as the user with no sudo:
// every package with a file the user can't stat (brokenCandidates). Nothing
// flagged means nothing is broken, which ends Repair without a password
// prompt; otherwise the candidates need the root recheck (rescanRepair).
//
// Each outcome says what was checked: how many packages, what the scan
// flagged (pacman's "package /missing/file" lines), and the commands.
func discoverRepair(ctx context.Context, env Env) Discovery {
	scan, err := brokenCandidates(ctx, env.repairWorkers())
	tried := "Tried: pacman -Qq, then pacman -Qkq over the installed packages, as the user"
	if err != nil {
		return Notice{Text: "Couldn't scan for broken packages: " + clipErr(err), Err: sanitize.OneLine(err.Error()),
			Details: []string{tried}, Log: scan.log + "error: " + err.Error() + "\n"}
	}
	if len(scan.cands) == 0 {
		return Notice{
			Text: fmt.Sprintf("No broken packages: checked %d installed %s for missing files (pacman -Qk, as the user).",
				scan.installed, plural(scan.installed, "package", "packages")),
			Details: []string{fmt.Sprintf("%d installed (pacman -Qq), checked with pacman -Qkq in %d parallel %s: nothing flagged",
				scan.installed, scan.chunks, plural(scan.chunks, "chunk", "chunks"))},
			Log: scan.log,
		}
	}
	return NeedsRoot{
		Items:      scan.cands,
		Busy:       "Rechecking flagged packages as root…",
		AuthFailed: "Sudo authentication failed — can't scan for broken packages.",
		Rescan:     rescanRepair(scan.cands, scan.installed),
		Checked: Checked{
			Summary: fmt.Sprintf("Checked %d installed %s as the user: %d flagged (%s), to recheck as root.",
				scan.installed, plural(scan.installed, "package", "packages"), len(scan.cands), nameList(scan.cands)),
			Details: scan.flagged,
			Log:     scan.log,
		},
	}
}

// rescanRepair runs once the sudo ticket is primed: it rechecks the user
// scan's candidates as root, and only the packages it confirms are offered
// for reinstalling. installed is how many packages the user scan checked,
// for the summary.
//
// Under polkit there is no ticket for its `sudo -n`, so it runs nothing
// and returns the recheck as a helper step instead (RepairCheckStep); the
// front end reads the helper's broken list and goes on with RepairStep.
func rescanRepair(candidates []string, installed int) func(context.Context, Env) Discovery {
	return func(ctx context.Context, env Env) Discovery {
		if env.Polkit {
			if len(candidates) > HelperMaxNames {
				return Notice{Text: fmt.Sprintf("Too many flagged packages (%d) to recheck through the root helper "+
					"(at most %d); run Repair from the terminal.", len(candidates), HelperMaxNames),
					Err: "too many flagged packages for the root helper"}
			}
			return Pending{Steps: []steps.Step{RepairCheckStep(candidates)}}
		}
		cmd := "sudo -n pacman -Qkq -- " + strings.Join(candidates, " ")
		pkgs, out, err := brokenPackages(ctx, candidates)
		log := "$ " + cmd + "\n" + out
		if err != nil {
			return Notice{Text: "Couldn't scan for broken packages: " + clipErr(err), Err: sanitize.OneLine(err.Error()),
				Details: []string{"Tried: " + cmd}, Log: log + "error: " + err.Error() + "\n"}
		}
		if len(pkgs) == 0 {
			return Notice{
				Text: fmt.Sprintf("No broken packages: checked %d installed %s; the %d flagged as the user (%s) are complete as root.",
					installed, plural(installed, "package", "packages"), len(candidates), nameList(candidates)),
				Details: []string{"Rechecked as root (" + cmd + "): nothing missing"},
				Log:     log,
			}
		}
		return Pending{Steps: []steps.Step{RepairStep(pkgs)}, Checked: Checked{
			Summary: fmt.Sprintf("Checked %d installed %s: %d broken (%s), confirmed as root.",
				installed, plural(installed, "package", "packages"), len(pkgs), nameList(pkgs)),
			Details: nonEmptyLines(out),
			Log:     log,
		}}
	}
}

// nameList is names for a summary: the first three, then "…".
func nameList(names []string) string {
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + ", …"
	}
	return strings.Join(names, ", ")
}

// HelperMaxNames is the most package names the root helper takes in one
// call (its maxNames; cmd/archmaid-helper ties the two in a test).
const HelperMaxNames = 256

// RepairCheckStep is Repair's root recheck as a root-helper step (polkit):
// `check-files` over exactly the user scan's candidates, which are its
// Items and its Argv after "--". The helper runs `pacman -Qk -- …` and
// reports the broken ones in its check event. It changes nothing, so it has
// no confirm. Argv names what the helper runs, without sudo. It is still
// Repair's discovery, so it logs to the discovery's log (logstore.Repair):
// a recheck that finds nothing (or fails) leaves a discovery-only run,
// which never takes a real run's place in the history.
func RepairCheckStep(candidates []string) steps.Step {
	return steps.Step{
		ID: steps.IDRepairCheck, Verb: steps.VerbCheckFiles,
		Title: "Rechecking flagged packages as root…", LogLabel: "Flagged packages rechecked",
		Argv:      append([]string{"pacman", "-Qk", "--"}, candidates...),
		Items:     slices.Clone(candidates),
		NeedsPriv: true,
		Category:  logstore.Repair,
	}
}

// RepairStep reinstalls pkgs, the packages the root recheck confirmed
// broken: the step rescanRepair queues under sudo, and the one a polkit
// front end queues after RepairCheckStep reports a non-empty broken list.
// Its Items are pkgs: the root helper's repair verb takes them from there.
func RepairStep(pkgs []string) steps.Step {
	return steps.Step{
		ID: steps.IDRepair, Verb: steps.VerbRepair,
		Title: "Reinstalling packages…", LogLabel: "Package repair completed",
		Argv:      append([]string{"sudo", "pacman", "-S", "--noconfirm", "--"}, pkgs...),
		NeedsPriv: true, Optional: true, AlwaysLog: true,
		Confirm: fmt.Sprintf("Reinstall %d broken package(s)?", len(pkgs)),
		Items:   slices.Clone(pkgs),
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
func brokenCandidates(ctx context.Context, workers int) (repairScan, error) {
	scan := repairScan{log: "$ pacman -Qq\n"}
	installed, err := qkqOutput(ctx, "pacman", "-Qq", "--color", "never")
	if err != nil {
		return scan, err
	}
	names := nonEmptyLines(installed)
	scan.installed = len(names)
	scan.log += fmt.Sprintf("(%d packages)\n", len(names))
	chunks := chunkStrings(names, workers)
	scan.chunks = len(chunks)
	found := make([][]string, len(chunks))
	outs := make([]string, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Go(func() {
			out, err := qkqOutput(ctx, "pacman", append([]string{"-Qkq", "--color", "never", "--"}, chunk...)...)
			found[i], outs[i], errs[i] = parseBrokenPackages(out), out, err
		})
	}
	wg.Wait()
	for i, chunk := range chunks {
		scan.log += fmt.Sprintf("$ pacman -Qkq -- <%d packages, %s … %s>\n", len(chunk), chunk[0], chunk[len(chunk)-1]) + outs[i]
		scan.flagged = append(scan.flagged, nonEmptyLines(outs[i])...)
	}
	if err := errors.Join(errs...); err != nil {
		return scan, err
	}
	scan.cands = slices.Concat(found...)
	if err := checkPackageNames("pacman -Qkq", scan.cands); err != nil {
		return scan, err
	}
	return scan, nil
}

// repairScan is what brokenCandidates saw, for the record: how many
// packages are installed, in how many chunks they were checked, pacman's
// report lines, the candidates, and the commands with their output.
type repairScan struct {
	installed, chunks int
	flagged, cands    []string
	log               string
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
// nothing may prompt on the raw tty. sudo is sysbin.Sudo, never one on PATH.
func brokenPackages(ctx context.Context, candidates []string) (pkgs []string, out string, err error) {
	out, err = qkqOutput(ctx, sysbin.Sudo, append([]string{"-n", "pacman", "-Qkq", "--color", "never", "--"}, candidates...)...)
	if err != nil {
		return nil, out, err
	}
	pkgs = parseBrokenPackages(out)
	if err := checkPackageNames("sudo -n pacman -Qkq", pkgs); err != nil {
		return nil, out, err
	}
	return pkgs, out, nil
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
