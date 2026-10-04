package dms

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv: a scratch home, and traps first on
// PATH for su, sudo, pkexec and the like, so a test that forgets its shim
// fails instead of reaching the real one.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
