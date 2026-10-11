package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The kept password's failure paths with polkit's agent (keep.go): a kept
// password that is gone, refused by su or not taken by the agent is never
// used again, and the prompt goes to the user or ends; and the agent's
// channel closing ends its reader.

// errAgentGone is a fakePrompt's Answer error: the agent went away.
var errAgentGone = errors.New("pkttyagent: broken pipe")

// polkitRig is a Driver that keeps the run's password, running one helper
// step whose pkexec asks polkit's agent; each prompt the agent gets is
// made by prompt and also sent on prompts.
func polkitRig(t *testing.T, prompt func() *fakePrompt) (*rig, chan *fakePrompt) {
	t.Helper()
	agent, prompts := make(chan AgentPrompt), make(chan *fakePrompt, 3)
	helper := func(ctx context.Context, seq uint64, _ steps.Step, emit func(engine.Input)) helperclient.Outcome {
		p := prompt()
		prompts <- p
		select {
		case agent <- p:
		case <-ctx.Done():
		}
		select {
		case <-p.done:
			if p.Err() == nil {
				emit(engine.PrivilegeGranted{Seq: seq})
				emit(engine.StepExited{Seq: seq})
				return helperclient.Outcome{Granted: true}
			}
		case <-p.cancelled:
		case <-ctx.Done():
		}
		emit(engine.StepExited{Seq: seq, NotGranted: true, Err: errors.New("exit status 127")})
		return helperclient.Outcome{NotGranted: true}
	}
	h := steps.Step{ID: "h1", Title: "Root…", LogLabel: "Root", Verb: steps.VerbPaccache, Argv: []string{"false"}, NeedsPriv: true}
	r := newRig(t, engine.Options{Auth: engine.AuthPolkit},
		Deps{Polkit: true, Agent: agent, KeepPassword: true, Helper: helper}, action("pk", h))
	return r, prompts
}

// waitWiped waits until the Driver's kept password has been wiped.
func waitWiped(t *testing.T, r *rig, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !r.d.keep.Wiped() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.d.keep.Wiped() || r.d.keep.Has() {
		t.Fatalf("%s: the kept password is still kept", what)
	}
}

// A kept password su now refuses never reaches polkit: it is dropped, and
// the agent's prompt goes to the user.
func TestKeepAgentRefusedBySu(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	r, prompts := polkitRig(t, func() *fakePrompt { return newFakePrompt("org.archmaid.helper.full-cleanup", "good") })
	r.d.keep.Keep([]byte("good")) // as if an earlier prompt of the run kept it
	r.d.Send(engine.Start{Action: "pk"})
	k := &keepRig{rig: r}
	p := k.prompt()
	if !p.Polkit || p.Kind != engine.PasswordAskpass {
		t.Fatalf("NeedsPassword = %+v, want the agent's prompt for the user", p)
	}
	agent := nextPrompt(t, prompts)
	if got := agent.got(); len(got) != 0 {
		t.Fatalf("polkit got %q: a password su refused", got)
	}
	waitWiped(t, r, "su refused it")
	r.d.Send(engine.CancelPrompt{Prompt: p.ID})
}

// A kept password the agent can't take (it went away) is dropped: never
// tried again in this run.
func TestKeepAgentGoneWhileAnswering(t *testing.T) {
	suShim(t)
	r, prompts := polkitRig(t, func() *fakePrompt {
		p := newFakePrompt("org.archmaid.helper.full-cleanup", "good")
		p.answerErr = errAgentGone
		return p
	})
	r.d.keep.Keep([]byte("good"))
	r.d.Send(engine.Start{Action: "pk"})
	agent := nextPrompt(t, prompts)
	waitWiped(t, r, "the agent went away")
	if got := agent.got(); len(got) != 1 || got[0] != "good" {
		t.Fatalf("the agent got %q, want the kept password once", got)
	}
}

// The user's password for an agent prompt is kept once su accepts it, but
// dropped again when the agent can't take it: it was never handed on, so
// the run's next prompt doesn't get it.
func TestKeepUserAnswerAgentGone(t *testing.T) {
	suShim(t)
	r, prompts := polkitRig(t, func() *fakePrompt {
		p := newFakePrompt("org.archmaid.helper.full-cleanup", "good")
		p.answerErr = errAgentGone
		return p
	})
	r.d.Send(engine.Start{Action: "pk"})
	k := &keepRig{rig: r}
	p := k.prompt()
	agent := nextPrompt(t, prompts)
	r.d.Send(engine.SubmitPassword{Prompt: p.ID, Password: "good"})
	waitWiped(t, r, "the agent went away with the user's answer")
	if got := agent.got(); len(got) != 1 || got[0] != "good" {
		t.Fatalf("the agent got %q", got)
	}
}

// A kept password that is gone by the time the worker reads it (wiped
// after agentRequest saw it) sends the prompt to the user unanswered.
func TestKeepAgentKeptGone(t *testing.T) {
	d := &Driver{
		keep:   privilege.NewKeptPassword(time.Hour),
		inputs: make(chan msg, 1),
		stop:   make(chan struct{}),
		root:   context.Background(),
	}
	d.keep.Keep([]byte("good"))
	d.keep.Wipe()
	p := newFakePrompt("org.archmaid.helper.full-cleanup", "good")
	d.answerAgentKept(p)
	select {
	case m := <-d.inputs:
		if m.agent != p || !m.noKeep {
			t.Fatalf("posted %+v, want the prompt for the Session (noKeep)", m)
		}
	default:
		t.Fatal("the prompt went nowhere")
	}
	if got := p.got(); len(got) != 0 {
		t.Fatalf("the agent got %q", got)
	}
}

// Closing the agent's channel ends the Driver's reader of it at once, not
// at Close.
func TestAgentChannelClosed(t *testing.T) {
	agent := make(chan AgentPrompt)
	r := newRig(t, engine.Options{Auth: engine.AuthPolkit}, Deps{Polkit: true, Agent: agent}, action("pk"))
	reading := func() bool {
		buf := make([]byte, 1<<20)
		return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "driver.(*Driver).readAgent(")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !reading() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !reading() {
		t.Fatal("no reader of the agent's channel")
	}
	close(agent)
	deadline = time.Now().Add(5 * time.Second)
	for reading() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if reading() {
		t.Fatal("the agent's reader outlived its channel")
	}
	r.d.Close()
}

// The caller a dialog names is never blank: one whose binary couldn't be
// read is "an unknown program".
func TestRequesterOfUnknownExe(t *testing.T) {
	if got := requesterOf(privilege.Requester{CallerPid: 42}); got != "an unknown program (pid 42)" {
		t.Errorf("requesterOf(no exe) = %q", got)
	}
	if got := requesterOf(privilege.Requester{CallerPid: 42, CallerExe: "/usr/bin/yay"}); got != "/usr/bin/yay (pid 42)" {
		t.Errorf("requesterOf(yay) = %q", got)
	}
}
