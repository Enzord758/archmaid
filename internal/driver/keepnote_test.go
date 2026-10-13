package driver

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
)

// firstPrompt starts the rig's first askpass request and returns the
// NeedsPassword the Driver publishes for it.
func firstPrompt(g *guardRig) engine.NeedsPassword {
	g.t.Helper()
	g.ask(helperPid)
	return g.until("the first prompt", func(ev engine.Event) bool { _, ok := ev.(engine.NeedsPassword); return ok }).(engine.NeedsPassword)
}

// The prompt says whether the kept password will answer (#144b): after
// the PATH guard tripped it says no, with a reason, so the GUI's dialog
// doesn't claim the password is kept.
func TestPromptKeepsOffAfterPathGuard(t *testing.T) {
	g := newGuardRig(t, []string{dirWith(t, "sudo", 0o755)}, nil)
	p := firstPrompt(g)
	if p.Keeps {
		t.Error("the prompt says the password is kept, after the PATH guard turned keeping off")
	}
	if p.KeepOff == "" {
		t.Error("the prompt gives no reason the password isn't kept")
	}
}

// A normal run's first prompt says the password will be kept.
func TestPromptKeepsOnNormalRun(t *testing.T) {
	g := newGuardRig(t, nil, nil)
	p := firstPrompt(g)
	if !p.Keeps || p.KeepOff != "" {
		t.Errorf("Keeps %v, KeepOff %q; want the password kept", p.Keeps, p.KeepOff)
	}
}

// A kept password that was refused is off too: the prompt that follows
// says so (no PATH reason, so KeepOff is empty).
func TestPromptKeepsOffAfterFail(t *testing.T) {
	g := newGuardRig(t, nil, nil)
	g.d.keep.Fail()
	p := firstPrompt(g)
	if p.Keeps {
		t.Error("the prompt says the password is kept after Fail")
	}
}
