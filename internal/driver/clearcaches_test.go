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

// wipeRig is a Driver running the real "Clear caches" action
// with shims on PATH: paccache (logs every call to paccache.log; its dry run
// prints a summary), pkexec (logs to pkexec.log, then prints a one-verb
// stream) and sudo (logs to sudo.log, then fails: nothing may use it under polkit).
func wipeRig(t *testing.T) (r *rig, dir string) {
	t.Helper()
	a, ok := actions.ByID(actions.IDClearCaches)
	if !ok {
		t.Fatal("no Clear caches action")
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

// cachePicker answers the Clear caches pick with picked and records the
// pick event; everything else is left to the rig's defaults.
func cachePicker(t *testing.T, picked []string, seen *engine.NeedsSelection) func(engine.Event) engine.Command {
	return func(ev engine.Event) engine.Command {
		switch ev := ev.(type) {
		case engine.NeedsSelection:
			*seen = ev
			return engine.Select{Prompt: ev.ID, Picked: picked}
		case engine.NeedsConfirm:
			t.Errorf("the pick is the confirm, but %q was asked", ev.Msg)
		case engine.NeedsPassword:
			t.Errorf("a password was asked under polkit")
		}
		return nil
	}
}

// homeCache fills a scratch $HOME/.cache and returns its app folder.
func homeCache(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cache", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestClearCachesUnderPolkit: the pick shows both sizes and the warning
// before anything runs as root; ticking the package cache makes exactly one
// pkexec call, the helper's paccache-wipe verb with no argument; ticking
// ~/.cache alone, or nothing, makes none and empties only what was ticked.
func TestClearCachesUnderPolkit(t *testing.T) {
	for _, c := range []struct {
		name          string
		picked        []string
		wantPkexec    bool
		wantCacheGone bool
	}{
		{"package cache", []string{actions.PickPkgCache}, true, false},
		{"user cache", []string{actions.PickUserCache}, false, true},
		{"both", []string{actions.PickUserCache, actions.PickPkgCache}, true, true},
		{"nothing", nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := homeCache(t)
			r, dir := wipeRig(t)
			var seen engine.NeedsSelection
			evs := r.run(actions.IDClearCaches, cachePicker(t, c.picked, &seen))
			if len(seen.Rows) != 2 || !strings.Contains(seen.Rows[0].Label, "4 KiB") ||
				!strings.Contains(seen.Rows[1].Label, "2.5 GiB (7 files)") ||
				!strings.Contains(seen.Rows[1].Note, "without internet") {
				t.Errorf("the pick showed %+v", seen)
			}
			if got := logLines(t, filepath.Join(dir, "paccache.log")); !slices.Equal(got, []string{"--nocolor -dk0"}) {
				t.Errorf("paccache calls %q, want only the dry run", got)
			}
			if sudo := logLines(t, filepath.Join(dir, "sudo.log")); sudo != nil {
				t.Errorf("sudo ran under polkit: %q", sudo)
			}
			got := logLines(t, filepath.Join(dir, "pkexec.log"))
			if c.wantPkexec {
				want := []string{"--disable-internal-agent /usr/lib/archmaid/archmaid-helper paccache-wipe"}
				if !slices.Equal(got, want) {
					t.Errorf("pkexec calls %q, want %q", got, want)
				}
			} else if got != nil {
				t.Errorf("pkexec ran: %q", got)
			}
			_, err := os.Stat(app)
			if gone := os.IsNotExist(err); gone != c.wantCacheGone {
				t.Errorf("~/.cache emptied=%v, want %v (%v)", gone, c.wantCacheGone, err)
			}
			fin := find[engine.Finished](t, evs)
			if len(c.picked) == 0 {
				if fin.Outcome != engine.OutcomeCancelled {
					t.Errorf("Finished = %+v, want cancelled", fin)
				}
			} else if fin.Errs != 0 || fin.Skipped != 0 {
				t.Errorf("Finished = %+v", fin)
			}
		})
	}
}
