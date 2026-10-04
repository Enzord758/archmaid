package helpertest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// TestMain runs these tests under testenv: a scratch home, and traps first on
// PATH for su, sudo, pkexec and the like. Each test puts its own pkexec shim
// ahead of the trap; the real pkexec and helper are never reached.
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// shim installs a fake pkexec that records its argv (one arg per line) and
// answers as a granted paccache run, and returns the argv file.
func shim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\ncat <<'EOF'\n" +
		`{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}` + "\n" +
		`{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}` + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argv
}

// standIn makes an empty stand-in helper file and returns its path.
func standIn(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archmaid-helper")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var paccache = steps.Step{Verb: steps.VerbPaccache}

func run(t *testing.T) helperclient.Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return helperclient.Run(ctx, 1, paccache, func(engine.Input) {})
}

// TestUseHelperFileKeepsArgv: the hook moves only the install check. With it
// set to a stand-in, Run gets to pkexec, and pkexec's first argument is still
// HelperPath (polkit's exec.path), never the stand-in; Argv says the same.
func TestUseHelperFileKeepsArgv(t *testing.T) {
	argvFile := shim(t)
	p := standIn(t)
	helpertest.UseHelperFile(t, p)
	if o := run(t); o.Err != nil || !o.Granted {
		t.Fatalf("Run = %+v, want a granted run", o)
	}
	got, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("pkexec never ran: %v", err)
	}
	args := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(args) < 2 || args[0] != helperclient.HelperPath || args[1] != steps.VerbPaccache {
		t.Errorf("pkexec got %q, want %s paccache …", args, helperclient.HelperPath)
	}
	if strings.Contains(string(got), p) {
		t.Errorf("pkexec got the stand-in %s: %q", p, args)
	}
	argv, err := helperclient.Argv(paccache)
	if err != nil || len(argv) < 2 || argv[1] != helperclient.HelperPath {
		t.Errorf("Argv = %q, %v; want argv[1] %s", argv, err, helperclient.HelperPath)
	}
}

// TestUseHelperFileMovesCheck: the check stats the hook's path (a missing one
// fails Run before pkexec starts, naming it), and t's end restores
// HelperPath.
func TestUseHelperFileMovesCheck(t *testing.T) {
	argvFile := shim(t)
	missing := filepath.Join(t.TempDir(), "missing", "archmaid-helper")
	t.Run("missing", func(t *testing.T) {
		helpertest.UseHelperFile(t, missing)
		o := run(t)
		if !errors.Is(o.Err, helperclient.ErrHelperMissing) || !strings.Contains(o.Err.Error(), missing) {
			t.Fatalf("Run = %+v, want ErrHelperMissing at %s", o, missing)
		}
		if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
			t.Error("pkexec ran")
		}
	})
	// Back on HelperPath: whether or not this machine has the helper, the
	// check no longer names the stand-in.
	o := run(t)
	if fi, err := os.Stat(helperclient.HelperPath); err == nil && fi.Mode().IsRegular() {
		if o.Err != nil {
			t.Errorf("Run = %+v, want the shim's granted run", o)
		}
	} else if !errors.Is(o.Err, helperclient.ErrHelperMissing) || !strings.Contains(o.Err.Error(), helperclient.HelperPath) {
		t.Errorf("Run = %+v after the subtest, want ErrHelperMissing at %s", o, helperclient.HelperPath)
	}
}
