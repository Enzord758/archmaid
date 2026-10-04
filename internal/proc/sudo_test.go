package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── a sudo that could prompt on the terminal never runs ──────────────────────
//
// Under the full-screen UI a sudo that asks on the tty is invisible, and every
// keystroke goes to it. Only `sudo -n` (never ask) and `sudo -A` (ask through
// SUDO_ASKPASS) may run; privilege.Sudo.Command builds those. These tests put
// a sudo shim first and alone on PATH (and name it by path where a path is
// tested), so the real sudo can't run even on code that fails them.

// shimSudo puts a sudo on PATH that records its argv and the SUDO_ASKPASS it
// saw ("unset" if none), and returns its directory.
func shimSudo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + dir + "/sudo.called\"\n" +
		"printf '%s' \"${SUDO_ASKPASS-unset}\" > \"" + dir + "/sudo.askpass\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

// sudoRan reports whether the shim ran, and the SUDO_ASKPASS it saw.
func sudoRan(dir string) (ran bool, askpass string) {
	if _, err := os.Stat(filepath.Join(dir, "sudo.called")); err != nil {
		return false, ""
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sudo.askpass"))
	return true, string(b)
}

func startSpec(t *testing.T, spec Spec) (*Child, error) {
	t.Helper()
	ch := make(chan Msg, 16)
	c := New(Options{}).Start(spec, ch, nil)
	_, err := drain(t, ch)
	return c, err
}

// TestStartRefusesAPromptingSudo: sudo without -n or -A first — by name or by
// path — is refused before anything runs.
func TestStartRefusesAPromptingSudo(t *testing.T) {
	dir := shimSudo(t)
	for _, spec := range []Spec{
		{Name: "sudo", Args: []string{"pacman", "-Syu"}},
		{Name: "sudo", Args: []string{"-v"}},
		{Name: "sudo", Args: []string{"-S", "-v"}},
		{Name: "sudo", Args: []string{"-k", "-n", "true"}},
		{Name: "sudo"},
		{Name: filepath.Join(dir, "sudo"), Args: []string{"true"}},
	} {
		c, err := startSpec(t, spec)
		if c != nil || !errors.Is(err, ErrSudoMayPrompt) {
			t.Errorf("Start(%s %q) = %v, %v; want nil and ErrSudoMayPrompt", spec.Name, spec.Args, c, err)
		}
		if ran, _ := sudoRan(dir); ran {
			t.Fatalf("Start(%s %q) ran sudo", spec.Name, spec.Args)
		}
	}
}

// TestStartSudoEnv: -n and -A run; with a nil Env the child inherits
// archmaid's environment minus any SUDO_ASKPASS (one the user's shell
// exported must not answer a `sudo -A`), while an explicit Env is used as
// given.
func TestStartSudoEnv(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	for _, flag := range []string{"-n", "-A"} {
		dir := shimSudo(t)
		if _, err := startSpec(t, Spec{Name: "sudo", Args: []string{flag, "true"}}); err != nil {
			t.Fatalf("sudo %s: %v", flag, err)
		}
		if ran, askpass := sudoRan(dir); !ran || askpass != "unset" {
			t.Fatalf("sudo %s: ran=%v SUDO_ASKPASS=%q; want it run without the inherited SUDO_ASKPASS", flag, ran, askpass)
		}
	}

	dir := shimSudo(t)
	env := []string{"PATH=" + dir, "SUDO_ASKPASS=/run/archmaid/askpass.sh"}
	if _, err := startSpec(t, Spec{Name: "sudo", Args: []string{"-A", "true"}, Env: env}); err != nil {
		t.Fatal(err)
	}
	if _, askpass := sudoRan(dir); askpass != "/run/archmaid/askpass.sh" {
		t.Fatalf("explicit Env: SUDO_ASKPASS=%q, want the caller's wrapper", askpass)
	}
}

// TestQueryRefusesAPromptingSudo: Query holds sudo to the same rule, and a
// `sudo -n` it runs doesn't inherit SUDO_ASKPASS either.
func TestQueryRefusesAPromptingSudo(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	dir := shimSudo(t)
	if _, err := Query(context.Background(), "sudo", "pacman", "-Qkq"); !errors.Is(err, ErrSudoMayPrompt) {
		t.Fatalf("Query(sudo pacman -Qkq) = %v, want ErrSudoMayPrompt", err)
	}
	if ran, _ := sudoRan(dir); ran {
		t.Fatal("Query ran a sudo that could prompt")
	}
	if _, err := Query(context.Background(), "sudo", "-n", "pacman", "-Qkq"); err != nil {
		t.Fatal(err)
	}
	if ran, askpass := sudoRan(dir); !ran || askpass != "unset" {
		t.Fatalf("Query(sudo -n …): ran=%v SUDO_ASKPASS=%q", ran, askpass)
	}
}

// TestCheckSudo: only sudo is held to the rule.
func TestCheckSudo(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		ok   bool
	}{
		{"pacman", []string{"-Qdtq"}, true},
		{"sudoku", nil, true},
		{"sudo", []string{"-n", "true"}, true},
		{"/usr/bin/sudo", []string{"-A", "true"}, true},
		{"/usr/bin/sudo", []string{"true"}, false},
		{"sudo", []string{"--", "-n"}, false},
		// A value-taking flag swallows the rest of its cluster, and only
		// the first argument counts: here "-n" is a prompt or a user.
		{"sudo", []string{"-pn", "true"}, false},
		{"sudo", []string{"-p", "-n", "true"}, false},
		{"sudo", []string{"-un", "true"}, false},
		{"sudo", []string{"-SA", "true"}, false}, // -S reads stdin, not askpass
		{"sudo", []string{"--non-interactive=x", "true"}, false},
		// Wrappers that leave sudo's rule intact, and ones without sudo.
		{"env", []string{"FOO=1", "sudo", "-n", "true"}, true},
		{"env", []string{"-u", "SUDO_ASKPASS", "sudo", "-A", "true"}, true},
		{"env", nil, true},
		{"env", []string{"FOO=1", "pacman", "-Qq"}, true},
		{"sh", []string{"-c", "sudo -n pacman -Qk"}, true},
		{"bash", []string{"-c", `rm -rf "$HOME/.cache/"*`}, true},
		{"sh", []string{"script.sh"}, true},
		{"sudo-rs", []string{"-n", "true"}, true},
	} {
		if err := CheckSudo(c.name, c.args); (err == nil) != c.ok {
			t.Errorf("CheckSudo(%s %s) = %v, want ok=%v", c.name, strings.Join(c.args, " "), err, c.ok)
		}
	}
}

// TestCheckSudoFixed: the forms the first version of CheckSudo got wrong.
// Every case here fails on it: sudo's long and combined flags were refused,
// and a sudo behind env, a shell or the sudo-rs name ran unchecked.
func TestCheckSudoFixed(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		ok   bool
	}{
		{"sudo", []string{"--non-interactive", "true"}, true},
		{"sudo", []string{"--askpass", "true"}, true},
		{"sudo", []string{"-nA", "true"}, true},
		{"sudo", []string{"-An", "true"}, true},
		{"sudo", []string{"-kn", "true"}, true},
		{"/usr/bin/sudo", []string{"-Au", "root", "true"}, true},
		{"env", []string{"sudo", "pacman", "-Syu"}, false},
		{"env", []string{"FOO=1", "sudo", "pacman", "-Syu"}, false},
		{"/usr/bin/env", []string{"-i", "PATH=/usr/bin", "sudo", "-v"}, false},
		{"env", []string{"-u", "SUDO_ASKPASS", "sudo", "-v"}, false},
		{"env", []string{"--", "sudo", "-v"}, false},
		{"env", []string{"-S", "sudo -v"}, false},
		{"env", []string{"--split-string=FOO=1 sudo -v"}, false},
		{"sudo-rs", []string{"pacman", "-Syu"}, false},
		{"/usr/bin/sudo-rs", []string{"-v"}, false},
		{"sh", []string{"-c", "sudo pacman -Syu"}, false},
		{"bash", []string{"-lc", "yay -Sua && sudo pacman -Syu"}, false},
		{"/bin/sh", []string{"-c", "echo hi;'sudo' -v"}, false},
		{"zsh", []string{"-c", "x=$(sudo -v)"}, false},
		{"sh", []string{"-c", "sudo -n true; sudo -v"}, false},
		{"env", []string{"FOO=1", "sh", "-c", "sudo -v"}, false},
		{"sh", []string{"-c", "env FOO=1 sudo -v"}, false},
	} {
		if err := CheckSudo(c.name, c.args); (err == nil) != c.ok {
			t.Errorf("CheckSudo(%s %q) = %v, want ok=%v", c.name, c.args, err, c.ok)
		}
	}
}

// TestDryRunRefusesAPromptingSudo: a dry run refuses a sudo that could
// prompt, as the real run would, instead of reporting it would run it.
func TestDryRunRefusesAPromptingSudo(t *testing.T) {
	ch := make(chan Msg, 16)
	if c := New(Options{DryRun: true}).Start(Spec{Name: "sudo", Args: []string{"pacman", "-Syu"}}, ch, nil); c != nil {
		t.Fatalf("Start = %v, want nil", c)
	}
	lines, err := drain(t, ch)
	if !errors.Is(err, ErrSudoMayPrompt) || len(lines) != 0 {
		t.Fatalf("dry run = %q, %v; want no would-run line and ErrSudoMayPrompt", lines, err)
	}

	New(Options{DryRun: true}).Start(Spec{Name: "sudo", Args: []string{"-n", "pacman", "-Syu"}}, ch, nil)
	if lines, err := drain(t, ch); err != nil || len(lines) != 1 || lines[0] != DryRunPrefix+"sudo -n pacman -Syu" {
		t.Fatalf("dry run of sudo -n = %q, %v; want the would-run line", lines, err)
	}
}
