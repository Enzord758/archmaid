package privilege

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// The sudo side: the two-stage prime (su validates burn-free, only a correct
// password reaches sudo), the probe and keep-alive, and the argv and
// environment a step's child gets. Root cause of the two stages, measured
// live: a wrong password through `sudo -S` burns TWO pam_faillock failures
// (sudo's retry re-enters PAM on the EOF'd stdin). Nothing here runs the real
// su or sudo: shimAuth puts stand-ins on an exclusive PATH.

// TestMain dispatches --askpass the way archmaid's main() does: under `go
// test` the SUDO_ASKPASS wrapper a Server writes for os.Executable() execs
// this test binary, so the real round trip runs (TestAskpassEndToEnd). The
// tests themselves run under testenv: a scratch home, and traps first on PATH
// for su, sudo, pkexec and the like, so a test that forgets shimAuth fails
// instead of handing a password to the real su.
func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "--askpass" {
		prompt := ""
		if len(os.Args) >= 4 {
			prompt = os.Args[3]
		}
		os.Exit(RunClient(os.Args[2], prompt))
	}
	os.Exit(testenv.Run(m))
}

// shimAuth installs fake `su` and `sudo` on an exclusive PATH. su exits with
// suExit, sudo with sudoExit after printing sudoErr on stderr; each records
// its argv in <name>.called and the line it read on stdin in <name>.stdin
// (sh builtins only: nothing else is on PATH).
func shimAuth(t *testing.T, suExit, sudoExit int, sudoErr string) string {
	t.Helper()
	dir := t.TempDir()
	for name, tail := range map[string]string{
		"su":   "exit " + strconv.Itoa(suExit) + "\n",
		"sudo": "printf '%s' '" + sudoErr + "' >&2\nexit " + strconv.Itoa(sudoExit) + "\n",
	} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + dir + "/" + name + ".called\"\n" +
			"IFS= read -r line; printf '%s' \"$line\" >> \"" + dir + "/" + name + ".stdin\"\n" + tail
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// called returns the argv name's shim ran with, or ok=false if it never ran.
func called(t *testing.T, dir, name string) (argv string, ok bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".called"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func stdinOf(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".stdin"))
	if err != nil {
		t.Fatalf("%s read nothing: %v", name, err)
	}
	return string(b)
}

// requireSuOnly checks su ran as `su -s /usr/bin/true <user>` (no -c: that
// runs the user's shell startup files) with pw on stdin, and sudo never ran.
func requireSuOnly(t *testing.T, dir, pw string) {
	t.Helper()
	argv, ok := called(t, dir, "su")
	if !ok {
		t.Fatal("su was not invoked")
	}
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if want := "-s /usr/bin/true " + self.Username; argv != want {
		t.Fatalf("su argv = %q, want %q", argv, want)
	}
	if got := stdinOf(t, dir, "su"); got != pw {
		t.Fatalf("su read %q on stdin, want %q", got, pw)
	}
	if _, ok := called(t, dir, "sudo"); ok {
		t.Fatal("sudo ran: a password must pass su before sudo sees it")
	}
}

// TestAuthenticateWrongPasswordNeverReachesSudo: su rejects → the error comes
// back and sudo is never invoked (the burn-free property).
func TestAuthenticateWrongPasswordNeverReachesSudo(t *testing.T) {
	dir := shimAuth(t, 1, 0, "")
	if _, err := (Sudo{}).Authenticate(context.Background(), "wrong"); err == nil {
		t.Fatal("su rejection must surface as an error")
	}
	requireSuOnly(t, dir, "wrong")
}

// TestAuthenticateCorrectPasswordMintsTicket: su accepts → `sudo -S -v` runs
// with the password on stdin.
func TestAuthenticateCorrectPasswordMintsTicket(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	if detail, err := (Sudo{}).Authenticate(context.Background(), "right"); err != nil || detail != "" {
		t.Fatalf("Authenticate = %q, %v; want success", detail, err)
	}
	// -S -v mints the ticket; -n -v then checks it can be reused.
	if argv, ok := called(t, dir, "sudo"); !ok || argv != "-S -v\n-n -v" {
		t.Fatalf("sudo argv = %q (ran %v), want -S -v, then -n -v", argv, ok)
	}
	if got := stdinOf(t, dir, "sudo"); got != "right" {
		t.Fatalf("sudo read %q on stdin, want the password", got)
	}
}

// TestAuthenticateRefusesAnUnreusableTicket: the password is right and
// `sudo -S -v` succeeds, but `sudo -n -v` fails (timestamp_timeout=0,
// timestamp_type=ppid ...): ErrNoTicket with the user-facing message, so the
// run does not start and nothing prompts on the terminal later.
func TestAuthenticateRefusesAnUnreusableTicket(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + dir + "/sudo.called\"\n" +
		"case \"$*\" in -n*) exit 1;; esac\nIFS= read -r line\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	detail, err := (Sudo{}).Authenticate(context.Background(), "right")
	if !errors.Is(err, ErrNoTicket) || detail != NoTicketMessage {
		t.Fatalf("Authenticate = %q, %v; want NoTicketMessage and ErrNoTicket", detail, err)
	}
	if argv, _ := called(t, dir, "sudo"); argv != "-S -v\n-n -v" {
		t.Fatalf("sudo argv = %q, want -S -v, then -n -v (never a prompting call)", argv)
	}
}

// TestAuthenticateSudoLockoutSurfaced: if sudo itself fails after su passed,
// its lockout line comes back as the detail.
func TestAuthenticateSudoLockoutSurfaced(t *testing.T) {
	shimAuth(t, 0, 1, "sudo: The account is locked due to 3 failed logins.")
	detail, err := (Sudo{}).Authenticate(context.Background(), "right")
	if err == nil || !strings.Contains(detail, "locked") {
		t.Fatalf("Authenticate = %q, %v; want the lockout notice and an error", detail, err)
	}
}

// TestAuthCheckPasswordLockoutNotice: su's lockout line comes back as the detail.
func TestAuthCheckPasswordLockoutNotice(t *testing.T) {
	dir := t.TempDir()
	su := "#!/bin/sh\nprintf 'su: The account is locked due to 3 failed logins.\\n' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte(su), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	detail, err := CheckPassword(context.Background(), "pw")
	if err == nil || detail != "su: The account is locked due to 3 failed logins." {
		t.Fatalf("CheckPassword = %q, %v; want the lockout line and an error", detail, err)
	}
}

// TestAuthLockoutNoticeExtraction: a pam_faillock lockout line is surfaced; plain
// wrong-password stderr is not.
func TestAuthLockoutNoticeExtraction(t *testing.T) {
	if got := lockoutNotice("su: Authentication failure\n"); got != "" {
		t.Fatalf("plain failure should give no notice, got %q", got)
	}
	stderr := "Password: \nThe account is locked due to 3 failed logins.\n"
	if got := lockoutNotice(stderr); !strings.Contains(got, "locked") {
		t.Fatalf("lockout line not extracted from %q", stderr)
	}
}

// TestSudoProbeAndRefreshNeverPrompt: both run `sudo -n -v`, and Probe reports
// sudo's exit status.
func TestSudoProbeAndRefreshNeverPrompt(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	if !(Sudo{}).Probe(context.Background()) {
		t.Fatal("Probe = false with sudo exiting 0")
	}
	if argv, _ := called(t, dir, "sudo"); argv != "-n -v" {
		t.Fatalf("Probe ran sudo %q, want -n -v", argv)
	}
	os.Remove(filepath.Join(dir, "sudo.called"))
	(Sudo{}).Refresh(context.Background())
	if argv, _ := called(t, dir, "sudo"); argv != "-n -v" {
		t.Fatalf("Refresh ran sudo %q, want -n -v", argv)
	}

	shimAuth(t, 0, 1, "")
	if (Sudo{}).Probe(context.Background()) {
		t.Fatal("Probe = true with sudo exiting 1")
	}
}

// TestSudoCommandFlag: a sudo command runs `sudo -A` only when it needs root
// and askpass is wired, `sudo -n` otherwise; any other command keeps its argv.
// Display is the step's own command line, without the flag.
func TestSudoCommandFlag(t *testing.T) {
	for _, c := range []struct {
		wrapper   string
		argv      []string
		needsPriv bool
		wantArgs  []string
	}{
		{"/w", []string{"sudo", "paccache", "-r"}, true, []string{"-A", "paccache", "-r"}},
		{"", []string{"sudo", "paccache", "-r"}, true, []string{"-n", "paccache", "-r"}},
		{"/w", []string{"sudo", "pacman", "-Qkq"}, false, []string{"-n", "pacman", "-Qkq"}},
		{"/w", []string{"topgrade", "-y", "--no-retry"}, true, []string{"-y", "--no-retry"}},
		{"/w", []string{"yay", "-n"}, false, []string{"-n"}},
	} {
		spec := Sudo{Wrapper: c.wrapper}.Command(c.argv, c.needsPriv)
		if spec.Name != c.argv[0] || strings.Join(spec.Args, " ") != strings.Join(c.wantArgs, " ") {
			t.Errorf("Command(%v, %v) with wrapper %q = %s %v, want %s %v",
				c.argv, c.needsPriv, c.wrapper, spec.Name, spec.Args, c.argv[0], c.wantArgs)
		}
		if want := strings.Join(c.argv, " "); spec.Display != want {
			t.Errorf("Display = %q, want %q", spec.Display, want)
		}
	}
	argv := []string{"sudo", "x"}
	Sudo{Wrapper: "/w"}.Command(argv, true)
	if argv[1] != "x" || len(argv) != 2 {
		t.Fatalf("Command changed its argv: %v", argv)
	}
	if spec := (Sudo{}).Command(nil, true); spec.Name != "" {
		t.Fatalf("Command(nil) = %+v, want nothing to run", spec)
	}
	// A step with no arguments keeps the trailing space it always displayed.
	if got := (Sudo{}).Command([]string{"true"}, false).Display; got != "true " {
		t.Fatalf("Display = %q, want %q", got, "true ")
	}
}

// TestSudoCommandEnvCarriesAskpassOnlyWhenPrivileged: only needsPriv commands get
// SUDO_ASKPASS, pointing at the wrapper; every other one has it stripped, even
// when archmaid inherited one from the user's shell. Without a wrapper no
// command gets one, and archmaid's own environment is never touched.
func TestSudoCommandEnvCarriesAskpassOnlyWhenPrivileged(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	askpassVals := func(env []string) []string {
		var vals []string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "SUDO_ASKPASS="); ok {
				vals = append(vals, v)
			}
		}
		return vals
	}

	wrapper := "/run/user/1000/archmaid-askpass-x/askpass.sh"
	s := Sudo{Wrapper: wrapper}
	if got := askpassVals(s.Command([]string{"topgrade"}, true).Env); len(got) != 1 || got[0] != wrapper {
		t.Fatalf("privileged command SUDO_ASKPASS = %v, want [%s]", got, wrapper)
	}
	if got := askpassVals(s.Command([]string{"yay"}, false).Env); len(got) != 0 {
		t.Fatalf("unprivileged command SUDO_ASKPASS = %v, want none", got)
	}
	if got := askpassVals((Sudo{}).Command([]string{"sudo"}, true).Env); len(got) != 0 {
		t.Fatalf("unwired askpass: SUDO_ASKPASS = %v, want none", got)
	}
	env := s.Env(false)
	if len(env) != len(os.Environ())-1 {
		t.Fatalf("Env dropped more than SUDO_ASKPASS: %d of %d entries", len(env), len(os.Environ()))
	}
	if got := os.Getenv("SUDO_ASKPASS"); got != "/from/the/users/shell" {
		t.Fatalf("SUDO_ASKPASS = %q: archmaid's own environment must be left alone", got)
	}
}

// TestAuthenticateHonoursContext: a cancelled context runs nothing to
// completion and reports an error, never success.
func TestAuthenticateHonoursContext(t *testing.T) {
	shimAuth(t, 0, 0, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Sudo{}).Authenticate(ctx, "right"); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Authenticate on a cancelled context = %v, want context.Canceled", err)
	}
}
