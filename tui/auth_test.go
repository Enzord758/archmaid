package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two-stage prime (su validates burn-free, only a correct password reaches
// sudo) and the in-flight auth lock. Root cause both fixes address, measured
// live: a wrong password through `sudo -S` burns TWO pam_faillock failures
// (sudo's retry re-enters PAM on the EOF'd stdin), and during PAM's ~2s delay
// the input kept accepting keystrokes / a second enter fired concurrent sudo.

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

// TestAuthWrongPasswordNeverReachesSudo: su rejects → sudoResultMsg carries the
// error and sudo is never invoked (the burn-free property).
func TestAuthWrongPasswordNeverReachesSudo(t *testing.T) {
	dir := shimAuthPair(t, 1)

	msg := authSudo("wrong")().(sudoResultMsg)
	if msg.err == nil {
		t.Fatal("su rejection must surface as an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "su.called")); err != nil {
		t.Fatal("su was not invoked")
	}
	if _, err := os.Stat(filepath.Join(dir, "sudo.called")); err == nil {
		t.Fatal("sudo must NOT be invoked for a wrong password")
	}
}

// TestAuthCorrectPasswordMintsTicket: su accepts → sudo -S -v runs.
func TestAuthCorrectPasswordMintsTicket(t *testing.T) {
	dir := shimAuthPair(t, 0)

	msg := authSudo("right")().(sudoResultMsg)
	if msg.err != nil {
		t.Fatalf("expected success, got %v", msg.err)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "sudo.called"))
	if err != nil {
		t.Fatal("sudo was not invoked after successful validation")
	}
	if got := strings.TrimSpace(string(argv)); got != "-S -v" {
		t.Fatalf("sudo argv = %q, want -S -v", got)
	}
}

// TestLockoutNoticeExtraction: a pam_faillock lockout line is surfaced; plain
// wrong-password stderr is not.
func TestLockoutNoticeExtraction(t *testing.T) {
	if got := lockoutNotice("su: Authentication failure\n"); got != "" {
		t.Fatalf("plain failure should give no notice, got %q", got)
	}
	stderr := "Password: \nThe account is locked due to 3 failed logins.\n"
	if got := lockoutNotice(stderr); !strings.Contains(got, "locked") {
		t.Fatalf("lockout line not extracted from %q", stderr)
	}
}

// ── in-flight auth lock ─────────────────────────────────────────────────────

// primePasswordModel puts the model on the prime-mode password screen.
func primePasswordModel() model {
	m := fixedModel()
	m.screen = screenPassword
	m.password.Focus()
	return m
}

// TestEnterLocksInputAndFiresAuth: submitting sets authInFlight, clears the
// input, and returns the auth cmd.
func TestEnterLocksInputAndFiresAuth(t *testing.T) {
	m := primePasswordModel()
	for _, r := range "hunter2" {
		m = update(m, key(string(r)))
	}
	next, cmd := m.Update(key("enter"))
	got := next.(model)
	if !got.authInFlight {
		t.Fatal("enter must set authInFlight")
	}
	if cmd == nil {
		t.Fatal("enter must return the auth cmd")
	}
	if got.password.Value() != "" {
		t.Fatal("input must be cleared on submit (password not retained)")
	}
}

// TestInFlightIgnoresAllInput: while authenticating, keys/enter/esc do nothing
// — no concurrent auth, no appended keystrokes, no cancel-away.
func TestInFlightIgnoresAllInput(t *testing.T) {
	m := primePasswordModel()
	m.authInFlight = true

	for _, k := range []string{"x", "enter", "esc"} {
		next, cmd := m.Update(key(k))
		got := next.(model)
		if cmd != nil {
			t.Fatalf("key %q while in flight must not fire a cmd", k)
		}
		if got.screen != screenPassword || got.password.Value() != "" {
			t.Fatalf("key %q while in flight mutated state", k)
		}
	}
}

// TestAuthResultUnlocksInput: any sudoResultMsg clears the in-flight lock.
func TestAuthResultUnlocksInput(t *testing.T) {
	m := primePasswordModel()
	m.authInFlight = true

	got := update(m, sudoResultMsg{err: errors.New("nope")})
	if got.authInFlight {
		t.Fatal("failure result must unlock the input")
	}
	if !strings.Contains(got.pwErr, "attempt") {
		t.Fatalf("expected attempts-left error, got %q", got.pwErr)
	}
}

// TestLockoutDetailShownVerbatim: a result carrying a lockout detail replaces
// the generic wrong-password message.
func TestLockoutDetailShownVerbatim(t *testing.T) {
	m := primePasswordModel()
	m.authInFlight = true

	got := update(m, sudoResultMsg{err: errors.New("denied"),
		detail: "The account is locked due to 3 failed logins."})
	if !strings.Contains(got.pwErr, "locked") {
		t.Fatalf("lockout detail not surfaced, pwErr = %q", got.pwErr)
	}
}

// TestInFlightViewShowsAuthenticating: the popup renders the authenticating
// state (and hides the input) while waiting on PAM.
func TestInFlightViewShowsAuthenticating(t *testing.T) {
	m := primePasswordModel()
	m.authInFlight = true
	view := m.viewPassword()
	if !strings.Contains(view, "authenticating") {
		t.Fatalf("in-flight popup missing authenticating indicator:\n%s", view)
	}
	if strings.Contains(view, "enter submit") {
		t.Fatal("in-flight popup should not invite further input")
	}
}
