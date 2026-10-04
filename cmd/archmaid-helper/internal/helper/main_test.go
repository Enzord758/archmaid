package helper

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv: a scratch home, and traps first on
// PATH for su, sudo, pkexec and the like. The verbs' tests never execute
// anything (fakeRunner); the ExecRunner tests run harmless absolute paths
// (/usr/bin/env, /usr/bin/cat, /bin/sh) with PATH=/usr/bin, which the traps
// can't reach, so testenv's TestNoAbsolutePrivilegedExec is what keeps a
// privileged binary out of the helper's absolute paths.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
