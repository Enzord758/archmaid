package sanitize

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv like every package's, though
// nothing here executes anything yet.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
