package driver

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv: a scratch home, and traps first
// on PATH for su, sudo, pkexec and the like. No test here reaches a real
// privileged binary: Priv is a fake, and the askpass test puts its own su
// shim first on PATH.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
