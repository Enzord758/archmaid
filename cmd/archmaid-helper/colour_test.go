package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// colourRunner is a pacman that colours as the real one does into a
// pipe: only with --color always. Every relayed line also carries a
// title (OSC) and a cursor move, which must never get through. Its
// pacman-conf names cache as pacman's one cache dir.
type colourRunner struct {
	anyRunner
	cache string
}

func (r colourRunner) Query(argv []string) (string, string, int, error) {
	if len(argv) == 2 && argv[0] == helper.PacmanConf && argv[1] == "CacheDir" {
		return r.cache + "/\n", "", 0, nil
	}
	return r.anyRunner.Query(argv)
}

func (colourRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	if slices.Contains(helper.CallEnv(argv), helper.SystemdColours) {
		// systemd's tools colour into a pipe only with SYSTEMD_COLORS in
		// the call's env (task #133): this stand-in reads the env the
		// helper's runner would give this argv.
		line("out", "\x1b]0;pwned\x07\x1b[2K\x1b[1;32m"+filepath.Base(argv[0])+"\x1b[0m done")
		return 0, nil
	}
	if len(argv) < 2 || argv[0] != helper.Pacman {
		line("out", "\x1b]0;pwned\x07plain "+filepath.Base(argv[0]))
		return 0, nil
	}
	i := slices.Index(argv, "--color")
	if i >= 0 && i+1 < len(argv) && argv[i+1] == "always" {
		line("out", "\x1b]0;pwned\x07\x1b[2K\x1b[1;34m::\x1b[0m\x1b[1m pacman "+argv[1]+"\x1b[0m")
	} else {
		line("out", "\x1b]0;pwned\x07\x1b[2K:: pacman "+argv[1])
	}
	return 0, nil
}

// TestHelperColourReachesTheClient: the helper's own stream for the
// full-cleanup call the GUI builds (run in-process as a dev build on
// colourRunner) read by helperclient.Run, as the GUI's Driver reads it:
// the pacman parts' lines reach the engine with their SGR colour and
// nothing else (no title, no cursor move); the relayed systemd tools
// (tmpfiles, the journal vacuum) colour as they do with SYSTEMD_COLORS in
// their own call's env (task #133), paccache, which can't be forced, stays
// plain. Under NO_COLOR the
// client drops the colour (the helper's switch is a constant in it, so
// the client is where NO_COLOR is honoured). It fails on a helper that
// strips SGR from its line events or doesn't ask pacman for colour.
func TestHelperColourReachesTheClient(t *testing.T) {
	var parts []steps.Step
	for _, s := range buildAllSteps(t) {
		if s.Combine == steps.VerbFullCleanup && s.Verb != steps.VerbRemoveOrphans {
			parts = append(parts, s)
		}
	}
	call := steps.Step{ID: steps.VerbFullCleanup, Verb: steps.VerbFullCleanup, NeedsPriv: true, Parts: parts}
	argv, err := helperclient.Argv(call)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	// The cache holds an empty download folder pacman left; the line
	// saying why the tidy left it alone (made just now: its ctime, which
	// a test can't set back) reaches the client plain. That it is removed
	// once old enough is the helper package's test (leftovers_test.go).
	cache := filepath.Join(dir, "pkg")
	leftover := filepath.Join(cache, "download-Clr123")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := helper.Run(helperArgs(t, argv), helper.Options{
		Runner: colourRunner{cache: cache}, Stdout: &out, Stderr: &errOut, Euid: 1000, Dev: true,
		LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
	}); code != helper.ExitOK {
		t.Fatalf("helper exit %d: %s", code, errOut.String())
	}
	stream := filepath.Join(dir, "stream")
	if err := os.WriteFile(stream, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte("#!/bin/sh\nwhile IFS= read -r l; do printf '%s\\n' \"$l\"; done < '"+stream+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	standIn := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(standIn, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	helpertest.UseHelperFile(t, standIn)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	coloured := []string{
		"Left " + leftover + " alone: changed within the last 2 minutes, so a download may be starting in it",
		"\x1b[1;34m::\x1b[0m\x1b[1m pacman -Sc\x1b[0m",
		"\x1b[1;32msystemd-tmpfiles\x1b[0m done",
		"\x1b[1;32mjournalctl\x1b[0m done",
		"\x1b[1;34m::\x1b[0m\x1b[1m pacman -Qk\x1b[0m",
	}
	for _, noColour := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			t.Setenv("NO_COLOR", noColour)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var lines []string
			var exited *engine.StepExited
			helperclient.Run(ctx, 1, call, func(in engine.Input) {
				switch in := in.(type) {
				case engine.StepOutput:
					for _, l := range in.Lines {
						// the part labels are the client's own lines
						if !strings.HasPrefix(l, "Cleaning") && !strings.HasPrefix(l, "Removing") &&
							!strings.HasPrefix(l, "Vacuuming") && !strings.HasPrefix(l, "Checking") {
							lines = append(lines, l)
						}
					}
				case engine.StepExited:
					exited = &in
				}
			})
			if exited == nil || exited.Err != nil {
				t.Fatalf("StepExited = %+v", exited)
			}
			want := []string{"plain paccache"}
			for _, l := range coloured {
				if noColour != "" {
					l = strings.NewReplacer("\x1b[1;34m", "", "\x1b[0m", "", "\x1b[1m", "", "\x1b[1;32m", "").Replace(l)
				}
				want = append(want, l)
			}
			if !slices.Equal(lines, want) {
				t.Errorf("output lines\n got %q\nwant %q", lines, want)
			}
		})
	}
}
