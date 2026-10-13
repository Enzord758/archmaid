package driver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/installed"
)

// TestCLICOLORForceStaysInTopgradesTree (task #112): a whole Full Cleanup on
// the real Driver, then the widget's updates refresh. CLICOLOR_FORCE is in
// the environment of topgrade's child and, as a plain consequence of process
// inheritance, of what topgrade starts (its sudo, which runs archmaid's
// askpass relay, and the AUR build scripts: the second shim line stands for
// them). It is in no other child: not the yay -Sc --aur step, not the
// pkexec call to the root helper, not hyprpm, not the updates refresh
// (systemctl --user start, or its script fallback), and not archmaid's own
// environment, which every other child inherits.
func TestCLICOLORForceStaysInTopgradesTree(t *testing.T) {
	stream := grantedStream(t)
	r, calls := fullCleanupRig(t, stream)
	if _, set := os.LookupEnv("CLICOLOR_FORCE"); set {
		t.Skip("CLICOLOR_FORCE is exported in the test environment")
	}
	dir := filepath.Dir(calls)
	record := func(name string) string {
		return "printf '%s\\n' \"${CLICOLOR_FORCE-unset}\" >> '" + filepath.Join(dir, name+".clicolor") + "'\n"
	}
	for name, body := range map[string]string{
		// the second line is a descendant: sudo, then the askpass relay
		"topgrade":   record("topgrade") + "'" + filepath.Join(dir, "descendant") + "'\necho updated\n",
		"descendant": record("topgrade-descendant"),
		"yay":        record("yay") + "echo yay cleaned\n",
		"hyprpm":     record("hyprpm") + "echo plugins\n",
		"pkexec":     "echo \"$*\" >> '" + calls + "'\n" + record("pkexec") + stream + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.run(actions.IDFullCleanup, func(ev engine.Event) engine.Command {
		if c, ok := ev.(engine.NeedsConfirm); ok {
			return engine.Confirm{Prompt: c.ID, Yes: true}
		}
		return nil
	})
	for name, want := range map[string][]string{
		"topgrade":            {"1"},
		"topgrade-descendant": {"1"},
		"yay":                 {"unset"},
		"pkexec":              {"unset"},
	} {
		if got := readFileLines(t, filepath.Join(dir, name+".clicolor")); !slices.Equal(got, want) {
			t.Errorf("%s saw CLICOLOR_FORCE %q, want %q", name, got, want)
		}
	}
	if got := readFileLines(t, filepath.Join(dir, "hyprpm.clicolor")); got != nil && !slices.Equal(got, []string{"unset"}) {
		t.Errorf("hyprpm saw CLICOLOR_FORCE %q", got)
	}
	if _, set := os.LookupEnv("CLICOLOR_FORCE"); set {
		t.Errorf("archmaid's own environment has CLICOLOR_FORCE")
	}

	// the updates refresh: systemctl --user start fails here, so the script
	// fallback runs, with archmaid's own environment
	script := filepath.Join(dir, "archmaid-updates-check")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+record("updates")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := installed.UpdatesScript
	installed.UpdatesScript = script
	t.Cleanup(func() { installed.UpdatesScript = old })
	if err := RefreshUpdates(context.Background()); err != nil {
		t.Fatalf("RefreshUpdates: %v", err)
	}
	waitFor(t, "the updates script", func() bool { return len(readFileLines(t, filepath.Join(dir, "updates.clicolor"))) > 0 })
	if got := readFileLines(t, filepath.Join(dir, "updates.clicolor")); !slices.Equal(got, []string{"unset"}) {
		t.Errorf("the updates script saw CLICOLOR_FORCE %q, want unset", got)
	}
}
