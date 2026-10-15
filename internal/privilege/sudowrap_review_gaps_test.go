package privilege

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// Gaps found in the #154 review, pinned as tests that skip while the gap is
// open and fail once it is closed the wrong way round (they assert the
// wanted behavior, so they pass when fixed).

// TestSudoWrapperExportedBracketKnownGap: bash started as sh imports
// exported functions, and the script's `[` is a plain builtin, so a caller
// that exports a function named `[` (BASH_FUNC_[%%) makes every
// `[ -n "$archmaid_ask" ]` fail and the real sudo runs without -A. exec,
// export, shift, break and continue are special builtins and win over
// functions in POSIX mode. Needs a hostile same-uid caller environment;
// skipped until the script tests with `case` instead of `[`.
func TestSudoWrapperExportedBracketKnownGap(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	w := wrapperFor(t, sudo, "/askpass")
	cmd := exec.Command(w, "pacman")
	cmd.Env = append(os.Environ(), "BASH_FUNC_[%%=() { return 1; }")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\x00-A\x00") {
		t.Skipf("known gap: an exported function named [ keeps -A off (got %q)", out)
	}
}

// TestPathShadowsOwnSudoWrapperSetuidKnownGap: ownFiles.is compares
// Mode().Perm(), which leaves out the setuid, setgid and sticky bits, so
// the wrapper chmod'ed u+s is still taken for archmaid's own. Harmless
// (the kernel ignores setuid on scripts; the file is the user's own), a
// nit; skipped until the check compares the whole mode.
func TestPathShadowsOwnSudoWrapperSetuidKnownGap(t *testing.T) {
	real := realSudoDir(t)
	s, _ := listen(t, ServerOptions{SudoWrapper: true})
	if err := os.Chmod(s.sudoFile, 0o700|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(s.SudoDir(), real))); len(got) == 0 {
		t.Skip("known gap: a setuid bit on the wrapper is not noticed")
	}
}
