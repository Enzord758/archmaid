package frontend

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain keeps these tests off the real home and the real pacman (the
// audit's foreign-package list, the downloads resolver), and fails if one
// touched the real state directory anyway.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
