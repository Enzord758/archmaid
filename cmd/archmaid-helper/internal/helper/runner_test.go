package helper

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// These run real processes, but only /bin/sh and /usr/bin/env — never a
// package or system tool.

func TestExecRunnerEnvIsExactlyChildEnv(t *testing.T) {
	t.Setenv("LD_PRELOAD", "/tmp/evil.so")
	t.Setenv("PATH", "/tmp/evil:/usr/bin")
	t.Setenv("PKEXEC_UID", "1000")
	out, _, exit, err := ExecRunner{}.Query([]string{"/usr/bin/env"})
	if err != nil || exit != 0 {
		t.Fatalf("env: exit %d err %v", exit, err)
	}
	got := nonEmptyLines(out)
	slices.Sort(got)
	want := ChildEnv()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("child env = %q, want %q", got, want)
	}
}

func TestExecRunnerCwdIsRoot(t *testing.T) {
	out, _, _, err := ExecRunner{}.Query([]string{"/bin/sh", "-c", "pwd"})
	if err != nil || strings.TrimSpace(out) != "/" {
		t.Errorf("cwd = %q (%v), want /", out, err)
	}
}

func TestExecRunnerStream(t *testing.T) {
	script := `printf 'a\nb\r\nc\rd'; printf 'e1\n' >&2; read x && echo "stdin not empty"; exit 3`
	var mu sync.Mutex
	var got []string
	exit, err := ExecRunner{}.Stream([]string{"/bin/sh", "-c", script}, func(stream, text string) {
		mu.Lock()
		got = append(got, stream+":"+text)
		mu.Unlock()
	})
	if err != nil || exit != 3 {
		t.Fatalf("exit %d err %v, want 3", exit, err)
	}
	var out []string
	for _, g := range got {
		if strings.HasPrefix(g, "out:") {
			out = append(out, g)
		}
	}
	if !slices.Equal(out, []string{"out:a", "out:b", "out:c", "out:d"}) {
		t.Errorf("stdout lines = %q", out)
	}
	if !slices.Contains(got, "err:e1") {
		t.Errorf("stderr not relayed: %q", got)
	}
}

func TestExecRunnerRefusesRelativeCommands(t *testing.T) {
	for _, argv := range [][]string{{"sh", "-c", "true"}, {"./x"}, {}} {
		if _, _, _, err := (ExecRunner{}).Query(argv); err == nil {
			t.Errorf("Query(%q) ran", argv)
		}
		if _, err := (ExecRunner{}).Stream(argv, func(string, string) {}); err == nil {
			t.Errorf("Stream(%q) ran", argv)
		}
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	if _, err := (ExecRunner{}).Stream([]string{"/nonexistent/archmaid-x"}, func(string, string) {}); err == nil {
		t.Error("want an error for a missing binary")
	}
}

// TestAllArgvAreAbsoluteAndUseDashDash pins two invariants over every argv
// builder: the binary is an absolute path from the fixed set, and every
// name list follows "--".
func TestAllArgvAreAbsoluteAndUseDashDash(t *testing.T) {
	fixed := []string{Pacman, Paccache, Journalctl, SystemdTmpfile, Systemctl}
	names := []string{"x", "y"}
	withNames := [][]string{removeArgv(names), repairArgv(names), disableArgv(names), unitShowArgv("x.service")}
	all := append([][]string{checkFilesArgv(), paccacheArgv("3"), journalArgv("1week"), cleanTmpArgv(), upgradeArgv()}, withNames...)
	for _, a := range all {
		if !slices.Contains(fixed, a[0]) {
			t.Errorf("%q: not a fixed absolute binary", a)
		}
	}
	for _, a := range withNames {
		i := slices.Index(a, "--")
		if i < 0 || slices.ContainsFunc(a[i+1:], func(s string) bool { return strings.HasPrefix(s, "-") }) {
			t.Errorf("%q: names must follow --", a)
		}
	}
}
