package actions

import (
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestColourSwitches: in every queue, under sudo and under polkit, the
// steps whose child writes colour into the pipe carry their switch:
// topgrade CLICOLOR_FORCE=1 in its own Env, yay --color always at the end
// of its argv. Nothing else gets an Env (a root step's sudo or helper
// wouldn't pass it on anyway), and under NO_COLOR nothing gets either.
func TestColourSwitches(t *testing.T) {
	for _, noColour := range []string{"", "1"} {
		t.Setenv("NO_COLOR", noColour)
		for _, polkit := range []bool{false, true} {
			for _, id := range []string{IDFullCleanup, IDTopgrade} {
				a, _ := ByID(id)
				env := testEnv(true)
				env.Polkit = polkit
				for _, s := range a.Steps(env) {
					where := id + "/" + s.ID
					if polkit {
						where += " (polkit)"
					}
					if noColour != "" {
						where += " NO_COLOR"
					}
					wantEnv := []string(nil)
					if s.ID == steps.IDTopgrade && noColour == "" {
						wantEnv = []string{"CLICOLOR_FORCE=1"}
					}
					if !slices.Equal(s.Env, wantEnv) {
						t.Errorf("%s: Env %q, want %q", where, s.Env, wantEnv)
					}
					colour := len(s.Argv) >= 2 && slices.Equal(s.Argv[len(s.Argv)-2:], []string{"--color", "always"})
					if wantArg := s.ID == steps.IDYayCache && noColour == ""; colour != wantArg {
						t.Errorf("%s: argv %q, want --color always at its end: %v", where, s.Argv, wantArg)
					}
				}
			}
		}
	}
}
