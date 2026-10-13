package helper

import (
	"fmt"
	"slices"
	"strings"
)

// cleanupArgs are full-cleanup's explicit flags:
//
//	full-cleanup --keep N [--wipe] [--uninstalled] [--orphans sha256:…] --journal D
type cleanupArgs struct {
	keep, orphans, journal string
	wipe, uninstalled      bool
}

const fullCleanupUsage = "full-cleanup --keep N [--wipe] [--uninstalled] [--orphans sha256:<hash>] --journal 1week|2weeks|1month"

// parseCleanupArgs is deliberately strict: every flag at most once, values
// as the next argument (no --flag=value), no positionals, no unknown flags.
func parseCleanupArgs(args []string) (cleanupArgs, error) {
	var c cleanupArgs
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		f := args[i]
		if seen[f] {
			return c, fmt.Errorf("%s given twice", f)
		}
		seen[f] = true
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", f)
			}
			i++
			return args[i], nil
		}
		var err error
		switch f {
		case "--keep":
			if c.keep, err = value(); err == nil && !keepPattern.MatchString(c.keep) {
				err = fmt.Errorf("--keep must be 1–9, got %q", c.keep)
			}
		case "--journal":
			if c.journal, err = value(); err == nil && !slices.Contains(journalSpans, c.journal) {
				err = fmt.Errorf("--journal must be one of %s, got %q", strings.Join(journalSpans, ", "), c.journal)
			}
		case "--orphans":
			if c.orphans, err = value(); err == nil && !hashPattern.MatchString(c.orphans) {
				err = fmt.Errorf("--orphans must be a plan hash (sha256:<64 hex>)")
			}
		case "--wipe":
			c.wipe = true
		case "--uninstalled":
			c.uninstalled = true
		default:
			err = fmt.Errorf("unexpected argument %q", f)
		}
		if err != nil {
			return c, err
		}
	}
	switch {
	case c.keep == "":
		return c, fmt.Errorf("--keep is required")
	case c.journal == "":
		return c, fmt.Errorf("--journal is required")
	}
	return c, nil
}

// The parts of full-cleanup, named by the verb each stands for (the "part"
// of its progress and done events). They run in this order, which is part
// of the protocol (README.md): a client maps them back to its own steps in
// order (internal/helperclient).
const (
	PartRemoveOrphans = "remove-orphans"
	PartPaccache      = "paccache"
	PartPaccacheWipe  = "paccache-wipe"
	// PartCleanUninstalled is the one part that is no verb of its own: it
	// exists only inside full-cleanup (no polkit action of its own).
	PartCleanUninstalled = "clean-uninstalled"
	PartCleanTmp         = "clean-tmp"
	PartVacuumJournal    = "vacuum-journal"
	PartCheckFiles       = "check-files"
)

// runFullCleanup runs the fixed cleanup sequence under one polkit dialog:
// [remove-orphans H], paccache -k N, [paccache -k 0], [pacman -Sc],
// tmpfiles clean, journal vacuum, pacman -Qk. pacman -Sc (--uninstalled)
// drops the cached packages of what is no longer installed — what yay -Sc
// ran through sudo — after the orphan removal, so the orphans' packages go
// too; its part first removes the empty download folders interrupted
// pacman downloads left in the cache (leftovers.go). The orphan removal goes first, right after
// the lock check, while the plan the user confirmed is freshest. Each part
// starts with a progress event and ends with a done event, both naming its
// part. Parts are independent, so a failing one does not stop the rest.
// Result: 1 if any part failed, else 3 if the orphan plan had changed
// (orphans left alone), else 0.
func runFullCleanup(s *session, args []string) int {
	c, err := parseCleanupArgs(args)
	if err != nil {
		return s.usage("%v; usage: %s", err, fullCleanupUsage)
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	type cstep struct {
		part    string
		label   string
		argv    []string
		okExits []int
	}
	var steps []cstep
	if c.orphans != "" {
		steps = append(steps, cstep{part: PartRemoveOrphans})
	}
	steps = append(steps, cstep{part: PartPaccache, label: "Cleaning package cache (keep " + c.keep + ")", argv: paccacheArgv(c.keep)})
	if c.wipe {
		steps = append(steps, cstep{part: PartPaccacheWipe, label: "Removing every cached package", argv: paccacheArgv("0")})
	}
	if c.uninstalled {
		steps = append(steps, cstep{part: PartCleanUninstalled, label: "Removing uninstalled packages from the cache", argv: cleanUninstalledArgv()})
	}
	steps = append(steps,
		cstep{part: PartCleanTmp, label: "Cleaning temporary files", argv: cleanTmpArgv()},
		cstep{part: PartVacuumJournal, label: "Vacuuming the journal (older than " + c.journal + ")", argv: journalArgv(c.journal)},
		cstep{part: PartCheckFiles, label: "Checking package files", argv: integrityArgv(), okExits: checkFilesOK},
	)

	var failures []string
	stale := false
	for i, st := range steps {
		n, of := i+1, len(steps)
		s.part = st.part
		code, msg := ExitOK, ""
		switch st.part {
		case PartRemoveOrphans:
			code, msg = s.removeOrphans(n, of, c.orphans)
		case PartCleanUninstalled:
			// First the empty folders interrupted downloads left, which
			// -Sc would report as errors (leftovers.go).
			s.progress(n, of, st.label)
			s.tidyDownloadLeftovers()
			if ok, m := s.exec(st.label, st.argv, st.okExits...); !ok {
				code, msg = ExitFailed, m
			}
		default:
			if ok, m := s.step(n, of, st.label, st.argv, st.okExits...); !ok {
				code, msg = ExitFailed, m
			}
		}
		s.em.Done(n, of, st.part, code, msg)
		s.part = ""
		switch code {
		case ExitOK:
		case ExitStale:
			stale = true
		default: // failed, or pacman is running
			failures = append(failures, msg)
		}
	}
	switch {
	case len(failures) > 0:
		return s.finish(ExitFailed, strings.Join(failures, "; "))
	case stale:
		return s.finish(ExitStale, "orphan plan changed since confirm; orphans were not removed")
	}
	return s.finish(ExitOK, "")
}
