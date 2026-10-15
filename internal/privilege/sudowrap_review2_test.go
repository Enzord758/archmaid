package privilege

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// envWithout is os.Environ() minus the variables named.
func envWithout(names ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(names, name) {
			out = append(out, kv)
		}
	}
	return out
}

// TestSudoWrapperExportsAskpassWhenCallerHasNone: the real sudo is a child
// process, so SUDO_ASKPASS has to be exported by the wrapper itself; a
// caller that had none (the usual case) must not leave it unexported.
// (The other tests give the caller its own SUDO_ASKPASS, which stays
// exported when the script only assigns it.)
func TestSudoWrapperExportsAskpassWhenCallerHasNone(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	w := wrapperFor(t, sudo, "/askpass")
	cmd := exec.Command(w, "pacman")
	cmd.Env = envWithout("SUDO_ASKPASS")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "ASKPASS=/askpass\x00-A\x00pacman\x00" {
		t.Fatalf("the real sudo got %q", got)
	}
}

// TestSudoWrapperExecsWithoutA: also when -A is left out the wrapper execs
// the real sudo (no shell stays between the caller and sudo: Request.Verify's
// chain, signals and exit status are the caller's own), and the exit status
// comes through.
func TestSudoWrapperExecsWithoutA(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, `echo "$PPID"; exit 7`)
	w := wrapperFor(t, sudo, "/askpass")
	cmd := exec.Command("/bin/sh", "-c", `echo "$$"; `+shellQuote(w)+` -n true; echo "rc=$?"`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("output %q", out)
	}
	if _, err := strconv.Atoi(lines[0]); err != nil || lines[1] != lines[0] || lines[2] != "rc=7" {
		t.Fatalf("output %q: want sudo's parent to be the caller (pid %s) and its exit status 7", out, lines[0])
	}
}
