package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestFetchSanitizesTheAURError: the RPC's error text is untrusted network
// input that ends up in a notice and a log, so it arrives on one line,
// without escapes or controls, and capped.
func TestFetchSanitizesTheAURError(t *testing.T) {
	evil := "slow down\x1b]52;c;ZXZpbA==\x07\n[HIGH] forged line\x1b[2J" + strings.Repeat("x", 1000)
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"type": "error", "error": evil})
	})
	_, err := fetch(context.Background(), url, []string{"x"})
	if err == nil {
		t.Fatal("want the AUR's error")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\x07\n\r") {
		t.Fatalf("err = %q, want it sanitized onto one line", msg)
	}
	if !strings.HasPrefix(msg, "AUR error: slow down [HIGH] forged line") || utf8.RuneCountInString(msg) > len("AUR error: ")+errorMax {
		t.Fatalf("err = %q (%d runes), want the message capped at %d runes", msg, utf8.RuneCountInString(msg), errorMax)
	}
}

// TestShortErrCutsByRunes: a long error is cut by runes, never mid-character
// (the old byte cut at 67 split a two-byte "é" into invalid UTF-8), and
// sanitized; an ASCII one is cut exactly as before.
func TestShortErrCutsByRunes(t *testing.T) {
	got := ShortErr(errors.New(strings.Repeat("é", 100)), time.Second)
	if !utf8.ValidString(got) || got != strings.Repeat("é", 67)+"…" {
		t.Fatalf("ShortErr = %q, want 67 runes and …", got)
	}
	ascii := strings.Repeat("x", 100)
	if got := ShortErr(errors.New(ascii), time.Second); got != ascii[:67]+"…" {
		t.Fatalf("ShortErr(ascii) = %q, want the old cut", got)
	}
	if got := ShortErr(errors.New(strings.Repeat("y", 70)), time.Second); got != strings.Repeat("y", 70) {
		t.Fatalf("ShortErr(70 runes) = %q, want it whole", got)
	}
	if got := ShortErr(errors.New("bad\x1b[31m\nthing"), time.Second); got != "bad thing" {
		t.Fatalf("ShortErr = %q, want it sanitized", got)
	}
}
