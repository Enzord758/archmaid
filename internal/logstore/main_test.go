package logstore

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain keeps these tests off the real home (New defaults to
// ~/.local/state/archmaid) and fails if one wrote there anyway.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
