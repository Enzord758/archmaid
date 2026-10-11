package ui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestPkexecShimNeedsTheFlag: the GUI harness's pkexec shim answers only a
// call shaped like helperclient.Argv's (--disable-internal-agent first, then
// the helper's path), exit 126 for anything else, so a future change that
// drops the flag (pkexec would then start its own text agent on the
// terminal) fails the real-Driver scenarios instead of passing silently. A
// refused verb with the right shape is the shim's 127.
func TestPkexecShimNeedsTheFlag(t *testing.T) {
	dir, env := installShims(t, shimOpts{refuse: []string{steps.VerbPaccache}})
	for _, c := range []struct {
		name string
		args []string
		want int
	}{
		{"no flag", []string{helperclient.HelperPath, steps.VerbPaccache}, 126},
		{"flag after the path", []string{helperclient.HelperPath, helperclient.DisableInternalAgent, steps.VerbPaccache}, 126},
		{"wrong path", []string{helperclient.DisableInternalAgent, "/tmp/helper", steps.VerbPaccache}, 126},
		{"right shape, refused verb", []string{helperclient.DisableInternalAgent, helperclient.HelperPath, steps.VerbPaccache}, 127},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(dir, "pkexec"), c.args...)
			cmd.Env = env
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != c.want {
				t.Errorf("pkexec %q: %v, want exit %d", c.args, err, c.want)
			}
		})
	}
}
