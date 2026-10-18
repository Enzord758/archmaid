package driver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// wipeRig is a Driver running the real "Delete entire package cache" action
// with shims on PATH: paccache (logs every call to paccache.log; its dry run
// prints a summary), pkexec (logs to pkexec.log, then prints a one-verb
// stream) and sudo (logs to sudo.log, then fails: nothing may use it under polkit).
func wipeRig(t *testing.T) (r *rig, dir string) {
	t.Helper()
	a, ok := actions.ByID(actions.IDWipeCache)
	if !ok {
		t.Fatal("no wipe action")
	}
	dir = t.TempDir()
	stream := strings.Join([]string{
		`{"v":1,"t":"start","verb":"paccache-wipe","version":"0.1.0"}`,
		`{"v":1,"t":"progress","step":1,"of":1,"label":"Removing every cached package"}`,
		`{"v":1,"t":"line","stream":"out","text":"removed it all"}`,
		`{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}`,
	}, "\n")
	for name, script := range map[string]string{
		"paccache": "echo \"$*\" >> '" + filepath.Join(dir, "paccache.log") + "'\n" +
			"case \" $* \" in *' -dk0 '*) echo '==> finished dry run: 7 candidates (disk space saved: 2.50 GiB)' ;; *) echo wiped ;; esac",
		"pkexec": "echo \"$*\" >> '" + filepath.Join(dir, "pkexec.log") + "'\ncat <<'EOF'\n" + stream + "\nEOF",
		"sudo":   "echo \"$*\" >> '" + filepath.Join(dir, "sudo.log") + "'\nexit 1",
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
		Polkit:      true,
		LookPath:    func(f string) (string, error) { return "/usr/bin/" + f, nil },
		AuditStep:   func() steps.Step { return steps.Step{ID: steps.IDAURAudit, Run: noop} },
		AuditReport: func(context.Context) actions.Discovery { return nil },
		Downloads:   func() downloads.Options { return downloads.Options{} },
		DeleteDownloads: func(downloads.Plan) func(context.Context, steps.Env) steps.Result {
			return noop
		},
		PlanOrphans: func(context.Context) (steps.OrphanPlan, error) { return fcPlan, nil },
	}
	return newRig(t, engine.Options{Auth: engine.AuthPolkit, Env: env}, Deps{Polkit: true}, a), dir
}

func logLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestWipeCacheUnderPolkit: the confirm carries the dry run's size before
// anything runs as root; a yes makes exactly one pkexec call, the
// helper's paccache-wipe verb with no argument; a no runs nothing as root.
func TestWipeCacheUnderPolkit(t *testing.T) {
	for _, answer := range []bool{true, false} {
		r, dir := wipeRig(t)
		var shown string
		evs := r.run(actions.IDWipeCache, func(ev engine.Event) engine.Command {
			if c, ok := ev.(engine.NeedsConfirm); ok && c.Step == steps.IDPaccacheWipe {
				shown = c.Msg
				if got := logLines(t, filepath.Join(dir, "pkexec.log")); got != nil {
					t.Errorf("pkexec ran before the confirm was answered: %q", got)
				}
				return engine.Confirm{Prompt: c.ID, Yes: answer}
			}
			return nil
		})
		if !strings.Contains(shown, "2.50 GiB") || !strings.Contains(shown, "7 package files") ||
			!strings.Contains(shown, "without internet") {
			t.Errorf("the confirm showed %q", shown)
		}
		if got := logLines(t, filepath.Join(dir, "paccache.log")); !slices.Equal(got, []string{"--nocolor -dk0"}) {
			t.Errorf("answer=%v: paccache calls %q, want only the dry run", answer, got)
		}
		if sudo := logLines(t, filepath.Join(dir, "sudo.log")); sudo != nil {
			t.Errorf("answer=%v: sudo ran under polkit: %q", answer, sudo)
		}
		got := logLines(t, filepath.Join(dir, "pkexec.log"))
		if answer {
			want := []string{"--disable-internal-agent /usr/lib/archmaid/archmaid-helper paccache-wipe"}
			if !slices.Equal(got, want) {
				t.Errorf("pkexec calls %q, want %q", got, want)
			}
			if fin := find[engine.Finished](t, evs); fin.Errs != 0 || fin.Skipped != 0 {
				t.Errorf("Finished = %+v", fin)
			}
		} else {
			if got != nil {
				t.Errorf("No still ran as root: %q", got)
			}
			if fin := find[engine.Finished](t, evs); fin.Skipped != 1 {
				t.Errorf("Finished = %+v, want the step skipped", fin)
			}
		}
	}
}

// TestWipeCacheConfirmDefaultsToNo (review of #186): the confirm is
// destructive and Enter answers No.
func TestWipeCacheConfirmDefaultsToNo(t *testing.T) {
	r, _ := wipeRig(t)
	seen := false
	r.run(actions.IDWipeCache, func(ev engine.Event) engine.Command {
		if c, ok := ev.(engine.NeedsConfirm); ok && c.Step == steps.IDPaccacheWipe {
			seen = true
			if c.Default || !c.Destructive || c.YesLabel != "Delete" {
				t.Errorf("confirm default=%v destructive=%v yes=%q", c.Default, c.Destructive, c.YesLabel)
			}
			return engine.Confirm{Prompt: c.ID, Yes: false}
		}
		return nil
	})
	if !seen {
		t.Error("no confirm")
	}
}
