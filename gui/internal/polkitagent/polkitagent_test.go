package polkitagent

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/polkitagent/agenttest"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// shim installs Shim in a fresh dir and sets its environment; it returns
// the shim's path and its dir.
func shim(t *testing.T, env ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pkttyagent")
	if err := os.WriteFile(path, []byte(agenttest.Shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DIR", dir)
	t.Setenv("AGENT_USER", me(t))
	t.Setenv("AGENT_WANT", "hunter2")
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	return path, dir
}

func me(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}

func start(t *testing.T, path string) *Agent {
	t.Helper()
	self, err := proc.Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	a, err := Start(Options{Path: path, Pid: os.Getpid(), Start: self.Start, User: me(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Stop)
	return a
}

func request(t *testing.T, dir, action string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "request.tmp"), []byte(action), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "request.tmp"), filepath.Join(dir, "request")); err != nil {
		t.Fatal(err)
	}
}

func next(t *testing.T, a *Agent) driver.AgentPrompt {
	t.Helper()
	select {
	case p := <-a.Prompts():
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no prompt")
		return nil
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func waitDone(t *testing.T, p driver.AgentPrompt) error {
	t.Helper()
	select {
	case <-p.Done():
		return p.Err()
	case <-time.After(5 * time.Second):
		t.Fatal("no verdict")
		return nil
	}
}

// The agent registers for the given process, passes on archmaid's prompt
// for the current user and writes the answer once echo is off; polkit's
// verdict comes back.
func TestAgentAnswers(t *testing.T) {
	path, dir := shim(t)
	a := start(t, path)
	self, _ := proc.Read(os.Getpid())
	if args := read(t, dir, "agent.args"); args != "--process "+strconv.Itoa(os.Getpid())+","+strconv.FormatUint(self.Start, 10)+" --notify-fd 3" {
		t.Fatalf("pkttyagent ran with %q", args)
	}
	for _, tc := range []struct {
		pw string
		ok bool
	}{{"wrong", false}, {"hunter2", true}} {
		request(t, dir, "org.archmaid.helper.full-cleanup")
		p := next(t, a)
		if p.Action() != "org.archmaid.helper.full-cleanup" || p.Prompt() != "Password: " {
			t.Fatalf("prompt %q for %q", p.Prompt(), p.Action())
		}
		if err := p.Answer([]byte(tc.pw)); err != nil {
			t.Fatal(err)
		}
		if err := waitDone(t, p); (err == nil) != tc.ok {
			t.Fatalf("%s: verdict %v", tc.pw, err)
		}
		if p.Answer([]byte(tc.pw)) == nil {
			t.Fatal("a prompt took a second answer")
		}
	}
	if got := read(t, dir, "agent.got"); got != "wrong\nhunter2" {
		t.Fatalf("pkttyagent read %q", got)
	}
}

// Several identities: the current user's is chosen.
func TestAgentChoosesTheUser(t *testing.T) {
	path, dir := shim(t, "AGENT_IDENTS=multi")
	a := start(t, path)
	request(t, dir, "org.archmaid.helper.check-files")
	p := next(t, a)
	if read(t, dir, "chosen") != "2" {
		t.Fatalf("chose %q, want the user's entry", read(t, dir, "chosen"))
	}
	if err := p.Answer([]byte("hunter2")); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, p); err != nil {
		t.Fatal(err)
	}
}

// Not archmaid's action, or not the current user: nothing is passed on,
// nothing is written, and pkttyagent is stopped (polkit fails the
// request).
func TestAgentRefuses(t *testing.T) {
	for _, tc := range []struct{ name, action, idents string }{
		{"foreign action", "org.freedesktop.systemd1.manage-units", ""},
		{"lookalike action", "org.archmaid.helperx.run", ""},
		{"other user", "org.archmaid.helper.full-cleanup", "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, dir := shim(t, "AGENT_IDENTS="+tc.idents)
			a := start(t, path)
			request(t, dir, tc.action)
			select {
			case p := <-a.Prompts():
				t.Fatalf("passed on %q", p.Action())
			case <-a.exited:
			case <-time.After(5 * time.Second):
				t.Fatal("pkttyagent wasn't stopped")
			}
			if got := read(t, dir, "agent.got"); got != "" {
				t.Fatalf("pkttyagent was answered %q", got)
			}
		})
	}
}

// Cancel stops pkttyagent unanswered.
func TestAgentCancel(t *testing.T) {
	path, dir := shim(t)
	a := start(t, path)
	request(t, dir, "org.archmaid.helper.full-cleanup")
	p := next(t, a)
	p.Cancel()
	if err := waitDone(t, p); err != ErrRefused {
		t.Fatalf("verdict %v", err)
	}
	select {
	case <-a.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("pkttyagent wasn't stopped")
	}
	if p.Answer([]byte("hunter2")) == nil || read(t, dir, "agent.got") != "" {
		t.Fatal("a cancelled prompt was answered")
	}
}

// No agent of ours: pkttyagent missing, or it can't register.
func TestAgentUnavailable(t *testing.T) {
	self, _ := proc.Read(os.Getpid())
	o := Options{Path: filepath.Join(t.TempDir(), "pkttyagent"), Pid: os.Getpid(), Start: self.Start, User: me(t)}
	if _, err := Start(o); err == nil {
		t.Fatal("started a missing pkttyagent")
	}
	path, _ := shim(t, "AGENT_MODE=noreg")
	o.Path = path
	_, err := Start(o)
	if err == nil || !strings.Contains(err.Error(), "Error registering authentication agent") {
		t.Fatalf("a failed registration: %v", err)
	}
}
