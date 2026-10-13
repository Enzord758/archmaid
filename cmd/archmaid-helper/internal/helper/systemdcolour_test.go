package helper

import (
	"slices"
	"strings"
	"testing"
)

// TestSystemdColoursOnlyForRelayedCalls (task #133): the environment the
// runner gives a call (command, which both Query and Stream use) holds
// SYSTEMD_COLORS=1 for the three systemd calls whose output is only
// relayed (tmpfiles --clean, journalctl --vacuum-time, systemctl disable
// --now) and for no other: not systemctl show or cat (disable-unit's
// denylist vetting parses them; coloured, `systemctl cat` hides [Install]
// in SGR and the check fails open), not a pacman, paccache or pacman-conf
// call. TERM stays dumb for all. It fails on a helper that sets the
// variable in ChildEnv (the parsed calls get it) or in no call (the
// relayed ones don't).
func TestSystemdColoursOnlyForRelayedCalls(t *testing.T) {
	names := []string{"a.service", "b.service"}
	relayed := [][]string{cleanTmpArgv(), journalArgv("2weeks"), disableArgv(names)}
	other := [][]string{
		unitShowArgv("a.service"), unitCatArgv("a.service"), cacheDirArgv(),
		installedArgv(), checkFilesArgv(), integrityArgv(), checkPackagesArgv(names), repairPlanArgv(names),
		orphansArgv(), cascadeArgv(names), queryArgv("-Q", "a"), queryArgv("-Si", "a"), queryArgv("-Qk", "a"),
		removeArgv(names), repairArgv(names), cleanUninstalledArgv(), paccacheArgv("3"), paccacheArgv("0"),
		// the same tools in a shape that isn't one of the relayed three
		{Systemctl, "cat", "--", "a.service"}, {Systemctl, "disable", "--", "a.service"},
		{Systemctl, "list-unit-files"}, {Systemctl, "show", "--", "a.service"},
		{Journalctl, "--disk-usage"}, {SystemdTmpfile, "--create"}, {SystemdTmpfile, "--clean", "--prefix=/tmp"},
	}
	has := func(env []string) bool {
		return slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "SYSTEMD_COLORS") })
	}
	for _, argv := range relayed {
		cmd, err := command(argv)
		if err != nil {
			t.Fatal(err)
		}
		want := append(ChildEnv(), "SYSTEMD_COLORS=1")
		if !slices.Equal(cmd.Env, want) {
			t.Errorf("%q: env %q, want %q", argv, cmd.Env, want)
		}
	}
	for _, argv := range other {
		cmd, err := command(argv)
		if err != nil {
			t.Fatal(err)
		}
		if has(cmd.Env) || !slices.Equal(cmd.Env, ChildEnv()) {
			t.Errorf("%q: env %q, want exactly ChildEnv (the output is parsed or the tool has no systemd colour)", argv, cmd.Env)
		}
	}
	if has(ChildEnv()) {
		t.Errorf("ChildEnv() = %q holds SYSTEMD_COLORS", ChildEnv())
	}
}

// TestSystemdColoursReachTheRunner: through a whole verb, not just the
// argv: of every call Full Cleanup makes, the ones CallEnv gives
// SYSTEMD_COLORS are exactly the relayed systemd calls, none of them a
// Query (everything the helper parses is read with Query).
func TestSystemdColoursReachTheRunner(t *testing.T) {
	h := PlanHash([]string{"a"}, []string{"a 1-1", "dep 1-1"})
	f := orphanFake("a\n", "a 1-1\ndep 1-1\n")
	o, out := rootOpts(t, f)
	if code := Run([]string{"full-cleanup", "--keep", "2", "--wipe", "--uninstalled", "--orphans", h, "--journal", "1month"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, q := range f.queries {
		if slices.Contains(CallEnv(q), SystemdColours) {
			t.Errorf("Query %q gets SYSTEMD_COLORS: its output is parsed", q)
		}
	}
	var got []string
	for _, s := range f.streams {
		if slices.Contains(CallEnv(s), SystemdColours) {
			got = append(got, strings.Join(s, " "))
		}
	}
	want := []string{"/usr/bin/systemd-tmpfiles --clean", "/usr/bin/journalctl --vacuum-time=1month"}
	if !slices.Equal(got, want) {
		t.Errorf("coloured systemd calls %q, want %q", got, want)
	}
}
