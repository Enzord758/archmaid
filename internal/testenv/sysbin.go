package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// init points every internal/sysbin path (archmaid's su, sudo, pkexec,
// pkttyagent and topgrade, absolute in the product) back at its bare name,
// so it is looked up on PATH again, where Run's traps sit first and a
// test's own shim ahead of them. It is an init, not part of Run, because a
// test binary also runs as a re-exec'd child (the GUI harness's windows,
// the askpass relays) that never calls Run: a variable Run set would not
// cross the exec, the child would run the real /usr/bin program, and no
// PATH trap could catch it. An init runs in every process of the binary.
//
// No product binary may link this package (scripts/check.sh): it would
// undo the absolute paths there.
func init() { BareSysbin() }

// BareSysbin points every internal/sysbin path at its bare name.
func BareSysbin() {
	for _, p := range sysbin.All() {
		*p = filepath.Base(*p)
	}
}

// TB is the part of testing.TB the helpers below use. (The package does
// link testing, for testing.CoverMode in cover.go, so a binary that links
// it by mistake links testing too; scripts/check.sh fails a main package
// that links either.)
type TB interface {
	Helper()
	TempDir() string
	Cleanup(func())
	Fatal(args ...any)
	Setenv(key, value string)
}

// UseSysbin points the internal/sysbin path *p at an executable sh script
// of its own (body after the #! line), at an absolute path in a fresh dir
// that is not on PATH, for the rest of t, and returns that path. It is how
// a test proves a call site runs the sysbin path and not a name on PATH
// (with Decoys first on PATH). The variable is process-wide: such a test
// must not run in parallel.
func UseSysbin(t TB, p *string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abs", filepath.Base(*p))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := *p
	*p = path
	t.Cleanup(func() { *p = old })
	return path
}

// Decoys makes PATH a fresh dir holding one sh script per name, which
// appends "name argv" to the returned log and exits 1, for the rest of t:
// a call that looks the name up on PATH instead of running its sysbin path
// lands there. Nothing else is on PATH, so the decoys and a sysbin shim may
// use sh builtins only.
func Decoys(t TB, names ...string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "decoys.log")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		script := "#!/bin/sh\nprintf '%s\\n' \"" + name + " $*\" >> " + shQuote(log) + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return log
}

// checkSysbinBare is Run's check that the init above has done its work and
// nothing has undone it before the tests start.
func checkSysbinBare() error {
	for _, p := range sysbin.All() {
		if strings.Contains(*p, "/") {
			return fmt.Errorf("internal/sysbin path %s is not bare under testenv: a test could run the real one past the PATH traps", *p)
		}
	}
	return nil
}
