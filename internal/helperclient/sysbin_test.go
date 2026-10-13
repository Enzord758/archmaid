package helperclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestRunStartsSysbinPkexec: Run starts sysbin.Pkexec (in the product
// /usr/bin/pkexec), never a pkexec found on PATH, with Argv's arguments.
// The argv[0] the tests compare stays Pkexec's name.
func TestRunStartsSysbinPkexec(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	// sh builtins only: PATH is the decoys'.
	testenv.UseSysbin(t, &sysbin.Pkexec, "printf '%s\\n' \"$@\" > '"+argv+"'\n"+
		"printf '%s\\n' '"+startCheck+"' '"+resultOK+"'")
	decoys := testenv.Decoys(t, "pkexec", "sudo", "su")
	fakeHelperInstalled(t, dir)
	r := runStep(t, repairCheck)
	if ex := r.exited(t); ex.Err != nil || ex.NotGranted {
		t.Fatalf("StepExited %+v, want success through the sysbin pkexec", ex)
	}
	got, _ := os.ReadFile(argv)
	if want := DisableInternalAgent + "\n" + HelperPath + "\ncheck-files\namd-ucode\nbig\nnfs-utils\n"; string(got) != want {
		t.Errorf("sysbin pkexec got argv %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(decoys); len(b) > 0 {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", b)
	}
}
