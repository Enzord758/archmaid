package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Full Cleanup on the GUI's path (AuthPolkit, the real helperclient.Run)
// against a pkexec shim: the owner's rule is ONE polkit dialog per run.

var fcPlan = steps.OrphanPlan{
	Hash:    helperclient.PlanHash([]string{"libfoo"}, []string{"libfoo 1.0-1"}),
	Direct:  []string{"libfoo"},
	Cascade: []string{"libfoo 1.0-1"},
}

// fullCleanupRig is a Driver running the real Full Cleanup action under
// polkit, with shims for topgrade, yay and hyprpm, and a pkexec shim that
// appends each call's argv to the returned file and then runs body.
func fullCleanupRig(t *testing.T, body string) (*rig, string) {
	t.Helper()
	fc, ok := actions.ByID(actions.IDFullCleanup)
	if !ok {
		t.Fatal("no Full Cleanup action")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "pkexec-calls")
	for name, script := range map[string]string{
		"pkexec":   "echo \"$*\" >> '" + calls + "'\n" + body,
		"topgrade": "echo updated",
		"yay":      "echo yay cleaned",
		"hyprpm":   "echo plugins updated",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	standIn := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(standIn, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	helpertest.UseHelperFile(t, standIn)

	noop := func(context.Context, steps.Env) steps.Result { return steps.Result{} }
	env := actions.Env{
		Polkit:   true,
		LookPath: func(f string) (string, error) { return "/usr/bin/" + f, nil },
		AuditStep: func() steps.Step {
			return steps.Step{ID: steps.IDAURAudit, Title: "Audit…", LogLabel: "Audit", Run: noop}
		},
		AuditReport: func(context.Context) actions.Discovery { return nil },
		Downloads:   func() downloads.Options { return downloads.Options{} },
		DeleteDownloads: func(downloads.Plan) func(context.Context, steps.Env) steps.Result {
			return noop
		},
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) { return fcPlan, nil },
	}
	// Deps.Helper unset: the real helperclient.Run.
	return newRig(t, engine.Options{Auth: engine.AuthPolkit, Env: env}, Deps{Polkit: true}, fc), calls
}

// pkexecCalls is each pkexec call's argv.
func pkexecCalls(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// grantedStream is the helper's stream for the whole call, every part ok.
func grantedStream(t *testing.T) string {
	t.Helper()
	parts := []string{"remove-orphans", "paccache", "paccache-wipe", "clean-tmp", "vacuum-journal", "check-files"}
	ls := []string{`{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}`}
	for i, p := range parts {
		ls = append(ls,
			fmt.Sprintf(`{"v":1,"t":"progress","step":%d,"of":6,"label":"doing %s","part":%q}`, i+1, p, p),
			fmt.Sprintf(`{"v":1,"t":"line","stream":"out","text":"out of %s"}`, p),
			fmt.Sprintf(`{"v":1,"t":"done","step":%d,"of":6,"part":%q,"ok":true,"code":0}`, i+1, p))
	}
	ls = append(ls, `{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}`)
	f := filepath.Join(t.TempDir(), "stream")
	if err := os.WriteFile(f, []byte(strings.Join(ls, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return "while IFS= read -r l; do printf '%s\\n' \"$l\"; done < '" + f + "'"
}

var rootRows = []string{steps.IDOrphansRemove, steps.IDPaccache, steps.IDPaccacheWipe, steps.IDTmpfiles,
	steps.IDJournal, steps.IDIntegrity}

// TestFullCleanupPolkitOneDialog: a whole Full Cleanup under polkit, every
// confirm answered yes, makes exactly ONE pkexec call (full-cleanup, with
// the confirmed orphan plan's hash and the wipe), after both confirms;
// every root row still starts and ends on its own, in plan order; and the
// user steps run after it.
func TestFullCleanupPolkitOneDialog(t *testing.T) {
	r, calls := fullCleanupRig(t, grantedStream(t))
	confirms := 0
	evs := r.run(actions.IDFullCleanup, func(ev engine.Event) engine.Command {
		if c, ok := ev.(engine.NeedsConfirm); ok {
			if _, err := os.Stat(calls); err == nil {
				t.Errorf("confirm for %q after pkexec ran", c.Step)
			}
			confirms++
			return engine.Confirm{Prompt: c.ID, Yes: true}
		}
		return nil
	})
	got := pkexecCalls(t, calls)
	if len(got) != 1 {
		t.Fatalf("%d pkexec calls, want exactly one: %q", len(got), got)
	}
	want := "/usr/lib/archmaid/archmaid-helper full-cleanup --keep 3 --journal 2weeks --orphans " + fcPlan.Hash + " --wipe"
	if got[0] != want {
		t.Errorf("pkexec %q\nwant    %q", got[0], want)
	}
	if confirms != 3 { // the gate, the orphans, the wipe
		t.Errorf("%d confirms, want 3", confirms)
	}
	if n := len(all[engine.AuthWaiting](evs)); n != 1 {
		t.Errorf("%d polkit waits, want 1", n)
	}
	var started, ok []string
	for _, ev := range evs {
		switch ev := ev.(type) {
		case engine.StepStarted:
			started = append(started, ev.ID)
		case engine.StepDone:
			if ev.Status != steps.OK {
				t.Errorf("%s: %+v", ev.ID, ev)
			}
			ok = append(ok, ev.ID)
		}
	}
	wantRows := append(append([]string{steps.IDAURAudit, steps.IDTopgrade, steps.IDOrphansProbe}, rootRows...),
		steps.IDYayCache, steps.IDHyprpm)
	if !slices.Equal(started, wantRows) || !slices.Equal(ok, wantRows) {
		t.Errorf("started %q\ndone    %q\nwant    %q", started, ok, wantRows)
	}
	// Each part's output is on its own row.
	for _, ev := range evs {
		if c, isChunk := ev.(engine.OutputChunk); isChunk && slices.Contains(rootRows, c.ID) {
			for _, l := range c.Lines {
				if strings.HasPrefix(l, "out of ") && !strings.Contains(l, map[string]string{
					steps.IDOrphansRemove: "remove-orphans", steps.IDPaccache: "paccache", steps.IDPaccacheWipe: "paccache-wipe",
					steps.IDTmpfiles: "clean-tmp", steps.IDJournal: "vacuum-journal", steps.IDIntegrity: "check-files"}[c.ID]) {
					t.Errorf("%s got %q", c.ID, l)
				}
			}
		}
	}
	if fin := find[engine.Finished](t, evs); fin.Errs != 0 || fin.Skipped != 0 {
		t.Errorf("Finished = %+v", fin)
	}
}

// TestFullCleanupPolkitRefused: the one dialog refused (pkexec 127) marks
// every root step it covered AuthRefused, and only those; the run goes on
// with yay -Sc and hyprpm. Still exactly one pkexec call.
func TestFullCleanupPolkitRefused(t *testing.T) {
	r, calls := fullCleanupRig(t, "exit 127")
	evs := r.run(actions.IDFullCleanup, yes)
	if got := pkexecCalls(t, calls); len(got) != 1 {
		t.Fatalf("%d pkexec calls, want exactly one: %q", len(got), got)
	}
	status := map[string]steps.Status{}
	for _, d := range all[engine.StepDone](evs) {
		status[d.ID] = d.Status
	}
	for _, id := range rootRows {
		if status[id] != steps.AuthRefused {
			t.Errorf("%s: status %d, want AuthRefused", id, status[id])
		}
	}
	for _, id := range []string{steps.IDAURAudit, steps.IDTopgrade, steps.IDOrphansProbe, steps.IDYayCache, steps.IDHyprpm} {
		if status[id] != steps.OK {
			t.Errorf("%s: status %d, want OK", id, status[id])
		}
	}
	if fin := find[engine.Finished](t, evs); fin.Refused != len(rootRows) || fin.Outcome != engine.OutcomeCompleted {
		t.Errorf("Finished = %+v", fin)
	}
}

// all is every event of type T in evs.
func all[T engine.Event](evs []engine.Event) []T {
	var out []T
	for _, ev := range evs {
		if e, ok := ev.(T); ok {
			out = append(out, e)
		}
	}
	return out
}
