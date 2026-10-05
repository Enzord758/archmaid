package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// internal/helperclient builds the pkexec command line the GUI's Driver
// runs; it can't see the helper's internals, so it is tied to them here.

// TestClientConstantsAreTheHelpers: the client's helper path and name cap
// are the helper's.
func TestClientConstantsAreTheHelpers(t *testing.T) {
	if helperclient.HelperPath != helper.InstallPath {
		t.Errorf("helperclient.HelperPath %q, helper.InstallPath %q", helperclient.HelperPath, helper.InstallPath)
	}
	if helperclient.MaxNames != helper.MaxNames {
		t.Errorf("helperclient.MaxNames %d, helper.MaxNames %d", helperclient.MaxNames, helper.MaxNames)
	}
	if helperclient.SchemaVersion != helper.SchemaVersion {
		t.Errorf("helperclient.SchemaVersion %d, helper.SchemaVersion %d", helperclient.SchemaVersion, helper.SchemaVersion)
	}
}

// TestClientNamePatternsAreTheHelpers: the client's name checks and the
// helper's accept and refuse the same names, run in-process as a dev build
// with a fake runner. Both patterns are unexported, so they are tied by
// behaviour, edge cases included: a leading '-' (a flag) or '.', the
// length caps, and characters neither allows.
func TestClientNamePatternsAreTheHelpers(t *testing.T) {
	cases := map[string][]string{
		steps.VerbDisableUnit: {
			"a.service", "x@y.service", "getty@tty1.service", ":x.service", "_x.service", "@x.service",
			"-x.service", "--now.service", ".x.service", "a.timer", "x", ".service", "a b.service",
			"a/b.service", "ä.service", strings.Repeat("a", 250) + ".service", strings.Repeat("a", 251) + ".service",
		},
		steps.VerbRepair: {
			"a", "A", "+x", "_x", "@x", "a.b-c+d", "-x", "--root=/", ".x", "", "a/b", "a b",
			strings.Repeat("a", 128), strings.Repeat("a", 129),
		},
	}
	for verb, names := range cases {
		for _, n := range names {
			_, err := helperclient.Argv(steps.Step{Verb: verb, Items: []string{n}})
			var out, errOut bytes.Buffer
			dir := t.TempDir()
			code := helper.Run([]string{verb, n}, helper.Options{
				Runner: anyRunner{installed: []string{n}}, Stdout: &out, Stderr: &errOut, Euid: 1000, Dev: true,
				LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
			})
			if client, helperOK := err == nil, code != helper.ExitUsage; client != helperOK {
				t.Errorf("%s %q: the client accepts it: %v, the helper: %v (exit %d)", verb, n, client, helperOK, code)
			}
		}
	}
}

// anyRunner answers every query with success: `pacman -Qq` lists the given
// names, everything else prints nothing. Only input validation can then
// make the helper exit 2.
type anyRunner struct{ installed []string }

func (r anyRunner) Query(argv []string) (string, string, int, error) {
	if slices.Equal(argv, []string{helper.Pacman, "-Qq"}) {
		return strings.Join(r.installed, "\n") + "\n", "", 0, nil
	}
	return "", "", 0, nil
}

func (anyRunner) Stream([]string, func(stream, text string)) (int, error) { return 0, nil }

// TestClientArgvsPassTheHelpersValidation: for every step any action can
// build that the client can run, the arguments it builds are ones the
// helper's own validation accepts (anything but exit 2), run in-process
// as a dev build with a fake runner (no root, nothing executed).
func TestClientArgvsPassTheHelpersValidation(t *testing.T) {
	ran := map[string]bool{}
	for _, s := range buildAllSteps(t) {
		if s.Verb == "" || s.Run != nil {
			continue
		}
		if s.Verb == steps.VerbFullCleanup {
			// A part that is no verb of its own (pacman -Sc): it runs only
			// inside the full-cleanup call, which TestFullCleanupTiesToClient
			// runs through the helper.
			if _, err := helperclient.Argv(s); err == nil {
				t.Errorf("%s: Argv accepted a lone full-cleanup part", s.ID)
			}
			continue
		}
		argv, err := helperclient.Argv(s)
		if s.Verb == steps.VerbRemoveOrphans {
			if err == nil {
				t.Errorf("%s: Argv accepted remove-orphans without a plan hash", s.ID)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Argv: %v", s.ID, err)
			continue
		}
		if argv[0] != helperclient.Pkexec || argv[1] != helper.InstallPath {
			t.Fatalf("%s: argv %q", s.ID, argv)
		}
		var out, errOut bytes.Buffer
		dir := t.TempDir()
		code := helper.Run(argv[2:], helper.Options{
			Runner: anyRunner{installed: s.Items}, Stdout: &out, Stderr: &errOut, Euid: 1000, Dev: true,
			LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
		})
		if code == helper.ExitUsage {
			t.Errorf("%s: the helper refused %q as usage: %s", s.ID, argv[2:], errOut.String())
		}
		ran[s.Verb] = true
	}
	for _, v := range []string{steps.VerbCheckFiles, steps.VerbRepair, steps.VerbDisableUnit, steps.VerbPaccache,
		steps.VerbPaccacheWipe, steps.VerbCleanTmp, steps.VerbVacuumJournal} {
		if !ran[v] {
			t.Errorf("no built step ran %s through the helper: the walk missed it", v)
		}
	}
}
