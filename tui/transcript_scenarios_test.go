package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Characterization scenarios for the transcript harness (transcript_test.go).
// Each one records today's behaviour — known bugs included — so a refactor
// that changes any of it shows up as a golden diff. The suffix says how the
// action ran: .dryrun under --dryrun, .shim for real against shimmed programs.
//
// Known bugs these goldens deliberately pin (see the P0 plan):
//   - Full Cleanup's temp step is `find /tmp /var/tmp … -exec rm -rf {} +`,
//     which also deletes live system state (every Full Cleanup plan).
//   - Delete old downloads runs `find ~/Downloads -maxdepth 1 -mtime +30
//     -exec rm -rf {} +`, which matches Downloads itself once it's old: the
//     count includes the directory, and the fresh file inside would go too
//     (downloads_found).
//   - The orphan list is computed before topgrade runs and removed after it
//     with --noconfirm (every Full Cleanup .shim: pacman -Qdtq comes first).
//   - The AUR snapshot advances during the audit, before the user answers, so
//     "no" still uses the alarm up (fullcleanup_audit_takeover_decline).

// transcriptScenarios run as subtests of TestTranscript; each sets up its own
// environment and returns the driver after the action finished.
var transcriptScenarios = []struct {
	name string
	run  func(t *testing.T) *Driver
}{
	// ── Full Cleanup ─────────────────────────────────────────────────────────

	{"fullcleanup_clean.dryrun", func(t *testing.T) *Driver {
		// nothing to remove, nothing foreign, no hyprpm
		transcriptEnv(t, true)
		shimLog := installShims(t, Shims{"pacman": pacmanRules("", "", "")})
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "ENTIRE package cache", "no"},
		)
	}},
	{"fullcleanup_orphans_cascade_acceptall.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		shimLog := installShims(t, Shims{
			"pacman": pacmanRules("libfoo\nlibbar", "libfoo\nlibbar\nlibbaz", "yay-bin"),
			"hyprpm": nil,
		})
		aurClean(t)
		return run(t, shimLog, "Full Cleanup", acceptAll...)
	}},
	{"fullcleanup_orphans_cascade_acceptall.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "libfoo\nlibbar", "libfoo\nlibbar\nlibbaz"))
		aurClean(t)
		return run(t, shimLog, "Full Cleanup", acceptAll...)
	}},
	{"fullcleanup_decline_optionals.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "libfoo\nlibbar", "libfoo\nlibbar\nlibbaz"))
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "ENTIRE package cache", "no"},
			Answer{"confirm", "orphaned package", "no"},
		)
	}},
	{"fullcleanup_gate_declined.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "libfoo\nlibbar", "libfoo\nlibbar\nlibbaz"))
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "no"},
		)
	}},
	{"fullcleanup_yay_fails.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		sh := fullCleanupShims(true, "libfoo\nlibbar", "libfoo\nlibbar\nlibbaz")
		sh["yay"] = []Rule{{Prefix: "-Sc", Out: "error: could not access cache directory", Exit: 1}}
		shimLog := installShims(t, sh)
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "ENTIRE package cache", "no"},
			Answer{"confirm", "orphaned package", "yes"},
		)
	}},
	{"fullcleanup_audit_takeover_decline.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "", ""))
		aurTakeover(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "yay-bin changed maintainer", "no"},
			Answer{"confirm", "ENTIRE package cache", "no"},
		)
	}},
	{"fullcleanup_audit_takeover_accept.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "", ""))
		aurTakeover(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "yay-bin changed maintainer", "yes"},
			Answer{"confirm", "ENTIRE package cache", "no"},
		)
	}},
	{"fullcleanup_audit_unreachable.shim", func(t *testing.T) *Driver {
		// no fakeAUR: TestMain's closed port refuses the connection
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(true, "", ""))
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"confirm", "ENTIRE package cache", "no"},
		)
	}},
	{"fullcleanup_uncached_password_ok.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(false, "", ""))
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"password", "", "correct"},
			Answer{"confirm", "ENTIRE package cache", "no"},
		)
	}},
	{"fullcleanup_three_wrong_passwords.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, fullCleanupShims(false, "libfoo\nlibbar", "libfoo\nlibbar\nlibbaz"))
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"password", "", "wrong"},
			Answer{"password", "", "wrong"},
			Answer{"password", "", "wrong"},
		)
	}},
	{"fullcleanup_abort_topgrade.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		sh := fullCleanupShims(true, "", "")
		sh["topgrade"] = []Rule{{Prefix: "-y", Block: true}}
		shimLog := installShims(t, sh)
		aurClean(t)
		return run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"abort", "topgrade", ""},
		)
	}},
	{"fullcleanup_abort_sigkill.shim", func(t *testing.T) *Driver {
		// topgrade ignores SIGINT; only the SIGKILL after sigkillGrace ends
		// it, well before its sleep would
		transcriptEnv(t, false)
		sh := fullCleanupShims(true, "", "")
		sh["topgrade"] = []Rule{{Prefix: "-y", Block: true, TrapINT: true}}
		shimLog := installShims(t, sh)
		aurClean(t)
		start := time.Now()
		d := run(t, shimLog, "Full Cleanup",
			Answer{"confirm", "Proceed with full cleanup", "yes"},
			Answer{"abort", "topgrade", ""},
		)
		if el := time.Since(start); el > 10*time.Second {
			t.Fatalf("abort took %v: the SIGINT-ignoring child wasn't SIGKILLed", el)
		}
		return d
	}},

	// ── Clear user cache ─────────────────────────────────────────────────────

	{"clearcache_accept.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		shimLog := installShims(t, nil)
		return run(t, shimLog, "Clear user cache", Answer{"confirm", "Clear all user cache", "yes"})
	}},
	{"clearcache_decline.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		shimLog := installShims(t, nil)
		return run(t, shimLog, "Clear user cache", Answer{"confirm", "Clear all user cache", "no"})
	}},

	// ── Run topgrade ─────────────────────────────────────────────────────────

	{"topgrade_ok.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{
			"sudo":     sudoRules(true),
			"topgrade": {{Prefix: "-y", Out: topgradeOK}},
		})
		return run(t, shimLog, "Run topgrade")
	}},
	{"topgrade_fail.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{
			"sudo": sudoRules(true),
			"topgrade": {{Prefix: "-y", Exit: 1, Out: "error: failed to commit transaction (conflicting files)\n" +
				"── Summary ──\nSystem update: FAILED"}},
		})
		return run(t, shimLog, "Run topgrade")
	}},

	// ── Repair broken packages ───────────────────────────────────────────────

	{"repair_cached_found.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(true, Rule{Prefix: "-n pacman -Qk", Exit: 1, Out: "" +
			"foo: 12 total files, 1 missing file\n" +
			"bar: 30 total files, 0 missing files\n" +
			"baz: 80 total files, 10 missing files"})})
		return run(t, shimLog, "Repair broken packages", Answer{"confirm", "Reinstall 2 broken", "yes"})
	}},
	{"repair_none.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(true, Rule{Prefix: "-n pacman -Qk",
			Out: "foo: 12 total files, 0 missing files\nbar: 30 total files, 0 missing files"})})
		return run(t, shimLog, "Repair broken packages")
	}},
	{"repair_auth_giveup.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(false), "su": suRules()})
		return run(t, shimLog, "Repair broken packages",
			Answer{"password", "", "wrong"},
			Answer{"password", "", "wrong"},
			Answer{"password", "", "wrong"},
		)
	}},

	// ── Delete old downloads (dry run only: the step is a real rm -rf) ───────

	{"downloads_found.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		dl := downloadsDir(t)
		for _, n := range []string{"old.iso", "old.zip"} {
			touch(t, filepath.Join(dl, n), 40)
		}
		touch(t, filepath.Join(dl, "fresh.pdf"), 0)
		touch(t, dl, 40) // a Downloads untouched for a month matches itself
		shimLog := installShims(t, nil, "bash", "find", "wc")
		return run(t, shimLog, "Delete old downloads", Answer{"confirm", "older than 30 days", "yes"})
	}},
	{"downloads_none.dryrun", func(t *testing.T) *Driver {
		transcriptEnv(t, true)
		touch(t, filepath.Join(downloadsDir(t), "fresh.pdf"), 0)
		shimLog := installShims(t, nil, "bash", "find", "wc")
		return run(t, shimLog, "Delete old downloads")
	}},

	// ── Disable unused services ──────────────────────────────────────────────

	{"services_select2.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(true), "systemctl": systemctlRules()}, servicesTools...)
		return run(t, shimLog, "Disable unused services",
			Answer{"select", "", "bluetooth.service docker.service"})
	}},
	{"services_none_selected.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(true), "systemctl": systemctlRules()}, servicesTools...)
		return run(t, shimLog, "Disable unused services", Answer{"select", "", ""})
	}},
	{"services_none_found.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"sudo": sudoRules(true), "systemctl": nil}, servicesTools...)
		return run(t, shimLog, "Disable unused services")
	}},

	// ── Audit AUR packages ───────────────────────────────────────────────────

	{"auraudit_first_baseline.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"pacman": pacmanRules("", "", "yay-bin\nfoo-git")})
		fakeAUR(t, map[string]aurInfo{
			"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer"), LastModified: aged(24 * 365), NumVotes: 900},
			"foo-git": {Name: "foo-git", LastModified: aged(24 * 365)},
		})
		return run(t, shimLog, "Audit AUR packages")
	}},
	{"auraudit_takeover.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"pacman": pacmanRules("", "", "yay-bin\nfoo-git")})
		fakeAUR(t, map[string]aurInfo{
			"yay-bin": {Name: "yay-bin", Maintainer: strp("mallory"), LastModified: aged(24 * 365), NumVotes: 900},
			"foo-git": {Name: "foo-git", Maintainer: strp("eve"), LastModified: aged(24 * 365), NumVotes: 3},
		})
		saveBaseline([]string{"yay-bin", "foo-git"}, map[string]aurInfo{
			"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer")},
			"foo-git": {Name: "foo-git"},
		}, nil)
		return run(t, shimLog, "Audit AUR packages")
	}},
	{"auraudit_no_foreign.shim", func(t *testing.T) *Driver {
		transcriptEnv(t, false)
		shimLog := installShims(t, Shims{"pacman": pacmanRules("", "", "")})
		return run(t, shimLog, "Audit AUR packages")
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

// run starts a driver with script and runs the menu item matching item.
func run(t *testing.T, shimLog, item string, script ...Answer) *Driver {
	t.Helper()
	d := NewDriver(t, shimLog, script...)
	d.Select(item)
	return d
}

// acceptAll says yes to Full Cleanup's gate and both optional removals.
var acceptAll = []Answer{
	{"confirm", "Proceed with full cleanup", "yes"},
	{"confirm", "ENTIRE package cache", "yes"},
	{"confirm", "orphaned package", "yes"},
}

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

// sudoRules: the cache probe (-n -v) succeeds when cached; anything else —
// `-S -v`, every `-A <step>` — exits 0 unless extra says otherwise.
func sudoRules(cached bool, extra ...Rule) []Rule {
	probe := Rule{Prefix: "-n -v"}
	if !cached {
		probe.Exit = 1
	}
	return append([]Rule{probe}, extra...)
}

// suRules accepts shimPassword on stdin, the way authSudo's check feeds it.
func suRules() []Rule {
	return []Rule{{Script: `read -r pw
if [ "$pw" != '` + shimPassword + `' ]; then echo 'su: Authentication failure' >&2; exit 1; fi`}}
}

const topgradeOK = ":: Synchronizing package databases...\n── Summary ──\nSystem update: OK"

// fullCleanupShims is a healthy machine for a real Full Cleanup: sudo cached
// or not, topgrade succeeds, hyprpm installed, pacman answering the orphan
// queries as given and listing yay-bin as foreign.
func fullCleanupShims(cached bool, orphans, cascade string) Shims {
	return Shims{
		"pacman":   pacmanRules(orphans, cascade, "yay-bin"),
		"sudo":     sudoRules(cached, Rule{Prefix: "-A pacman -Qk", Out: "yay-bin: 12 total files, 0 missing files"}),
		"su":       suRules(),
		"topgrade": {{Prefix: "-y", Out: topgradeOK}},
		"yay":      nil,
		"hyprpm":   nil,
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

// aurTakeover: yay-bin's maintainer changed from the snapshot's jguer to mallory.
func aurTakeover(t *testing.T) {
	t.Helper()
	fakeAUR(t, map[string]aurInfo{
		"yay-bin": {Name: "yay-bin", Maintainer: strp("mallory"), LastModified: aged(24 * 365), NumVotes: 900},
	})
	saveBaseline([]string{"yay-bin"}, map[string]aurInfo{"yay-bin": {Name: "yay-bin", Maintainer: strp("jguer")}}, nil)
}

// servicesTools are what servicesPipeline needs besides systemctl.
var servicesTools = []string{"bash", "comm", "awk", "sort", "grep"}

// systemctlRules: four enabled services, three of them inactive (sshd is
// running), plus a systemd- one the pipeline filters out.
func systemctlRules() []Rule {
	return []Rule{
		{Prefix: "list-unit-files", Out: "" +
			"bluetooth.service enabled disabled\n" +
			"cups.service enabled enabled\n" +
			"docker.service enabled disabled\n" +
			"sshd.service enabled disabled\n" +
			"systemd-timesyncd.service enabled enabled"},
		{Prefix: "list-units", Out: "" +
			"bluetooth.service loaded inactive dead Bluetooth service\n" +
			"cups.service loaded inactive dead CUPS Scheduler\n" +
			"docker.service loaded inactive dead Docker Application Container Engine\n" +
			"systemd-timesyncd.service loaded inactive dead Network Time Synchronization"},
	}
}

// downloadsDir creates $HOME/Downloads under the scratch HOME.
func downloadsDir(t *testing.T) string {
	t.Helper()
	dl := filepath.Join(os.Getenv("HOME"), "Downloads")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	return dl
}

// touch creates path if missing and sets its mtime daysAgo days back.
func touch(t *testing.T, path string, daysAgo int) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
