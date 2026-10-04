package helper

import (
	"fmt"
	"slices"
	"strings"
)

// cleanupArgs are full-cleanup's explicit flags:
//
//	full-cleanup --keep N [--wipe] [--orphans sha256:…] --journal D
type cleanupArgs struct {
	keep, orphans, journal string
	wipe                   bool
}

const fullCleanupUsage = "full-cleanup --keep N [--wipe] [--orphans sha256:<hash>] --journal 1week|2weeks|1month"

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

// runFullCleanup runs the fixed cleanup sequence under one polkit dialog:
// paccache -k N, [paccache -k 0], [remove-orphans H], journal vacuum,
// tmpfiles clean, pacman -Qk. Steps are independent, so a failing one does
// not stop the rest. Result: 1 if any step failed, else 3 if the orphan plan
// had changed (orphans left alone), else 0.
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
		label   string
		argv    []string
		okExits []int
		orphans bool
	}
	steps := []cstep{{label: "Cleaning package cache (keep " + c.keep + ")", argv: paccacheArgv(c.keep)}}
	if c.wipe {
		steps = append(steps, cstep{label: "Removing every cached package", argv: paccacheArgv("0")})
	}
	if c.orphans != "" {
		steps = append(steps, cstep{orphans: true})
	}
	steps = append(steps,
		cstep{label: "Vacuuming the journal (older than " + c.journal + ")", argv: journalArgv(c.journal)},
		cstep{label: "Cleaning temporary files", argv: cleanTmpArgv()},
		cstep{label: "Checking package files", argv: checkFilesArgv(), okExits: checkFilesOK},
	)

	var failures []string
	stale := false
	for i, st := range steps {
		n, of := i+1, len(steps)
		if st.orphans {
			ok, changed, msg := s.removeOrphans(n, of, c.orphans)
			if !ok {
				failures = append(failures, msg)
			}
			stale = stale || changed
			continue
		}
		if ok, msg := s.step(n, of, st.label, st.argv, st.okExits...); !ok {
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
