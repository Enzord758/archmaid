package privilege

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// recorder is a sysbin shim body that appends "<name> <argv>" to log, reads
// one line of stdin and exits code (sh builtins only: PATH is the decoys').
func recorder(log, name, code string) string {
	return "printf '%s\\n' \"" + name + " $*\" >> '" + log + "'\nIFS= read -r line\nexit " + code
}

// absSuSudo points sysbin.Su and sysbin.Sudo at recording shims outside PATH,
// with decoys of every privileged name first on PATH. It returns the shims'
// log and the decoys'.
func absSuSudo(t *testing.T, suExit, sudoExit string) (calls, decoys string) {
	t.Helper()
	calls = filepath.Join(t.TempDir(), "calls")
	testenv.UseSysbin(t, &sysbin.Su, recorder(calls, "su", suExit))
	testenv.UseSysbin(t, &sysbin.Sudo, recorder(calls, "sudo", sudoExit))
	decoys = testenv.Decoys(t, "su", "sudo", "pkexec", "pkttyagent", "topgrade")
	return calls, decoys
}

func readLog(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// TestSudoTicketRunsSysbin: the probe, the two-stage authentication, the
// keep-alive and the ticket drop run su and sudo from internal/sysbin (in
// the product /usr/bin/su and /usr/bin/sudo), never a su or sudo found on
// PATH: one there would be handed the password.
func TestSudoTicketRunsSysbin(t *testing.T) {
	calls, decoys := absSuSudo(t, "0", "0")
	ctx := context.Background()
	if !(Sudo{}).Probe(ctx) {
		t.Error("Probe: the sysbin sudo's exit 0 is a cached ticket")
	}
	if detail, err := (Sudo{}).Authenticate(ctx, "right"); err != nil {
		t.Errorf("Authenticate = %q, %v", detail, err)
	}
	(Sudo{}).Refresh(ctx)
	if err := DropTicket(ctx); err != nil {
		t.Errorf("DropTicket: %v", err)
	}
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	want := "sudo -n -v\n" +
		"su -s /usr/bin/true " + self.Username + "\n" + "sudo -S -v\n" +
		"sudo -n -v\n" +
		"sudo -n -k\n"
	if got := readLog(t, calls); got != want {
		t.Errorf("sysbin calls:\n%s\nwant:\n%s", got, want)
	}
	if got := readLog(t, decoys); got != "" {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", got)
	}
}

// TestCheckPasswordRunsSysbinSu: both su checks (the string and the bytes
// one, which the kept password uses) run sysbin.Su.
func TestCheckPasswordRunsSysbinSu(t *testing.T) {
	calls, decoys := absSuSudo(t, "0", "0")
	ctx := context.Background()
	if _, err := CheckPassword(ctx, "right"); err != nil {
		t.Errorf("CheckPassword: %v", err)
	}
	if _, err := CheckPasswordBytes(ctx, []byte("right")); err != nil {
		t.Errorf("CheckPasswordBytes: %v", err)
	}
	if got := readLog(t, calls); strings.Count(got, "su -s /usr/bin/true ") != 2 || strings.Contains(got, "sudo") {
		t.Errorf("sysbin calls:\n%s\nwant two su checks", got)
	}
	if got := readLog(t, decoys); got != "" {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", got)
	}
}

// TestCommandRunsSysbin: a step's sudo and topgrade run from internal/sysbin;
// the step still shows the names it gave (Display), and any other program
// keeps its name.
func TestCommandRunsSysbin(t *testing.T) {
	dir := t.TempDir()
	for p, name := range map[*string]string{&sysbin.Sudo: "sudo", &sysbin.Topgrade: "topgrade"} {
		old := *p
		*p = filepath.Join(dir, "abs", name)
		t.Cleanup(func() { *p = old })
	}
	for _, c := range []struct {
		argv          []string
		priv          bool
		name, display string
		args          []string
	}{
		{[]string{"sudo", "paccache", "-r"}, true, sysbin.Sudo, "sudo paccache -r", []string{"-A", "paccache", "-r"}},
		{[]string{"sudo", "pacman", "-Qkq"}, false, sysbin.Sudo, "sudo pacman -Qkq", []string{"-n", "pacman", "-Qkq"}},
		{[]string{"topgrade", "-y", "--no-retry", "--disable", "hyprpm"}, true, sysbin.Topgrade, "topgrade -y --no-retry --disable hyprpm", []string{"-y", "--no-retry", "--disable", "hyprpm"}},
		{[]string{"yay", "-Sc", "--aur"}, false, "yay", "yay -Sc --aur", []string{"-Sc", "--aur"}},
	} {
		spec := Sudo{Wrapper: "/run/user/1000/w"}.Command(c.argv, c.priv)
		if spec.Name != c.name || spec.Display != c.display || strings.Join(spec.Args, " ") != strings.Join(c.args, " ") {
			t.Errorf("Command(%q) = %s %q (display %q), want %s %q (display %q)",
				c.argv, spec.Name, spec.Args, spec.Display, c.name, c.args, c.display)
		}
	}
}
