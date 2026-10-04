package downloads

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain keeps these tests off the real home (PasswdHome and the resolver
// look at it) and fails if one touched the real state directory anyway.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
