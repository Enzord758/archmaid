package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// gatePlan is the engine plan of Full Cleanup under env, as the gate lists
// it (engine.PlanStep from each step).
func gatePlan(t *testing.T, env actions.Env) []engine.PlanStep {
	t.Helper()
	a, ok := actions.ByID(actions.IDFullCleanup)
	if !ok {
		t.Fatal("no Full Cleanup")
	}
	var plan []engine.PlanStep
	for _, s := range a.Steps(env) {
		plan = append(plan, engine.PlanStep{ID: s.ID, Title: s.Title, Verb: s.Verb, NeedsPriv: s.NeedsPriv, Confirm: s.Confirm != ""})
	}
	return plan
}

// TestGateNoteMatchesAuthMode (Dev 5's VM round b8): the real GUI is polkit
// with a kept password (mainReal's Options), and its Full Cleanup gate must
// say what the run does: the root steps together under one authorisation,
// as the Overview's kicker does, never "each through sudo". The gate's
// number is the kicker's.
func TestGateNoteMatchesAuthMode(t *testing.T) {
	env := actions.Env{
		Polkit: true,
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) {
			return steps.OrphanPlan{}, errors.New("not in a preview")
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		AuditStep: func() steps.Step {
			return steps.Step{ID: steps.IDAURAudit, Title: "Auditing AUR packages…"}
		},
	}
	prev, ok := overview.PlanPreview(env, actions.IDFullCleanup)
	if !ok {
		t.Fatal("no preview")
	}
	kicker := heroKicker(prev, true, false)
	plan := gatePlan(t, env)

	real := realOptions(engine.Options{Auth: engine.AuthPolkit}, nil, nil, "", nil)
	if !real.KeepsPassword {
		t.Fatal("the real GUI's Options do not keep the password")
	}
	got := gateNotesFor(real, plan, false)[0]
	want := fmt.Sprintf("%d steps run as root, together, through one authorisation; topgrade gets root through its own sudo. You type your password once", prev.Root)
	if !strings.HasPrefix(got, want) {
		t.Errorf("real GUI gate note %q, want it to start %q", got, want)
	}
	if strings.Contains(got, "each through sudo") {
		t.Errorf("gate note %q says sudo for the helper's steps", got)
	}
	if want := fmt.Sprintf("%d root steps in one authorisation", prev.Root); !strings.Contains(kicker, want) {
		t.Errorf("kicker %q, want %q", kicker, want)
	}

	// Sudo mode: each root step asks sudo.
	sudoEnv := env
	sudoEnv.Polkit = false
	sudo := gateNotesFor(Options{Engine: engine.Options{Auth: engine.AuthSudo}, KeepsPassword: true}, gatePlan(t, sudoEnv), false)[0]
	if !strings.Contains(sudo, " each through sudo") || strings.Contains(sudo, "authorisation") {
		t.Errorf("sudo-mode gate note %q", sudo)
	}
}
