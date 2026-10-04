package privilege

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestAuthenticateRefusesALineBreak: Authenticate refuses a password with a
// line break itself, before su or sudo runs, whatever the frontend checked:
// each reads one line, so it would check something other than what was typed.
func TestAuthenticateRefusesALineBreak(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	for _, pw := range []string{"good\nbad", "good\rbad", "good\r\n"} {
		if _, err := (Sudo{}).Authenticate(context.Background(), pw); !errors.Is(err, ErrLineBreak) {
			t.Fatalf("Authenticate(%q) = %v, want ErrLineBreak", pw, err)
		}
		if _, err := CheckPassword(context.Background(), pw); !errors.Is(err, ErrLineBreak) {
			t.Fatalf("CheckPassword(%q) = %v, want ErrLineBreak", pw, err)
		}
	}
	for _, name := range []string{"su", "sudo"} {
		if _, ok := called(t, dir, name); ok {
			t.Fatalf("%s ran for a password with a line break", name)
		}
	}
}

// TestLockoutNoticeIsSanitized: the notice is shown verbatim, so whatever su
// printed on that line is made safe first: no escapes or controls, and
// clipped (by runes, never mid-character).
func TestLockoutNoticeIsSanitized(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("ø", 300)
	su := "#!/bin/sh\nprintf 'su: \\033]52;c;ZXZpbA==\\007account \\033[2Jlocked\\t" + long + "\\n' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte(su), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	detail, err := CheckPassword(context.Background(), "pw")
	if err == nil {
		t.Fatal("want su's failure")
	}
	if strings.ContainsAny(detail, "\x1b\x07\t\n") || !utf8.ValidString(detail) {
		t.Fatalf("detail = %q, want it sanitized onto one line", detail)
	}
	if !strings.HasPrefix(detail, "su: account locked ø") || utf8.RuneCountInString(detail) > lockoutMax {
		t.Fatalf("detail = %q (%d runes), want the notice clipped to %d runes", detail, utf8.RuneCountInString(detail), lockoutMax)
	}
}
