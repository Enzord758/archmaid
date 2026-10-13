package helper

import (
	"slices"
	"strings"
	"testing"
)

// Colour in the helper's output (task #111). The helper's children write
// into pipes, so pacman colours only when asked (`--color always`; pacman
// reads no TERM, and --color always overrides pacman.conf and the tty
// check). The rule per call:
//
//   - a command whose output the helper only relays (line events) gets
//     --color always: pacman -Rns, -S, -Sc and full-cleanup's -Qk part;
//   - a command whose output the helper parses gets --color never, so no
//     parsed line ever holds an escape: every Query (-Qq, -Qdtq, -Rs
//     --print, -Q, -Si, -Qk -- pkg, -Sp) and the check-files verb's -Qk,
//     whose summary lines become the check event;
//   - paccache (a tty check only), systemd-tmpfiles and journalctl (no
//     --color option: one would fail the part) and systemctl get no flag,
//     and the environment stays exactly ChildEnv, TERM=dumb included, so
//     systemctl show/cat, which disable-unit's denylist vetting parses,
//     stay plain.

// colourWhen is argv's `--color <when>`, or "" when it has none. A pair
// after `--` (a name) doesn't count; one before it must come only once.
func colourWhen(t *testing.T, argv []string) string {
	t.Helper()
	opts := argv
	if i := slices.Index(argv, "--"); i >= 0 {
		opts = argv[:i]
	}
	i := slices.Index(opts, "--color")
	if i < 0 {
		return ""
	}
	if i+1 >= len(opts) {
		t.Fatalf("%q: --color without a value before --", argv)
	}
	if slices.Contains(opts[i+1:], "--color") {
		t.Fatalf("%q: --color twice", argv)
	}
	return opts[i+1]
}

// TestColourPerCall runs every verb that runs pacman (and the full
// Full Cleanup) on a fake runner and checks each recorded call's colour:
// relayed-only calls --color always, parsed ones --color never, and the
// tools without a colour option none. It fails on a helper that leaves
// pacman's colour to the defaults (no flag at all).
func TestColourPerCall(t *testing.T) {
	type call struct {
		argv string // the argv without its --color pair
		when string
	}
	record := func(t *testing.T, f *fakeRunner) (queries, streams []call) {
		t.Helper()
		for _, q := range f.queries {
			queries = append(queries, call{strings.Join(withoutColour(q), " "), colourWhen(t, q)})
		}
		for _, s := range f.streams {
			streams = append(streams, call{strings.Join(withoutColour(s), " "), colourWhen(t, s)})
		}
		return queries, streams
	}
	run := func(t *testing.T, f *fakeRunner, args ...string) {
		t.Helper()
		o, out := rootOpts(t, f)
		if code := Run(args, o); code != ExitOK {
			t.Fatalf("%q: exit %d:\n%s", args, code, out)
		}
	}
	h := PlanHash([]string{"a"}, []string{"a 1-1", "dep 1-1"})
	cases := []struct {
		name             string
		fake             func() *fakeRunner
		args             []string
		queries, streams []call
	}{
		{
			name: "full-cleanup", fake: func() *fakeRunner { return orphanFake("a\n", "a 1-1\ndep 1-1\n") },
			args: []string{"full-cleanup", "--keep", "2", "--wipe", "--uninstalled", "--orphans", h, "--journal", "1month"},
			queries: []call{
				{"/usr/bin/pacman -Qdtq", "never"},
				{"/usr/bin/pacman -Rs --print --print-format %n %v -- a", "never"},
			},
			streams: []call{
				{"/usr/bin/pacman -Rns --noconfirm -- a", "always"},
				{"/usr/bin/paccache -r -k 2", ""},
				{"/usr/bin/paccache -r -k 0", ""},
				{"/usr/bin/pacman -Sc --noconfirm", "always"},
				{"/usr/bin/systemd-tmpfiles --clean", ""},
				{"/usr/bin/journalctl --vacuum-time=1month", ""},
				{"/usr/bin/pacman -Qk", "always"}, // relayed only: no check event here
			},
		},
		{
			name: "remove-orphans", fake: func() *fakeRunner { return orphanFake("a\n", "a 1-1\ndep 1-1\n") },
			args: []string{"remove-orphans", h},
			queries: []call{
				{"/usr/bin/pacman -Qdtq", "never"},
				{"/usr/bin/pacman -Rs --print --print-format %n %v -- a", "never"},
			},
			streams: []call{{"/usr/bin/pacman -Rns --noconfirm -- a", "always"}},
		},
		{
			name: "plan-orphans", fake: func() *fakeRunner { return orphanFake("a\n", "a 1-1\ndep 1-1\n") },
			args: []string{"plan-orphans"},
			queries: []call{
				{"/usr/bin/pacman -Qdtq", "never"},
				{"/usr/bin/pacman -Rs --print --print-format %n %v -- a", "never"},
			},
		},
		{
			name: "check-files", fake: func() *fakeRunner { return newFake() },
			args:    []string{"check-files"},
			streams: []call{{"/usr/bin/pacman -Qk", "never"}}, // parsed into the check event
		},
		{
			name: "check-files names", fake: func() *fakeRunner { return installedFake("a") },
			args:    []string{"check-files", "a"},
			queries: []call{{"/usr/bin/pacman -Qq", "never"}},
			streams: []call{{"/usr/bin/pacman -Qk -- a", "never"}},
		},
		{
			name: "repair", fake: func() *fakeRunner {
				return onPlan(onPkg(newFake(), "a", "1.0-1", "1.0-1", 2), []string{"a"}, "a 1.0-1")
			},
			args: []string{"repair", "a"},
			queries: []call{
				{"/usr/bin/pacman -Q -- a", "never"},
				{"/usr/bin/pacman -Si -- a", "never"},
				{"/usr/bin/pacman -Qk -- a", "never"},
				{"/usr/bin/pacman -Sp --noconfirm --print-format %n %v -- a", "never"},
			},
			streams: []call{{"/usr/bin/pacman -S --noconfirm -- a", "always"}},
		},
		{
			name: "paccache", fake: func() *fakeRunner { return newFake() },
			args: []string{"paccache", "3"}, streams: []call{{"/usr/bin/paccache -r -k 3", ""}},
		},
		{
			name: "vacuum-journal", fake: func() *fakeRunner { return newFake() },
			args: []string{"vacuum-journal", "2weeks"}, streams: []call{{"/usr/bin/journalctl --vacuum-time=2weeks", ""}},
		},
		{
			name: "clean-tmp", fake: func() *fakeRunner { return newFake() },
			args: []string{"clean-tmp"}, streams: []call{{"/usr/bin/systemd-tmpfiles --clean", ""}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.fake()
			run(t, f, c.args...)
			q, s := record(t, f)
			if !slices.Equal(q, c.queries) {
				t.Errorf("queries (argv, --color)\n got %q\nwant %q", q, c.queries)
			}
			if !slices.Equal(s, c.streams) {
				t.Errorf("streams (argv, --color)\n got %q\nwant %q", s, c.streams)
			}
		})
	}
}

// TestEveryQueryIsColourless: whatever the helper reads with Query it
// parses, so every pacman argv builder a Query or a parsed Stream uses
// carries --color never, the relayed-only ones --color always, and the
// tools without a colour option none (TestColourPerCall checks the same
// on recorded runs).
func TestEveryQueryIsColourless(t *testing.T) {
	for _, argv := range [][]string{
		installedArgv(), checkFilesArgv(), checkPackagesArgv([]string{"a"}), repairPlanArgv([]string{"a"}),
		queryArgv("-Q", "a"), queryArgv("-Si", "a"), queryArgv("-Qk", "a"),
		orphansArgv(), cascadeArgv([]string{"a"}),
	} {
		if w := colourWhen(t, argv); w != "never" {
			t.Errorf("%q: --color %q, want never (its output is parsed)", argv, w)
		}
	}
	for _, argv := range [][]string{removeArgv([]string{"a"}), repairArgv([]string{"a"}), cleanUninstalledArgv(), integrityArgv()} {
		if w := colourWhen(t, argv); w != "always" {
			t.Errorf("%q: --color %q, want always (relayed only)", argv, w)
		}
	}
	for _, argv := range [][]string{paccacheArgv("3"), journalArgv("2weeks"), cleanTmpArgv(), disableArgv([]string{"a.service"}),
		unitShowArgv("a.service"), unitCatArgv("a.service")} {
		if slices.Contains(argv, "--color") {
			t.Errorf("%q has --color: %s has no such option", argv, argv[0])
		}
	}
}

// TestSanitizeLine: SanitizeLine keeps exactly the well-formed SGR
// sequences and is Sanitize otherwise.
func TestSanitizeLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"\x1b[mreset", "\x1b[mreset"},
		{"\x1b[1;38;5;196mx\x1b[38:2::1:2:3my", "\x1b[1;38;5;196mx\x1b[38:2::1:2:3my"},
		{"\x1b[?25lhide", "hide"},            // private
		{"\x1b[1$mx", "x"},                   // intermediate byte
		{"\x1b[>4;2mx", "x"},                 // private marker, final m
		{"\x1b[2Kerase\x1b[3Aup", "eraseup"}, // not SGR
		{"t\x1b]0;title\x07x", "tx"},         // OSC
		{"\x1b]8;;http://x\x1b\\l", "l"},     // hyperlink
		{"\x1b[31", ""},                      // unterminated
		{"a\x1b", "a"},                       // lone ESC
		{"\u009b31mc1", "31mc1"},             // 8-bit CSI is a C1 control: dropped
		{"\x1b[31m\x07\x00‮x", "\x1b[31mx"},  // controls and bidi still go
	}
	for _, c := range cases {
		if got := SanitizeLine(c.in); got != c.want {
			t.Errorf("SanitizeLine(%q) = %q, want %q", c.in, got, c.want)
		}
		if got, want := Sanitize(c.in), Sanitize(c.want); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q: SanitizeLine may only add SGR", c.in, got, want)
		}
	}
}

// TestChildEnvStaysDumb pins the children's environment: no colour switch
// reaches them through it. TERM=dumb stays: no child's colour depends on
// it (pacman reads no TERM, paccache checks for a tty), systemd's tools
// take it as "no colour", and disable-unit parses `systemctl cat`, which
// with colour on draws [Install] and every directive with SGR around it
// (systemd's cat_file): the [Install] section would go unseen, and with
// it the Alias=, WantedBy= and Also= the denylist checks.
func TestChildEnvStaysDumb(t *testing.T) {
	want := []string{"PATH=/usr/bin", "LC_ALL=C", "TERM=dumb"}
	if got := ChildEnv(); !slices.Equal(got, want) {
		t.Errorf("ChildEnv() = %q, want exactly %q", got, want)
	}
}

// TestLineKeepsOnlySGR: a relayed line keeps its SGR colour and loses
// every other escape (a title, a hyperlink, a cursor move, a private CSI,
// a malformed one) and control character, as SanitizeLine says; the other
// text fields of the same run (the progress label, the result's error)
// stay plain. It fails on a helper whose line events drop SGR too.
func TestLineKeepsOnlySGR(t *testing.T) {
	raw := "\x1b[1;34m::\x1b[0m \x1b[1mSynchronizing\x1b[0m\x1b]0;title\x07 \x1b[2K\x1b[?25l\x1b[38;5;196;48:2::1:2:3mx\x1b[m" +
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\ \x1b[1$mbad\x07\x00"
	want := "\x1b[1;34m::\x1b[0m \x1b[1mSynchronizing\x1b[0m \x1b[38;5;196;48:2::1:2:3mx\x1b[mlink bad"
	f := newFake().on(paccacheArgv("3"), reply{out: raw + "\n", err: "\x1b[31merror:\x1b[0m boom\n", exit: 1})
	o, out := rootOpts(t, f)
	Run([]string{"paccache", "3"}, o)
	evs := events(t, out)
	var texts []string
	for _, l := range ofType(evs, EventLine) {
		texts = append(texts, l["text"].(string))
	}
	if !slices.Equal(texts, []string{want, "\x1b[31merror:\x1b[0m boom"}) {
		t.Errorf("line texts\n got %q\nwant %q", texts, []string{want, "\x1b[31merror:\x1b[0m boom"})
	}
	for _, ev := range evs {
		if ev["t"] == EventLine {
			continue
		}
		for k, v := range ev {
			if s, ok := v.(string); ok && strings.ContainsRune(s, 0x1b) {
				t.Errorf("%s event's %s holds an escape: %q", ev["t"], k, s)
			}
		}
	}
}
