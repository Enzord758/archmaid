package runview

import (
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

func TestRedactURLs(t *testing.T) {
	for in, want := range map[string]string{
		"fatal: unable to access 'https://user:s3cret@example.com/repo.git/'": "fatal: unable to access 'https://***@example.com/repo.git/'",
		"git+ssh://git@aur.archlinux.org/yay.git":                             "git+ssh://***@aur.archlinux.org/yay.git",
		"a https://a:b@x/ and ftp://c@y/ both":                                "a https://***@x/ and ftp://***@y/ both",
		"mail me at user@host.example":                                        "mail me at user@host.example",
		"https://example.com/path@v1.2":                                       "https://example.com/path@v1.2",
		"ssh enzord@host":                                                     "ssh enzord@host",
		"no url here":                                                         "no url here",
	} {
		if got := RedactURLs(in); got != want {
			t.Errorf("RedactURLs(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRedact: credentials outside a URL's userinfo go too (the name kept),
// and ordinary text is left alone.
func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		// hits
		"GET https://api.example/v1?token=abc123&page=2": "GET https://api.example/v1?token=***&page=2",
		"https://x/cb?code=1&access_token=ya29.a0&x=1":   "https://x/cb?code=1&access_token=***&x=1",
		"refresh_token=r-1 expires":                      "refresh_token=*** expires",
		"curl 'https://x/?API_KEY=sk-123'":               "curl 'https://x/?API_KEY=***'",
		"using api-key=k1;secret=s2":                     "using api-key=***;secret=***",
		"env: PASSWORD=hunter2 USER=me":                  "env: PASSWORD=*** USER=me",
		"key=abc":                                        "key=***",
		"-H 'Authorization: Bearer eyJhbGci.x.y'":        "-H 'Authorization: Bearer ***'",
		"authorization: Basic dXNlcjpwYXNz":              "authorization: Basic ***",
		"db password: s3cr3t":                            "db password: ***",
		"passwd = p4ss":                                  "passwd = ***",
		"fatal: https://me:pw@git.example/x?token=t":     "fatal: https://***@git.example/x?token=***",
		// non-hits
		"ssh enzord@host": "ssh enzord@host",
		"/home/me/@cache/x and https://example.com/path@v1.2":     "/home/me/@cache/x and https://example.com/path@v1.2",
		"press any key to continue; the key value is in the docs": "press any key to continue; the key value is in the docs",
		"monkey=1 turkey=2 --key=abc":                             "monkey=1 turkey=2 --key=abc",
		"https://archlinux.org/packages/?q=yay&page=2":            "https://archlinux.org/packages/?q=yay&page=2",
		"[sudo] password for enzord:":                             "[sudo] password for enzord:",
		"Password:\nnext line":                                    "Password:\nnext line",
		"error: password authentication failed":                   "error: password authentication failed",
		"tokens=5 keys=3":                                         "tokens=5 keys=3",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCopyError: the failed step's title, error and last 20 lines, its
// URLs' userinfo and other credentials redacted.
func TestCopyError(t *testing.T) {
	m := NewModel()
	var lines []string
	for i := range 25 {
		lines = append(lines, "line "+string(rune('a'+i)))
	}
	lines = append(lines, "fatal: https://me:pw@git.example/x.git", "GET /api?token=t0k")
	m.Apply([]engine.Event{plan("s"), engine.StepStarted{ID: "s"}, engine.OutputChunk{ID: "s", Lines: lines},
		engine.StepDone{ID: "s", Status: steps.Failed, Err: "exit status 1"}})
	got := m.CopyError("s")
	if strings.Contains(got, "pw@") || !strings.Contains(got, "https://***@git.example/x.git") ||
		strings.Contains(got, "t0k") || !strings.Contains(got, "?token=***") {
		t.Errorf("not redacted: %q", got)
	}
	if !strings.HasPrefix(got, "Step s\nError: exit status 1\n") || strings.Contains(got, "line a\n") || strings.Contains(got, "line b\n") ||
		strings.Count(got, "\n") != 22 {
		t.Errorf("CopyError = %q", got)
	}
}
