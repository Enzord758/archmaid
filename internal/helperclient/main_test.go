package helperclient

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv: a scratch home, and traps first on
// PATH for su, sudo, pkexec and the like. The Run tests put their own pkexec
// shim ahead of the trap; nothing else here may reach pkexec.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
