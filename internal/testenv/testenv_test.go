package testenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The child half of TestStateGuardFailsPackage: stateChildEnv names the
// change to make, stateChildDir the scratch XDG_STATE_HOME it was started
// with (its Run has swapped XDG_STATE_HOME by the time the test runs).
const (
	stateChildEnv = "ARCHMAID_TESTENV_STATE_CHILD"
	stateChildDir = "ARCHMAID_TESTENV_STATE_DIR"
)

// stateNested is the file the child changes: two levels down, where the old
// top-level-only check could not see it.
const stateNested = "archmaid/20260101-0000/cleanup.log"

// TestStateChild makes one change under the scratch state dir and passes; the
// child's Run must still fail the package. Skipped unless re-exec'd.
func TestStateChild(t *testing.T) {
	op := os.Getenv(stateChildEnv)
	if op == "" {
		t.Skip("child half of TestStateGuardFailsPackage")
	}
	dir := os.Getenv(stateChildDir)
	// Belt and braces: only ever change a scratch tree the parent made.
	tmp := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(dir), tmp) {
		t.Fatalf("%s=%q is not under %s: refusing to change it", stateChildDir, dir, tmp)
	}
	file := filepath.Join(dir, stateNested)
	if b, err := os.ReadFile(file); err != nil || string(b) != "scratch\n" {
		t.Fatalf("%s is not the parent's scratch file (%v): refusing to change it", file, err)
	}
	var err error
	switch op {
	case "none":
	case "delete":
		err = os.Remove(file)
	case "grow":
		err = os.WriteFile(file, []byte("scratch\nmore\n"), 0o600)
	case "touch":
		now := time.Now()
		err = os.Chtimes(file, now, now)
	case "add":
		err = os.WriteFile(filepath.Join(filepath.Dir(file), "new.log"), nil, 0o600)
	default:
		t.Fatalf("unknown op %q", op)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestStateGuardFailsPackage proves Run's state-dir check on a scratch
// XDG_STATE_HOME: a test that deletes, grows, touches or adds a file nested
// below the state dir fails its package, with the path in the report; one
// that changes nothing passes.
func TestStateGuardFailsPackage(t *testing.T) {
	for _, op := range []string{"none", "delete", "grow", "touch", "add"} {
		t.Run(op, func(t *testing.T) {
			state := t.TempDir()
			file := filepath.Join(state, stateNested)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("scratch\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// An old mtime, so "touch" is a change at any clock resolution.
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(file, past, past); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestStateChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), stateChildEnv+"="+op, stateChildDir+"="+state, "XDG_STATE_HOME="+state)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if op == "none" {
				if err != nil {
					t.Fatalf("unchanged state dir failed the child: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
				}
				return
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("child: %v, want exit 1\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "PASS") {
				t.Errorf("the child's test itself should pass (only Run fails):\n%s", stdout.String())
			}
			out := stderr.String()
			if !strings.Contains(out, "the tests changed the real state dir") {
				t.Errorf("no state-dir report on stderr:\n%s", out)
			}
			want := "/20260101-0000/cleanup.log"
			if op == "add" {
				want = "/20260101-0000/new.log"
			}
			if !strings.Contains(out, want) {
				t.Errorf("report doesn't name %s:\n%s", want, out)
			}
		})
	}
}

// TestGuardScriptFound: Run's lookup finds the repo's script from a package
// dir, so the guard can't be silently skipped.
func TestGuardScriptFound(t *testing.T) {
	p, err := guardScript()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(p)) != "scripts" {
		t.Fatalf("guardScript() = %s", p)
	}
}
