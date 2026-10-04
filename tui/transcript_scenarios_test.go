package main

import (
	"testing"
	"time"
)

// Characterization scenarios for the transcript harness (transcript_test.go).
// Each one records today's behaviour — known bugs included — so a refactor
// that changes any of it shows up as a golden diff. The suffix says how the
// action ran: .dryrun under --dryrun, .shim for real against shimmed programs.

// transcriptScenarios run as subtests of TestTranscript; each sets up its own
// environment and returns the driver after the action finished.
var transcriptScenarios = []struct {
	name string
	run  func(t *testing.T) *Driver
}{
	{"fullcleanup_orphans_cascade_acceptall.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		shimLog := installShims(t, Shims{
			"pacman": pacmanRules("libfoo\nlibbar", "libfoo\nlibbar\nlibbaz", "yay-bin"),
			"hyprpm": nil,
		})
		aurClean(t)
		d := NewDriver(t, shimLog,
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "ENTIRE package cache", "yes"},
			Answer{"confirm", "orphaned package", "yes"},
		)
		d.Select("Full Cleanup")
		return d
	}},
}

func TestTranscript(t *testing.T) {
	for _, sc := range transcriptScenarios {
		t.Run(sc.name, func(t *testing.T) {
			d := sc.run(t)
			checkGolden(t, sc.name, d.Transcript())
		})
	}
}

// ── fixtures ─────────────────────────────────────────────────────────────────

// transcriptEnv gives a scenario a scratch HOME and log dir, the dry-run mode
// it asks for, and a short SIGINT→SIGKILL grace.
func transcriptEnv(t *testing.T, dry bool) {
	t.Helper()
	auditEnv(t)
	oldDry, oldGrace := dryRun, sigkillGrace
	dryRun, sigkillGrace = dry, 50*time.Millisecond
	t.Cleanup(func() { dryRun, sigkillGrace = oldDry, oldGrace })
}

// pacmanRules answers the orphan query (-Qdtq), the cascade preview
// (-Rs --print) and the foreign-package list (-Qqm); "" means none, which
// pacman reports by exiting 1.
func pacmanRules(orphans, cascade, foreign string) []Rule {
	exit := func(out string) int {
		if out == "" {
			return 1
		}
		return 0
	}
	return []Rule{
		{Prefix: "-Qdtq", Out: orphans, Exit: exit(orphans)},
		{Prefix: "-Rs --print", Out: cascade},
		{Prefix: "-Qqm", Out: foreign, Exit: exit(foreign)},
	}
}

// aurClean serves yay-bin from a fake AUR with the maintainer the saved
// snapshot already has, so the audit passes without a finding.
func aurClean(t *testing.T) {
	t.Helper()
	fakeAUR(t, map[string]aurInfo{
		"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer"), LastModified: aged(24 * 365), NumVotes: 900},
	})
	saveBaseline([]string{"yay-bin"}, map[string]aurInfo{"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer")}}, nil)
}
