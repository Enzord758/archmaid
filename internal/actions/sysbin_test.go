package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestRepairRecheckRunsSysbinSudo: Repair's root recheck runs sysbin.Sudo
// (in the product /usr/bin/sudo), never a sudo found on PATH.
func TestRepairRecheckRunsSysbinSudo(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	testenv.UseSysbin(t, &sysbin.Sudo, "printf '%s\\n' \"$*\" >> '"+log+"'\nexit 0")
	decoys := testenv.Decoys(t, "sudo", "su", "pkexec")
	if _, _, err := brokenPackages(context.Background(), []string{"liba"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if got, want := strings.TrimSpace(string(b)), "-n pacman -Qkq --color never -- liba"; got != want {
		t.Errorf("sysbin sudo argv %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(decoys); len(b) > 0 {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", b)
	}
}
