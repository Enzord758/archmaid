package testenv

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// Review #134 (Dev 2) of the sysbin reset (#131).

// absSysbinChildEnv marks the re-exec'd half of
// TestRunRefusesAbsoluteSysbinAtStart: its init, which runs after
// sysbin.go's (a package file's init runs before a test file's), points
// sysbin.Sudo at an absolute path, one that doesn't exist, so even a Run
// that let it through could run nothing real.
const absSysbinChildEnv = "ARCHMAID_TESTENV_SYSBIN_ABS_CHILD"

func init() {
	if os.Getenv(absSysbinChildEnv) != "" {
		sysbin.Sudo = "/nonexistent-archmaid-review-134/sudo"
	}
}

// TestRunRefusesAbsoluteSysbinAtStart: Run itself refuses to start a test
// binary whose sysbin paths aren't bare when it starts (something undid
// the init, or the init is gone), before any test runs: the child exits 1
// with the refusal, and its one test never runs.
// TestRunRefusesAbsoluteSysbin covers only the check, not that Run calls it.
func TestRunRefusesAbsoluteSysbinAtStart(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run", "^TestRunRefusesAbsoluteSysbinAtStartChild$", "-test.v")
	cmd.Env = append(os.Environ(), absSysbinChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("child: %v, want exit 1 from Run\n%s", err, out)
	}
	if !strings.Contains(string(out), "/nonexistent-archmaid-review-134/sudo is not bare under testenv") {
		t.Errorf("child's output has no refusal naming the absolute sudo:\n%s", out)
	}
	if strings.Contains(string(out), "=== RUN") {
		t.Errorf("a test ran under an absolute sysbin path:\n%s", out)
	}
}

// TestRunRefusesAbsoluteSysbinAtStartChild is the child's one test; Run
// must refuse before it runs.
func TestRunRefusesAbsoluteSysbinAtStartChild(t *testing.T) {
	if os.Getenv(absSysbinChildEnv) == "" {
		t.Skip("child half of TestRunRefusesAbsoluteSysbinAtStart")
	}
	t.Error("ran with sysbin.Sudo =", sysbin.Sudo)
}
