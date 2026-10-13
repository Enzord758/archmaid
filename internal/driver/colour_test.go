package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

// TestColourReachesTheChildren: a whole Full Cleanup on the real Driver
// (the GUI's polkit path, fullCleanupRig) starts topgrade with
// CLICOLOR_FORCE=1 in its environment and yay with --color always, as
// actions/colour.go asks, while archmaid's own environment never gets
// CLICOLOR_FORCE; with NO_COLOR set neither child gets its switch. The
// topgrade shim colours its line only when the variable is set (as the
// console crate does into a pipe), and that colour reaches the
// OutputChunk intact.
func TestColourReachesTheChildren(t *testing.T) {
	for _, noColour := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			r, calls := fullCleanupRig(t, grantedStream(t))
			t.Setenv("NO_COLOR", noColour) // after the rig, which clears it
			dir := filepath.Dir(calls)
			topgrade := "#!/bin/sh\n" +
				"printf '%s|%s\\n' \"${CLICOLOR_FORCE-unset}\" \"$*\" >> '" + filepath.Join(dir, "topgrade.env") + "'\n" +
				"if [ \"${CLICOLOR_FORCE:-0}\" != 0 ]; then printf '\\033[1;32mupdated\\033[0m\\n'; else echo updated; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(topgrade), 0o700); err != nil {
				t.Fatal(err)
			}
			evs := r.run(actions.IDFullCleanup, func(ev engine.Event) engine.Command {
				if c, ok := ev.(engine.NeedsConfirm); ok {
					return engine.Confirm{Prompt: c.ID, Yes: true}
				}
				return nil
			})

			wantEnv, wantYay, wantLine := "1|-y --no-retry --disable hyprpm", "-Sc --aur --noconfirm --color always", "\x1b[1;32mupdated\x1b[0m"
			if noColour != "" {
				wantEnv, wantYay, wantLine = "unset|-y --no-retry --disable hyprpm", "-Sc --aur --noconfirm", "updated"
			}
			if got := readFileLines(t, filepath.Join(dir, "topgrade.env")); !slices.Equal(got, []string{wantEnv}) {
				t.Errorf("topgrade ran with CLICOLOR_FORCE|argv %q, want %q", got, wantEnv)
			}
			if got := readFileLines(t, filepath.Join(dir, "yay.log")); !slices.Equal(got, []string{wantYay}) {
				t.Errorf("yay ran %q, want %q", got, wantYay)
			}
			var out []string
			for _, ev := range evs {
				if c, ok := ev.(engine.OutputChunk); ok {
					out = append(out, c.Lines...)
				}
			}
			if !slices.Contains(out, wantLine) {
				t.Errorf("output %q, want topgrade's %q", out, wantLine)
			}
			if v, set := os.LookupEnv("CLICOLOR_FORCE"); set {
				t.Errorf("archmaid's own environment has CLICOLOR_FORCE=%q", v)
			}
			if strings.Contains(strings.Join(pkexecCalls(t, calls), "\n"), "color") {
				t.Errorf("the root helper's call got a colour switch: %q", pkexecCalls(t, calls))
			}
		})
	}
}
