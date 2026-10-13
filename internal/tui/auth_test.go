package tui

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
)

// The two-stage prime (su validates burn-free, only a correct password reaches
// sudo) and the in-flight auth lock. Root cause both fixes address, measured
// live: a wrong password through `sudo -S` burns TWO pam_faillock failures
// (sudo's retry re-enters PAM on the EOF'd stdin), and during PAM's ~2s delay
// the input kept accepting keystrokes / a second enter fired concurrent sudo.
// The check is privilege.Sudo.Authenticate, which the Driver runs for the
// engine's CheckPassword with the Priv main gives it (privileged()); its own
// tests (su argv, stdin, lockout notices) live in internal/privilege.

// shimAuthPair installs fake `su` and `sudo` on an exclusive PATH. su exits
// with suExit; both record their invocation (argv) into markers in dir.
func shimAuthPair(t *testing.T, suExit int) string {
	t.Helper()
	dir := t.TempDir()
	su := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + dir + "/su.called\"\nexit " +
		string(rune('0'+suExit)) + "\n"
	sudo := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + dir + "/sudo.called\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte(su), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(sudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

// requireSuOnly checks the su shim ran as `su -s /usr/bin/true <user>` (no -c:
// that runs the user's shell startup files) and that sudo never ran.
func requireSuOnly(t *testing.T, dir string) {
	t.Helper()
	argv, err := os.ReadFile(filepath.Join(dir, "su.called"))
	if err != nil {
		t.Fatal("su was not invoked")
	}
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(argv)), "-s /usr/bin/true "+self.Username; got != want {
		t.Fatalf("su argv = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "sudo.called")); err == nil {
		t.Fatal("sudo ran: a password must pass su before sudo sees it")
	}
}

// TestAuthWrongPasswordNeverReachesSudo: su rejects → an error, and sudo is
// never invoked (the burn-free property).
func TestAuthWrongPasswordNeverReachesSudo(t *testing.T) {
	dir := shimAuthPair(t, 1)

	if _, err := privileged().Authenticate(context.Background(), "wrong"); err == nil {
		t.Fatal("su rejection must surface as an error")
	}
	// su runs true in place of the user's shell (no -c: that still ran the
	// shell's startup files), and sudo never runs.
	requireSuOnly(t, dir)
}

// TestAuthCorrectPasswordMintsTicket: su accepts → sudo -S -v runs.
func TestAuthCorrectPasswordMintsTicket(t *testing.T) {
	dir := shimAuthPair(t, 0)

	if _, err := privileged().Authenticate(context.Background(), "right"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "sudo.called"))
	if err != nil {
		t.Fatal("sudo was not invoked after successful validation")
	}
	if got := strings.TrimSpace(string(argv)); got != "-n -v" { // the last call: the ticket check after -S -v
		t.Fatalf("sudo argv = %q, want -n -v", got)
	}
}

// ── in-flight auth lock ─────────────────────────────────────────────────────

// upFrontPasswordModel puts the model on the engine's up-front password
// prompt.
func upFrontPasswordModel() (model, *fakeDriver) {
	m, f := engineModel()
	return feed(m, engine.NeedsPassword{ID: 1, Kind: engine.PasswordUpFront, AttemptsLeft: 3}), f
}

// TestEnterLocksInputAndSends: submitting sets authInFlight, clears the
// input, and sends the password.
func TestEnterLocksInputAndSends(t *testing.T) {
	m, f := upFrontPasswordModel()
	for _, r := range "hunter2" {
		m = update(m, key(string(r)))
	}
	m = update(m, key("enter"))
	if !m.authInFlight {
		t.Fatal("enter must set authInFlight")
	}
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.SubmitPassword{Prompt: 1, Password: "hunter2"}) {
		t.Fatalf("sent %#v", got)
	}
	if m.password.Value() != "" {
		t.Fatal("input must be cleared on submit (password not retained)")
	}
}

// TestInFlightIgnoresAllInput: while authenticating, keys/enter/esc do nothing
// — nothing sent, no appended keystrokes, no cancel-away.
func TestInFlightIgnoresAllInput(t *testing.T) {
	m, f := upFrontPasswordModel()
	m = update(m, key("enter"))
	n := len(f.sent)

	for _, k := range []string{"x", "enter", "esc"} {
		next, cmd := m.Update(key(k))
		got := next.(model)
		if cmd != nil || len(f.sent) != n {
			t.Fatalf("key %q while in flight fired a cmd or a Command", k)
		}
		if got.screen != screenPassword || got.password.Value() != "" {
			t.Fatalf("key %q while in flight mutated state", k)
		}
	}
}

// TestAuthResultUnlocksInput: the engine's re-ask after a wrong password
// unlocks the input and shows why; a lockout notice is shown verbatim.
func TestAuthResultUnlocksInput(t *testing.T) {
	for _, why := range []string{"Incorrect password — 2 attempts left", "The account is locked due to 3 failed logins."} {
		m, _ := upFrontPasswordModel()
		m = update(m, key("enter"))
		m = feed(m, engine.AuthResolved{AttemptsLeft: 2},
			engine.NeedsPassword{ID: 2, Kind: engine.PasswordUpFront, AttemptsLeft: 2, Error: why})
		if m.authInFlight {
			t.Fatal("the re-ask must unlock the input")
		}
		if m.pwErr != why {
			t.Fatalf("pwErr = %q, want %q", m.pwErr, why)
		}
	}
}

// TestInFlightViewShowsAuthenticating: the popup renders the authenticating
// state (and hides the input) while waiting on PAM.
func TestInFlightViewShowsAuthenticating(t *testing.T) {
	m, _ := upFrontPasswordModel()
	m = update(m, key("enter"))
	view := m.viewPassword()
	if !strings.Contains(view, "authenticating") {
		t.Fatalf("in-flight popup missing authenticating indicator:\n%s", view)
	}
	if strings.Contains(view, "enter submit") {
		t.Fatal("in-flight popup should not invite further input")
	}
}
