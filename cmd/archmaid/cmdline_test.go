package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain isolates HOME and the XDG dirs and traps the privileged binaries
// (internal/testenv), like every test package.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// tree is every path under dir, relative and sorted.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// TestStartupDeletesNothing is the command line's half of the regression
// test for the deleted run folder (internal/tui's TestStartDeletesNothing
// is the TUI's): every way startup can go — a dry run, --help, a bad flag,
// the askpass relay, a plain start — returns the right code and flags and
// leaves HOME, state dir included, exactly as it was. The old main() pruned
// right after parseArgs, so --dryrun deleted the oldest run, and ignored
// unknown flags (--help started the TUI and pruned too).
func TestStartupDeletesNothing(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "archmaid", "20200101-0900")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup.log"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		args      []string
		code      int
		start     bool
		flags     flags
		out, errs string // substrings of stdout and stderr ("" = must be empty)
	}{
		{name: "dryrun", args: []string{"--dryrun"}, start: true, flags: flags{dryRun: true}},
		{name: "plain start", args: nil, start: true},
		{name: "downloads", args: []string{"--downloads", "/data/dl", "--dryrun"}, start: true,
			flags: flags{dryRun: true, downloads: "/data/dl"}},
		{name: "help", args: []string{"--help"}, out: "Usage: archmaid"},
		{name: "-h", args: []string{"--dryrun", "-h"}, out: "Usage: archmaid"},
		{name: "unknown flag", args: []string{"--bogus"}, code: 2, errs: `unknown option "--bogus"`},
		{name: "typo of dryrun", args: []string{"--dry-run"}, code: 2, errs: `unknown option "--dry-run"`},
		{name: "positional", args: []string{"clean"}, code: 2, errs: `unexpected argument "clean"`},
		{name: "bad downloads", args: []string{"--downloads"}, code: 2, errs: "--downloads needs a folder"},
		{name: "askpass relay", args: []string{"--askpass", filepath.Join(t.TempDir(), "no.sock"), "Password:"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tree(t, home)
			var stdout, stderr bytes.Buffer
			f, code, start := startup(tc.args, &stdout, &stderr)
			if code != tc.code || start != tc.start || f != tc.flags {
				t.Fatalf("startup(%q) = %+v, %d, %v; want %+v, %d, %v (stderr %q)",
					tc.args, f, code, start, tc.flags, tc.code, tc.start, stderr.String())
			}
			for _, w := range []struct{ name, got, want string }{
				{"stdout", stdout.String(), tc.out}, {"stderr", stderr.String(), tc.errs},
			} {
				if (w.want == "") != (w.got == "") || !strings.Contains(w.got, w.want) {
					t.Fatalf("%s = %q, want it to contain %q", w.name, w.got, w.want)
				}
			}
			if after := tree(t, home); !slices.Equal(before, after) {
				t.Fatalf("startup(%q) changed HOME:\nbefore %v\nafter  %v", tc.args, before, after)
			}
		})
	}
}

// TestParseArgsDownloads: --downloads takes its folder as the next argument
// or after "=", and refuses to be given none.
func TestParseArgsDownloads(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{args: []string{"--downloads", "/data/dl"}, want: "/data/dl"},
		{args: []string{"--dryrun", "--downloads=/data/dl"}, want: "/data/dl"},
		{args: []string{"--downloads"}, wantErr: true},
		{args: []string{"--downloads="}, wantErr: true},
		{args: []string{"--dryrun"}, want: ""},
		// Two flags, not a folder named --dryrun: refused, so the dry run
		// isn't silently lost.
		{args: []string{"--downloads", "--dryrun"}, wantErr: true},
		{args: []string{"--downloads", "-x"}, wantErr: true},
		{args: []string{"--downloads=-x"}, wantErr: true},
		{args: []string{"--downloads", "./-x"}, want: "./-x"},
	} {
		f, err := parseArgs(tc.args)
		if (err != nil) != tc.wantErr || (!tc.wantErr && f.downloads != tc.want) {
			t.Fatalf("parseArgs(%q): flag=%q err=%v; want flag=%q err=%v",
				tc.args, f.downloads, err, tc.want, tc.wantErr)
		}
		if tc.wantErr && f != (flags{}) {
			t.Fatalf("parseArgs(%q) = %+v despite failing", tc.args, f)
		}
	}

	_, err := parseArgs([]string{"--downloads", "--dryrun"})
	if err == nil || !strings.Contains(err.Error(), `not the option "--dryrun"`) {
		t.Fatalf("err = %v, want it to name the option", err)
	}
}
