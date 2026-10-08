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

// TestCopyError: the failed step's title, error and last 20 lines, its
// URLs' userinfo redacted.
func TestCopyError(t *testing.T) {
	m := NewModel()
	var lines []string
	for i := range 25 {
		lines = append(lines, "line "+string(rune('a'+i)))
	}
	lines = append(lines, "fatal: https://me:pw@git.example/x.git")
	m.Apply([]engine.Event{plan("s"), engine.StepStarted{ID: "s"}, engine.OutputChunk{ID: "s", Lines: lines},
		engine.StepDone{ID: "s", Status: steps.Failed, Err: "exit status 1"}})
	got := m.CopyError("s")
	if strings.Contains(got, "pw@") || !strings.Contains(got, "https://***@git.example/x.git") {
		t.Errorf("not redacted: %q", got)
	}
	if !strings.HasPrefix(got, "Step s\nError: exit status 1\n") || strings.Contains(got, "line a\n") || strings.Count(got, "\n") != 22 {
		t.Errorf("CopyError = %q", got)
	}
}
