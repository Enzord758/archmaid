package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The TUI's side of askpass: the session's server wired up for privileged() and
// unwired on stop, the env reaching a real step child, and the model answering
// a request from the popup — su first, the password written only once it's
// right — or cancelling it. The server, the client, the peer check and the
// Request itself are internal/privilege's, and tested there.

// askpassTimeout bounds every wait here, so a broken round trip fails instead
// of hanging the suite.
const askpassTimeout = 3 * time.Second

// recv waits for one value on ch, failing the test after askpassTimeout.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(askpassTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// TestAskpassServerWiring: startAskpassServer starts a server under
// XDG_RUNTIME_DIR and wires its wrapper up for privileged() (sudo steps get -A),
// without ever setting SUDO_ASKPASS in archmaid's own environment;
// stopAskpassServer removes it and unwires the wrapper. Without
// XDG_RUNTIME_DIR askpass stays unwired, with no /tmp fallback, and stopping
// a nil server is safe.
func TestAskpassServerWiring(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	withAskpassWrapper(t, "")

	s := startAskpassServer()
	if s == nil {
		t.Fatal("no server with a usable XDG_RUNTIME_DIR")
	}
	if askpassWrapper == "" || askpassWrapper != s.Wrapper() || filepath.Dir(filepath.Dir(askpassWrapper)) != runtimeDir {
		t.Fatalf("askpassWrapper = %q, want the server's wrapper under %s", askpassWrapper, runtimeDir)
	}
	if got := os.Getenv("SUDO_ASKPASS"); got != "/from/the/users/shell" {
		t.Fatalf("SUDO_ASKPASS = %q: the server must not touch archmaid's own environment", got)
	}
	if spec := privileged().Command([]string{"sudo", "true"}, true); spec.Args[0] != "-A" {
		t.Fatalf("sudo step args = %v with askpass wired, want -A first", spec.Args)
	}
	stopAskpassServer(s)
	if askpassWrapper != "" {
		t.Fatalf("askpassWrapper = %q after stop, want it unwired", askpassWrapper)
	}
	if _, err := os.Stat(filepath.Dir(s.Wrapper())); !os.IsNotExist(err) {
		t.Fatalf("askpass dir still exists after stop (err %v)", err)
	}

	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)
	if s := startAskpassServer(); s != nil || askpassWrapper != "" {
		t.Fatalf("server %v, askpassWrapper %q without XDG_RUNTIME_DIR; want askpass unwired", s, askpassWrapper)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("left %d entries in TMPDIR", len(entries))
	}
	stopAskpassServer(nil)
}

// TestStepChildSeesAskpassEnv: the env reaches the real child — a shimmed
// topgrade (a needsSudo step) sees the wrapper, a plain step sees nothing.
func TestStepChildSeesAskpassEnv(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	old := askpassWrapper
	askpassWrapper = "/wrapper"
	t.Cleanup(func() { askpassWrapper = old })

	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '[%s]\\n' \"$SUDO_ASKPASS\"\n"
	for _, name := range []string{"topgrade", "yay"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, c := range []struct {
		s    steps.Step
		want string
	}{
		{steps.Step{Argv: []string{"topgrade"}, NeedsPriv: true}, "[/wrapper]"},
		{steps.Step{Argv: []string{"yay"}}, "[]"},
	} {
		ch := make(chan proc.Msg, 8)
		runStep(c.s, ch, nil)
		lines, err := drainStep(ch)
		if err != nil || len(lines) != 1 || lines[0] != c.want {
			t.Fatalf("%s saw SUDO_ASKPASS %v (err %v), want %s", c.s.Command(), lines, err, c.want)
		}
	}
}

// TestTUIServerWritesTheSudoWrapper: the TUI's askpass Server writes the
// sudo wrapper (a 0700 file in a 0700 dir inside its own) and
// privileged() puts it first on the PATH of a step that gets the askpass,
// only that step; stopping the Server removes it and unwires it.
func TestTUIServerWritesTheSudoWrapper(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Sudo, "exit 0")
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("PATH", "/x:/y")
	withAskpassWrapper(t, "")
	oldDir := askpassSudoDir
	t.Cleanup(func() { askpassSudoDir = oldDir })

	s := startAskpassServer()
	if s == nil {
		t.Fatal("no server")
	}
	dir := askpassSudoDir
	if dir == "" || dir != s.SudoDir() || filepath.Dir(dir) != filepath.Dir(s.Wrapper()) {
		t.Fatalf("askpassSudoDir = %q, want the server's sudo dir beside %s", dir, s.Wrapper())
	}
	for path, want := range map[string]os.FileMode{dir: os.ModeDir | 0o700, filepath.Join(dir, "sudo"): 0o700} {
		if fi, err := os.Lstat(path); err != nil || fi.Mode() != want {
			t.Fatalf("%s: %v (err %v), want %v", path, fi.Mode(), err, want)
		}
	}
	pathOf := func(env []string) string {
		return privilege.ChildPATH(env, nil)
	}
	if got := pathOf(privileged().Command([]string{"topgrade"}, true).Env); got != dir+":/x:/y" {
		t.Fatalf("a NeedsPriv step's PATH = %q, want the sudo dir first", got)
	}
	if got := pathOf(privileged().Command([]string{"yay", "-Sc"}, false).Env); got != "/x:/y" {
		t.Fatalf("a plain step's PATH = %q, want it untouched", got)
	}
	stopAskpassServer(s)
	if askpassSudoDir != "" || askpassWrapper != "" {
		t.Fatalf("still wired after stop: %q, %q", askpassSudoDir, askpassWrapper)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sudo wrapper's dir is still there after stop (err %v)", err)
	}
}
