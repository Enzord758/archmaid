package health

import (
	"os"
	"testing"
)

// TestOwnedPrivate (#271): a file of another owner is never trusted.
func TestOwnedPrivate(t *testing.T) {
	fi, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ownedPrivate(fi, os.Getuid()) || ownedPrivate(fi, os.Getuid()+1) {
		t.Error("ownedPrivate does not compare the owner")
	}
}
