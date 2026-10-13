package privilege

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// realSudoDir points sysbin.Sudo at a stand-in "real" sudo (never run here)
// in a dir of its own and returns that dir.
func realSudoDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(testenv.UseSysbin(t, &sysbin.Sudo, "exit 0"))
}

// put writes an executable (mode) named name into a fresh dir and returns
// the dir.
func put(t *testing.T, mode os.FileMode, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func paths(ss []Shadow) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.String())
	}
	return out
}

func list(dirs ...string) string { return strings.Join(dirs, string(os.PathListSeparator)) }

// TestPathShadows: a name counts before the system's own file of that name
// (the stand-in real dir holds only sudo, so every other name counts
// wherever it is), as an executable file or as any symlink (to
// /usr/bin/sudo itself too), and so does a relative entry. A file without
// an execute bit, a directory, and a sudo after the real sudo don't.
func TestPathShadows(t *testing.T) {
	real := realSudoDir(t)
	link := t.TempDir()
	if err := os.Symlink(sysbin.Sudo, filepath.Join(link, "sudo")); err != nil {
		t.Fatal(err)
	}
	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(dangling, "nowhere"), filepath.Join(dangling, "su")); err != nil {
		t.Fatal(err)
	}
	dirNamedSudo := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirNamedSudo, "sudo"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory that is the real one under another name (as /bin and
	// /usr/sbin are /usr/bin on Arch): the scan stops there too.
	alias := filepath.Join(t.TempDir(), "bin")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	exec := put(t, 0o700, "sudo")
	others := put(t, 0o755, "doas", "run0", "please", "sudo-rs", "pkexec", "su", "yay")
	plain := put(t, 0o644, "sudo", "su")
	missing := filepath.Join(t.TempDir(), "gone")

	for _, c := range []struct {
		name string
		path string
		want []string
	}{
		{"nothing before", list(real, exec), nil},
		{"an executable sudo before", list(missing, exec, real), []string{filepath.Join(exec, "sudo")}},
		// After the real sudo's dir only a sudo is out of reach: the stand-in
		// real dir holds no other name, so each one counts wherever it is (as
		// doas, please and sudo-rs do on Arch, where /usr/bin lacks them).
		{"after the real one", list(real, exec, others, link), []string{
			filepath.Join(others, "sudo-rs"), filepath.Join(others, "doas"), filepath.Join(others, "run0"),
			filepath.Join(others, "please"), filepath.Join(others, "su"), filepath.Join(others, "pkexec")}},
		{"every other name", list(others, real), []string{
			filepath.Join(others, "sudo-rs"), filepath.Join(others, "doas"), filepath.Join(others, "run0"),
			filepath.Join(others, "please"), filepath.Join(others, "su"), filepath.Join(others, "pkexec")}},
		{"not executable", list(plain, real), nil},
		{"a directory named sudo", list(dirNamedSudo, real), nil},
		{"a symlink to the real sudo", list(link, real), []string{filepath.Join(link, "sudo")}},
		{"a dangling symlink", list(dangling, real), []string{filepath.Join(dangling, "su")}},
		{"the real dir by another name", list(alias, exec), nil},
		{"relative entries", list(".", "", "bin", real), []string{
			`the relative PATH entry "."`, "an empty PATH entry (the current folder)", `the relative PATH entry "bin"`}},
		{"no real sudo on PATH: everything", list(exec, others), []string{
			filepath.Join(exec, "sudo"), filepath.Join(others, "sudo-rs"), filepath.Join(others, "doas"), filepath.Join(others, "run0"),
			filepath.Join(others, "please"), filepath.Join(others, "su"), filepath.Join(others, "pkexec")}},
	} {
		if got := paths(PathShadows(c.path)); !slices.Equal(got, c.want) {
			t.Errorf("%s: PathShadows(%q) = %q, want %q", c.name, c.path, got, c.want)
		}
	}
}

// TestPathShadowsNoRealSudo: without the real sudo (not installed), every
// entry is searched.
func TestPathShadowsNoRealSudo(t *testing.T) {
	old := sysbin.Sudo
	sysbin.Sudo = filepath.Join(t.TempDir(), "no", "sudo")
	t.Cleanup(func() { sysbin.Sudo = old })
	exec := put(t, 0o755, "sudo")
	if got := paths(PathShadows(exec)); !slices.Equal(got, []string{filepath.Join(exec, "sudo")}) {
		t.Errorf("PathShadows = %q", got)
	}
}

// TestPathShadowsBareSudo: with a bare sysbin.Sudo (only under testenv:
// PATH decides every program) there is nothing to compare against.
func TestPathShadowsBareSudo(t *testing.T) {
	if sysbin.Sudo != "sudo" {
		t.Fatalf("sysbin.Sudo = %q under testenv", sysbin.Sudo)
	}
	if got := PathShadows(list(put(t, 0o755, "sudo", "su"), "/usr/bin")); got != nil {
		t.Errorf("PathShadows = %q", paths(got))
	}
}

// TestChildPATH: the child's PATH is the last one in its environment, then
// its extra variables; archmaid's own for a nil Env; execvp's default
// without any.
func TestChildPATH(t *testing.T) {
	t.Setenv("PATH", "/own/bin:/usr/bin")
	for _, c := range []struct {
		env, extra []string
		want       string
	}{
		{nil, nil, "/own/bin:/usr/bin"},
		{[]string{"A=1", "PATH=/a", "PATH=/b"}, nil, "/b"},
		{[]string{"PATH=/a"}, []string{"PATH=/x:/usr/bin"}, "/x:/usr/bin"},
		{nil, []string{"PATH="}, ""},
		{[]string{"HOME=/h"}, []string{"FOO=1"}, "/bin:/usr/bin"},
	} {
		if got := ChildPATH(c.env, c.extra); got != c.want {
			t.Errorf("ChildPATH(%q, %q) = %q, want %q", c.env, c.extra, got, c.want)
		}
	}
	// An empty PATH= is one empty entry: the current folder, relative. With
	// a real sudo to compare against (a bare sysbin.Sudo skips the scan),
	// the guard sees it.
	realSudoDir(t)
	if got := paths(PathShadows(ChildPATH(nil, []string{"PATH="}))); !slices.Equal(got, []string{"an empty PATH entry (the current folder)"}) {
		t.Errorf("PathShadows(ChildPATH(PATH=)) = %q, want the empty entry", got)
	}
}

// TestKeptSpent: Spent is true for the zero value and nil, false for a new
// one, and true after Fail or Wipe.
func TestKeptSpent(t *testing.T) {
	var nilK *KeptPassword
	if !nilK.Spent() || !(&KeptPassword{}).Spent() {
		t.Error("nil or zero KeptPassword not spent")
	}
	k := NewKeptPassword(1 << 40)
	if k.Spent() {
		t.Error("a new KeptPassword is spent")
	}
	k.Fail()
	if !k.Spent() || k.Keep([]byte("pw")) {
		t.Error("Fail: not spent, or it kept")
	}
	w := NewKeptPassword(1 << 40)
	w.Wipe()
	if !w.Spent() {
		t.Error("Wipe: not spent")
	}
}
