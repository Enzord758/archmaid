package privilege

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The TUI's sudo wrapper (sudowrap.go), run for real against a stand-in
// for the real sudo at an absolute path, which prints what it got.

// argvSudo is a stand-in real sudo: it prints SUDO_ASKPASS, then each
// argument, NUL-terminated.
const argvSudo = `printf '%s\0' "ASKPASS=${SUDO_ASKPASS-unset}" "$@"`

// wrapperFor writes sudoWrapperScript(sudo, askpass) into a fresh dir as
// `sudo` and returns its path.
func wrapperFor(t *testing.T, sudo, askpass string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sudo")
	if err := os.WriteFile(path, []byte(sudoWrapperScript(sudo, askpass)), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSudoWrapperArgs: the wrapper adds -A (and SUDO_ASKPASS, archmaid's)
// exactly when the caller's sudo options don't already say how sudo gets
// its password (-A, -S, -n, also in a cluster) and the mode can ask for
// one (not -K, -V, -h); an option's value is never read as a flag, and
// sudo's options end at the command, at -- and at an option it doesn't
// know. Whatever it adds, the caller's arguments reach sudo exactly,
// spaces, quotes, $, backslashes, newlines and empty ones included.
func TestSudoWrapperArgs(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	const askpass = "/run/user/1000/archmaid-askpass-1/it's askpass.sh"
	w := wrapperFor(t, sudo, askpass)
	for _, c := range []struct {
		args []string
		ask  bool
	}{
		{nil, true},
		{[]string{"pacman", "-Syu"}, true},
		{[]string{"-v"}, true},
		{[]string{"-k"}, true},
		{[]string{"-k", "pacman", "-U", "x.pkg.tar.zst"}, true}, // makepkg's
		{[]string{"-l"}, true},
		{[]string{"-E", "pacman", "-S", "foo"}, true}, // -S is pacman's
		{[]string{"pacman", "-S", "-n", "foo"}, true},
		{[]string{"--", "-n"}, true},
		{[]string{"-u", "-n", "pacman"}, true}, // the user "-n"
		{[]string{"-un", "pacman"}, true},      // the user "n"
		{[]string{"-p", "-n", "pacman"}, true}, // the prompt "-n"
		{[]string{"--prompt", "-S", "pacman"}, true},
		{[]string{"--prompt=-n", "pacman"}, true},
		{[]string{"--user", "-A", "true"}, true},
		{[]string{"-hhost", "pacman"}, true}, // -h with a value is --host
		{[]string{"--preserve-env=PATH", "pacman"}, true},
		{[]string{"--preserve-env", "pacman"}, true},
		{[]string{"--bogus", "-n"}, true}, // unknown: sudo's to refuse; -A can't hurt
		{[]string{"-Z", "-n"}, true},
		{[]string{"-"}, true},
		{[]string{"-A", "pacman"}, false},
		{[]string{"--askpass", "pacman"}, false},
		{[]string{"-S", "-v"}, false},
		{[]string{"--stdin", "-v"}, false},
		{[]string{"-n", "-v"}, false},
		{[]string{"--non-interactive", "true"}, false},
		{[]string{"-nv"}, false},
		{[]string{"-vn"}, false},
		{[]string{"-kA", "true"}, false},
		{[]string{"-Sv"}, false},
		{[]string{"-C", "3", "-n", "true"}, false}, // 3 is -C's value, -n a flag
		{[]string{"-C3", "-n", "true"}, false},
		{[]string{"--user", "root", "-n", "true"}, false},
		{[]string{"--user=root", "-S", "true"}, false},
		{[]string{"-E", "-u", "root", "-A", "true"}, false},
		{[]string{"-K"}, false},
		{[]string{"--remove-timestamp"}, false},
		{[]string{"-V"}, false},
		{[]string{"--version"}, false},
		{[]string{"-h"}, false},
		{[]string{"--help"}, false},
		{[]string{"sh", "-c", `echo "$HOME" 'it''s' \$(id) a\b`, "", "two  spaces", "line\nbreak", "-n", "--", "*"}, true},
	} {
		cmd := exec.Command(w, c.args...)
		cmd.Env = append(os.Environ(), "SUDO_ASKPASS=/callers/own")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		want := append([]string{"ASKPASS=/callers/own"}, c.args...)
		if c.ask {
			want = append([]string{"ASKPASS=" + askpass, "-A"}, c.args...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("sudo %q: the real sudo got %q, want %q", c.args, got, want)
		}
	}
}

// TestSudoWrapperExecsTheRealSudo: a program that runs `sudo` by name with
// the wrapper's dir first on PATH runs the real sudo (sysbin.Sudo's
// absolute path, as written) as its own child: the wrapper execs it, so
// no shell sits between (Request.Verify's chain is unchanged), and a
// decoy sudo later on PATH never runs.
func TestSudoWrapperExecsTheRealSudo(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, `printf '%s %s\n' "$PPID" "$*"`)
	w := wrapperFor(t, sudo, "/askpass")
	decoyLog := filepath.Join(t.TempDir(), "decoy.log")
	decoy := t.TempDir()
	if err := os.WriteFile(filepath.Join(decoy, "sudo"), []byte("#!/bin/sh\necho decoy >> "+shellQuote(decoyLog)+"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	cmd := exec.Command("/bin/sh", "-c", `echo "$$"; sudo pacman -Syu; true`)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(w)+sep+decoy+sep+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		t.Fatalf("output %q", out)
	}
	caller, _ := strconv.Atoi(lines[0])
	ppid, rest, _ := strings.Cut(lines[1], " ")
	if ppid != strconv.Itoa(caller) || rest != "-A pacman -Syu" {
		t.Fatalf("the real sudo ran as %q with parent %s; want -A pacman -Syu, a child of the caller (pid %d)", rest, ppid, caller)
	}
	if b, err := os.ReadFile(decoyLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the decoy sudo later on PATH ran: %q (err %v)", b, err)
	}
	if !bytes.Contains([]byte(sudoWrapperScript(sudo, "/askpass")), []byte("exec "+shellQuote(sudo)+" -A \"$@\"")) {
		t.Fatal("the wrapper doesn't exec the absolute sudo")
	}
}

// TestListenSudoWrapper: with SudoWrapper the Server writes the wrapper as
// `sudo` in a 0700 dir of its own inside its private dir, a 0700 file
// that execs sysbin.Sudo as it was then (not as it is later) with the
// Server's askpass; Close removes it and forgets it. Without the option,
// or with a bare sysbin.Sudo (only under testenv), there is none.
func TestListenSudoWrapper(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	s, _ := listen(t, ServerOptions{SudoWrapper: true})
	dir := s.SudoDir()
	if dir == "" || filepath.Dir(dir) != s.dir || s.sudoFile != filepath.Join(dir, "sudo") {
		t.Fatalf("SudoDir = %q, file %q; want <private dir>/bin/sudo", dir, s.sudoFile)
	}
	for path, want := range map[string]os.FileMode{dir: os.ModeDir | 0o700, s.sudoFile: 0o700} {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode() != want {
			t.Fatalf("%s: mode %v (err %v), want %v", path, fi.Mode(), err, want)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the wrapper's dir holds %d entries, want only sudo", len(entries))
	}
	sysbin.Sudo = "/elsewhere/sudo" // fixed at write time: a later change doesn't reach it
	out, err := exec.Command(s.sudoFile, "true").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "ASKPASS="+s.Wrapper()+"\x00-A\x00true\x00" {
		t.Fatalf("the wrapper ran %q, want the absolute sudo with -A and the Server's askpass", got)
	}
	if fi, _ := os.Lstat(s.sudoFile); !ownSudo.is(s.sudoFile, fi) {
		t.Fatal("the Server's wrapper isn't recorded as its own")
	}
	s.Close()
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the wrapper's dir is still there after Close (err %v)", err)
	}
	ownSudo.mu.Lock()
	n := len(ownSudo.m)
	ownSudo.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d wrappers still recorded after Close", n)
	}

	plain, _ := listen(t, ServerOptions{})
	if plain.SudoDir() != "" {
		t.Fatalf("a Server without SudoWrapper has a sudo dir %q", plain.SudoDir())
	}
	sysbin.Sudo = "sudo"
	bare, _ := listen(t, ServerOptions{SudoWrapper: true})
	if bare.SudoDir() != "" {
		t.Fatalf("a bare sysbin.Sudo got a wrapper in %q: it would find itself on PATH", bare.SudoDir())
	}
	if _, err := os.Lstat(filepath.Join(bare.dir, sudoWrapperDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a bare sysbin.Sudo left a wrapper dir (err %v)", err)
	}
}

// TestWriteSudoWrapperExisting: a wrapper dir that is already there (it
// can't be, inside the Server's fresh private dir) is an error, with
// nothing written into it and nothing recorded as archmaid's own.
func TestWriteSudoWrapperExisting(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Sudo, "exit 0")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, sudoWrapperDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if bin, path, err := writeSudoWrapper(dir, "/askpass"); err == nil || bin != "" || path != "" {
		t.Fatalf("writeSudoWrapper = %q, %q, %v; want an error", bin, path, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, sudoWrapperDir)); len(entries) != 0 {
		t.Fatalf("wrote %d entries into the dir that was there", len(entries))
	}
	ownSudo.mu.Lock()
	n := len(ownSudo.m)
	ownSudo.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d wrappers recorded", n)
	}
}

// TestSudoEnvSudoDir: a step that gets the askpass gets the sudo wrapper's
// dir first on its PATH (every PATH entry; without one, before execvp's
// default); a step that doesn't, a Sudo without the askpass or without the
// dir, keeps archmaid's PATH as it is.
func TestSudoEnvSudoDir(t *testing.T) {
	t.Setenv("PATH", "/a:/b")
	pathOf := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PATH="); ok {
				out = append(out, v)
			}
		}
		return out
	}
	both := Sudo{Wrapper: "/w/askpass.sh", SudoDir: "/w/bin"}
	for _, c := range []struct {
		s         Sudo
		needsPriv bool
		want      []string
	}{
		{both, true, []string{"/w/bin:/a:/b"}},
		{both, false, []string{"/a:/b"}},
		{Sudo{Wrapper: "/w/askpass.sh"}, true, []string{"/a:/b"}},
		{Sudo{SudoDir: "/w/bin"}, true, []string{"/a:/b"}},
	} {
		if got := pathOf(c.s.Env(c.needsPriv)); !slices.Equal(got, c.want) {
			t.Errorf("%+v.Env(%v): PATH %q, want %q", c.s, c.needsPriv, got, c.want)
		}
	}
	if got := ChildPATH(both.Command([]string{"topgrade", "-y"}, true).Env, nil); got != "/w/bin:/a:/b" {
		t.Errorf("topgrade's PATH = %q", got)
	}
	os.Unsetenv("PATH")
	if got := pathOf(both.Env(true)); !slices.Equal(got, []string{"/w/bin:/bin:/usr/bin"}) {
		t.Errorf("without a PATH: %q", got)
	}
}

// TestAuthenticateNoTicketWithWrapper: the password is right but sudo
// keeps no ticket (`sudo -n -v` fails after `sudo -S -v`). With the sudo
// wrapper wired the run goes on: no error, NoTicketNote as the detail.
// Without it (no askpass, or no sudo dir) it is still ErrNoTicket.
func TestAuthenticateNoTicketWithWrapper(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + dir + "/sudo.called\"\n" +
		"case \"$*\" in -n*) exit 1;; esac\nIFS= read -r line\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	detail, err := Sudo{Wrapper: "/w/askpass.sh", SudoDir: "/w/bin"}.Authenticate(t.Context(), "right")
	if err != nil || detail != NoTicketNote {
		t.Fatalf("Authenticate = %q, %v; want NoTicketNote and no error", detail, err)
	}
	for _, s := range []Sudo{{}, {Wrapper: "/w/askpass.sh"}, {SudoDir: "/w/bin"}} {
		if detail, err := s.Authenticate(t.Context(), "right"); !errors.Is(err, ErrNoTicket) || detail != NoTicketMessage {
			t.Errorf("%+v: Authenticate = %q, %v; want ErrNoTicket", s, detail, err)
		}
	}
	if argv, _ := called(t, dir, "sudo"); argv != strings.Repeat("-S -v\n-n -v\n", 3)+"-S -v\n-n -v" {
		t.Fatalf("sudo argv = %q: only -S -v and -n -v, never a prompting call", argv)
	}
}

// TestPathShadowsOwnSudoWrapper: the TUI's sudo wrapper first on a step's
// PATH is sudo's boundary (it execs the real one), so a sudo after it is
// no shadow; it doesn't bound the other names. Only that file, at that
// path, as written counts: one replaced (a new file, the same text), one
// rewritten in place, a copy elsewhere, a symlink to it, or one whose
// Server has closed is a shadow like any other.
func TestPathShadowsOwnSudoWrapper(t *testing.T) {
	real := realSudoDir(t)
	later := put(t, 0o755, "sudo", "su")
	fresh := func() (*Server, string) {
		t.Helper()
		s, _ := listen(t, ServerOptions{SudoWrapper: true})
		if s.SudoDir() == "" {
			t.Fatal("no sudo wrapper")
		}
		return s, s.sudoFile
	}

	s, own := fresh()
	if got := paths(PathShadows(list(s.SudoDir(), later, real))); !slices.Equal(got, []string{filepath.Join(later, "su")}) {
		t.Fatalf("own wrapper first: shadows %q, want only the su after it", got)
	}

	body, _ := os.ReadFile(own)
	copyDir := put(t, 0o700)
	if err := os.WriteFile(filepath.Join(copyDir, "sudo"), body, 0o700); err != nil {
		t.Fatal(err)
	}
	link := t.TempDir()
	if err := os.Symlink(own, filepath.Join(link, "sudo")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{copyDir, link} {
		if got := paths(PathShadows(list(dir, real))); !slices.Equal(got, []string{filepath.Join(dir, "sudo")}) {
			t.Errorf("%s: shadows %q, want its sudo", dir, got)
		}
	}

	// Replaced: removed and written again with the same text.
	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(s.SudoDir(), real))); !slices.Equal(got, []string{own}) {
		t.Errorf("replaced: shadows %q, want %s", got, own)
	}

	// Rewritten in place: the same inode, other text.
	s2, own2 := fresh()
	if err := os.WriteFile(own2, append(body, '#'), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(s2.SudoDir(), real))); !slices.Equal(got, []string{own2}) {
		t.Errorf("rewritten: shadows %q, want %s", got, own2)
	}

	// Its Server closed (the dir gone, then a file put back in its place).
	s3, own3 := fresh()
	s3.Close()
	if err := os.MkdirAll(filepath.Dir(own3), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own3, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(filepath.Dir(own3), real))); !slices.Equal(got, []string{own3}) {
		t.Errorf("after Close: shadows %q, want %s", got, own3)
	}
}
