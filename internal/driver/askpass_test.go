package driver

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/privilege/privtest"
	"github.com/Enzord758/archmaid/internal/proc"
)

// Fake pids for the askpass chain: the helper, run by sudo, run by the
// step's child (the real one).
const (
	helperPid = 990001
	sudoPid   = 990002
	ourUID    = 4242
)

// askpassClient is sudo's side of one request: what it was answered, or
// that it was hung up on.
type askpassClient struct {
	req  *privilege.Request
	got  chan string // the answer line, or "" when hung up
	peer privilege.Peer
}

func newAskpassClient(t *testing.T, prompt string, peer privilege.Peer) *askpassClient {
	server, client := net.Pipe()
	c := &askpassClient{req: privtest.NewRequest(server, prompt, peer), got: make(chan string, 1), peer: peer}
	go func() {
		line, _ := privilege.ReadAnswer(client, nil) // the relay's side, handshake and all
		client.Close()
		c.got <- line
	}()
	t.Cleanup(func() { server.Close(); client.Close() })
	return c
}

// suShim puts an su first on PATH that takes "good" only.
func suShim(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nread pw\n[ \"$pw\" = good ]\n"
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// A mid-run sudo prompt: the request is verified against the running
// step's child and asked for; a wrong password asks again, the right one
// reaches sudo. A request whose peer fails the check is hung up on and
// noted in the step's output.
func TestAskpass(t *testing.T) {
	suShim(t)
	reqs := make(chan *privilege.Request)
	st := shStep("sudo-step", "echo $$; exec sleep 30")
	st.NeedsPriv = true
	r := newRig(t, engine.Options{}, Deps{Askpass: reqs}, action("a", st))
	r.d.Send(engine.Start{Action: "a"})
	pid := 0
	for pid == 0 {
		ev, _ := r.next()
		if _, ok := ev.(engine.OutputChunk); ok {
			pid = pidOf(t, ev)
		}
	}
	child, err := proc.Read(pid)
	if err != nil {
		t.Fatal(err)
	}
	privtest.FakeSystem(t, func() int { return ourUID }, "/fake/archmaid", func(p int) (proc.Info, error) {
		switch p {
		case helperPid:
			return proc.Info{PPid: sudoPid, Start: child.Start + 20, Exe: "/fake/archmaid"}, nil
		case sudoPid:
			return proc.Info{PPid: pid, Start: child.Start + 10, Exe: "/usr/bin/sudo"}, nil
		case pid:
			return proc.Info{PPid: 1, Start: child.Start, Exe: "/usr/bin/sh"}, nil
		}
		return proc.Info{}, errors.New("no such process")
	})
	good := newAskpassClient(t, "[sudo] password for me:", privilege.Peer{Pid: helperPid, UID: ourUID, Start: child.Start + 20})
	reqs <- good.req

	awaitPassword := func() engine.NeedsPassword {
		for {
			ev, _ := r.next()
			if p, ok := ev.(engine.NeedsPassword); ok {
				return p
			}
		}
	}
	p := awaitPassword()
	if p.Kind != engine.PasswordAskpass || p.Requester != "/usr/bin/sh (pid "+strconv.Itoa(pid)+")" || p.Prompt != "[sudo] password for me:" {
		t.Fatalf("NeedsPassword = %+v", p)
	}
	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "bad"})
	p = awaitPassword()
	if p.Error == "" || p.AttemptsLeft != 2 {
		t.Fatalf("after a wrong password: %+v", p)
	}
	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	select {
	case line := <-good.got:
		if line != "good\n" {
			t.Fatalf("sudo got %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sudo got no answer")
	}

	for { // the Session has taken the answer: no request is open
		ev, _ := r.next()
		if res, ok := ev.(engine.AuthResolved); ok && res.Granted {
			break
		}
	}

	// A peer that isn't ours: hung up on, and said so in the output.
	bad := newAskpassClient(t, "[sudo] password:", privilege.Peer{Pid: helperPid, UID: ourUID + 1, Start: child.Start + 20})
	reqs <- bad.req
	for {
		ev, _ := r.next()
		if c, ok := ev.(engine.OutputChunk); ok && strings.Contains(strings.Join(c.Lines, "\n"), "refused a sudo password request") {
			break
		}
	}
	if line := <-bad.got; line != "" {
		t.Fatalf("a refused peer got %q", line)
	}
	r.d.Send(engine.Abort{})
	for {
		ev, _ := r.next()
		if _, ok := ev.(engine.Finished); ok {
			break
		}
	}

	// With no step running, a request is refused (the Session hangs up).
	idle := newAskpassClient(t, "[sudo] password:", privilege.Peer{Pid: helperPid, UID: ourUID, Start: child.Start + 20})
	if !gone(pid, 5*time.Second) {
		t.Fatal("the aborted child was not reaped")
	}
	reqs <- idle.req
	if line := <-idle.got; line != "" {
		t.Fatalf("a request with nothing running got %q", line)
	}
	// One left unanswered at Close is hung up on by Close.
	last := newAskpassClient(t, "[sudo] password:", privilege.Peer{Pid: helperPid, UID: ourUID, Start: child.Start + 20})
	reqs <- last.req
	r.d.Close()
	if line := <-last.got; line != "" {
		t.Fatalf("a request open at Close got %q", line)
	}
}
