package privilege

import (
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// Review tests for the sudo wrapper (#154): the caller's environment, the
// script against a model of sudo 1.9.17's option grammar, and the guard's
// own-file check on the mode.

// TestSudoWrapperHostileEnv: nothing the caller exports changes what the
// wrapper runs. /bin/sh is bash on Arch; started as sh it reads no BASH_ENV
// or ENV, and a hostile IFS, CDPATH or archmaid_* variable (the script's
// own names) is overwritten or unused. Exported functions named after the
// builtins the script uses ([, exec, export, case's helpers) must not run.
func TestSudoWrapperHostileEnv(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	w := wrapperFor(t, sudo, "/askpass")
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	hook := filepath.Join(dir, "hook")
	if err := os.WriteFile(hook, []byte("echo hooked >> '"+marker+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "() { echo hooked >> '" + marker + "'; return 1; }"
	env := append(os.Environ(),
		"BASH_ENV="+hook, "ENV="+hook, "CDPATH="+dir+":/", "IFS= -aAnSu\n",
		"archmaid_ask=", "archmaid_val=1", "archmaid_rest=x", "archmaid_c=n", "archmaid_arg=-n",
		"BASH_FUNC_exec%%="+body, "BASH_FUNC_export%%="+body,
		"BASH_FUNC_shift%%="+body, "BASH_FUNC_break%%="+body, "BASH_FUNC_continue%%="+body,
		"SUDO_ASKPASS=/callers/own",
	)
	for _, args := range [][]string{{"pacman", "-Syu"}, {"-u", "root", "-v"}, {"-nv"}, {}} {
		cmd := exec.Command(w, args...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		want := []string{"ASKPASS=/callers/own"}
		if !slices.Contains([]string{"-nv"}, strings.Join(args, " ")) {
			want = []string{"ASKPASS=/askpass", "-A"}
		}
		want = append(want, args...)
		if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, want) {
			t.Errorf("hostile env, %q: the real sudo got %q, want %q", args, got, want)
		}
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the caller's environment ran code in the wrapper: %q", b)
	}
}

// sudoModel is an independent model of sudo 1.9.17's argument grammar, from
// its man page and parse_args.c (short options "+Aa:BbC:c:D:Eeg:Hh::iKklNnPp:R:r:SsT:t:U:u:Vv",
// getopt_long, exact long names): does sudo with these arguments, and
// without -A added, possibly ask for a password on the terminal? It does
// unless -A, -S or -n is given or the mode is -K, -V or help (-h/--help).
// Arguments the model can't place (an unknown option, abbreviations, a bare
// -h that might take the next word as its host) are reported as unknown.
func sudoModel(args []string) (prompts, known bool) {
	valShort := "aCcDgpRrTtUu"  // required argument
	flagShort := "BbEeHiklNPsv" // no argument
	valLong := map[string]bool{"auth-type": true, "close-from": true, "login-class": true, "chdir": true,
		"group": true, "host": true, "prompt": true, "chroot": true, "role": true, "type": true,
		"command-timeout": true, "other-user": true, "user": true}
	flagLong := map[string]bool{"background": true, "bell": true, "preserve-env": true, "edit": true,
		"set-home": true, "login": true, "reset-timestamp": true, "list": true, "no-update": true,
		"preserve-groups": true, "shell": true, "validate": true}
	quiet := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return !quiet, true
		case strings.HasPrefix(a, "--"):
			name, _, hasEq := strings.Cut(a[2:], "=")
			switch {
			case name == "askpass" || name == "stdin" || name == "non-interactive" ||
				name == "remove-timestamp" || name == "version":
				quiet = true
			case name == "help":
				quiet = true
			case valLong[name]:
				if !hasEq {
					i++
				}
			case flagLong[name]:
			default:
				return false, false
			}
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				c := a[j]
				switch {
				case c == 'A' || c == 'S' || c == 'n' || c == 'K' || c == 'V':
					quiet = true
				case c == 'h':
					if j == len(a)-1 {
						// -h host: the next word is the host unless it
						// starts with '-' or is VAR=value; with none, help.
						if i+1 >= len(args) {
							quiet = true // -h last: help
						} else if n := args[i+1]; !strings.HasPrefix(n, "-") && !strings.Contains(n, "=") {
							i++
						} else {
							return false, false // -h and no host: sudo's own mix
						}
					}
					j = len(a)
				case strings.IndexByte(valShort, c) >= 0:
					if j == len(a)-1 {
						i++
					}
					j = len(a)
				case strings.IndexByte(flagShort, c) >= 0:
				default:
					return false, false
				}
			}
		default:
			return !quiet, true
		}
	}
	return !quiet, true
}

// TestSudoWrapperAgainstModel: random argument lists built from sudo's real
// options; wherever the model knows the grammar, the wrapper adds -A
// exactly when sudo could ask on the terminal. Adding -A where sudo
// wouldn't ask is harmless but is held to equality too: it would mean the
// wrapper changed a call that needed no change.
func TestSudoWrapperAgainstModel(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	w := wrapperFor(t, sudo, "/askpass")
	shorts := []string{"A", "S", "n", "K", "V", "k", "v", "l", "ll", "E", "H", "i", "s", "e", "b", "B", "N", "P",
		"h", "nv", "vn", "kn", "hk", "Ek", "kA", "Sv", "lk", "HEk", "-"}
	valShorts := []string{"a", "C", "c", "D", "g", "p", "R", "r", "T", "t", "U", "u"}
	longs := []string{"askpass", "stdin", "non-interactive", "remove-timestamp", "version", "help",
		"background", "bell", "preserve-env", "edit", "set-home", "login", "reset-timestamp", "list",
		"no-update", "preserve-groups", "shell", "validate", "preserve-env=PATH", "preserve-env=A,B"}
	valLongs := []string{"auth-type", "close-from", "login-class", "chdir", "group", "host", "prompt",
		"chroot", "role", "type", "command-timeout", "other-user", "user"}
	values := []string{"root", "-n", "-A", "-S", "-K", "--stdin", "x y", "3", "-", "", "--", "n"}
	words := []string{"pacman", "-S", "-n", "-A", "--", "VAR=1", "true", "-u", "--askpass", "-Syu"}
	rng := rand.New(rand.NewSource(154))
	pick := func(s []string) string { return s[rng.Intn(len(s))] }
	checked, unknown := 0, 0
	for iter := 0; iter < 2500; iter++ {
		var args []string
		for k := rng.Intn(5); k > 0; k-- {
			switch rng.Intn(6) {
			case 0, 1:
				args = append(args, "-"+pick(shorts))
			case 2:
				if rng.Intn(2) == 0 {
					args = append(args, "-"+pick(valShorts), pick(values))
				} else {
					args = append(args, "-"+pick(valShorts)+pick(values))
				}
			case 3:
				args = append(args, "--"+pick(longs))
			case 4:
				l := pick(valLongs)
				if rng.Intn(2) == 0 {
					args = append(args, "--"+l, pick(values))
				} else {
					args = append(args, "--"+l+"="+pick(values))
				}
			case 5:
				args = append(args, "-E", "-"+pick(valShorts), pick(values))
			}
		}
		for k := rng.Intn(4); k > 0; k-- {
			args = append(args, pick(words))
		}
		prompts, known := sudoModel(args)
		if !known {
			unknown++
			continue
		}
		cmd := exec.Command(w, args...)
		cmd.Env = append(os.Environ(), "SUDO_ASKPASS=/callers/own")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		want := append([]string{"ASKPASS=/callers/own"}, args...)
		if prompts {
			want = append([]string{"ASKPASS=/askpass", "-A"}, args...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("sudo %q: the real sudo got %q, want %q", args, got, want)
		}
		checked++
	}
	if checked < 1500 {
		t.Fatalf("only %d of the cases were inside the model (%d outside)", checked, unknown)
	}
}

// TestSudoWrapperHostArg documents a gap found in review (#154):
// sudo takes the word after a bare `-h` as its host unless it starts with
// '-' or is VAR=value (parse_args.c, "Optional args support -hhostname, not
// -h hostname" then the next word), so `sudo -h myhost -v` is a prompting
// call; the wrapper used to read a bare -h as help and leave -A out.
// The script follows that grammar now (was a known gap).
func TestSudoWrapperHostArg(t *testing.T) {
	sudo := testenv.UseSysbin(t, &sysbin.Sudo, argvSudo)
	w := wrapperFor(t, sudo, "/askpass")
	out, err := exec.Command(w, "-h", "myhost", "-v").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\x00-A\x00") {
		t.Errorf("sudo -h myhost -v ran without -A (got %q)", out)
	}
}

// TestPathShadowsOwnSudoWrapperMode: the same file, with the same contents,
// but a mode other than 0700 (group- or world-accessible, setuid, or no
// execute bit at all) is not the wrapper as written.
func TestPathShadowsOwnSudoWrapperMode(t *testing.T) {
	real := realSudoDir(t)
	s, _ := listen(t, ServerOptions{SudoWrapper: true})
	if s.SudoDir() == "" {
		t.Fatal("no sudo wrapper")
	}
	own := s.sudoFile
	for _, mode := range []os.FileMode{0o755, 0o710, 0o770, 0o701, 0o600} {
		if err := os.Chmod(own, mode); err != nil {
			t.Fatal(err)
		}
		got := paths(PathShadows(list(s.SudoDir(), real)))
		wantShadow := mode&0o111 != 0
		if wantShadow != slices.Equal(got, []string{own}) || (!wantShadow && len(got) != 0) {
			t.Errorf("mode %v: shadows %q, shadow wanted %v", mode, got, wantShadow)
		}
	}
	if err := os.Chmod(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(s.SudoDir(), real))); len(got) != 0 {
		t.Errorf("restored to 0700: shadows %q", got)
	}
}
