package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The step verbs (internal/steps) name the root helper's verbs that do each
// step's work, so a front end can hand a step to the helper by its Verb. This
// package is the one place that can see both the steps and the helper's verb
// table (cmd/archmaid-helper/internal is the helper's own), so the two are
// tied together here.

// stepVerbConsts is every Verb* constant declared in internal/steps, read from
// the source so a new one can't slip past by not being listed in a test.
func stepVerbConsts(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "internal", "steps", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	consts := map[string]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Verb") {
						continue
					}
					var lit *ast.BasicLit
					if i < len(vs.Values) {
						lit, _ = vs.Values[i].(*ast.BasicLit)
					}
					if lit == nil || lit.Kind != token.STRING {
						t.Fatalf("steps.%s is not a string literal", name.Name)
					}
					v, _ := strconv.Unquote(lit.Value)
					consts[name.Name] = v
				}
			}
		}
	}
	if len(consts) == 0 {
		t.Fatal("found no steps.Verb* constants: the check checked nothing")
	}
	return consts
}

// TestStepVerbsAreHelperVerbs: every steps.Verb* is a verb the helper has.
func TestStepVerbsAreHelperVerbs(t *testing.T) {
	have := helper.Verbs()
	for name, v := range stepVerbConsts(t) {
		if !slices.Contains(have, v) {
			t.Errorf("steps.%s = %q is not a helper verb (have %v)", name, v, have)
		}
	}
}

// TestBuiltStepVerbsAreHelperVerbs: every step the actions build carries
// either no Verb or one the helper has — the fixed queues, and the steps only
// built at run time: Repair's reinstall (after its root recheck), the orphan
// removal the probe inserts, the services pick list's disable and the
// downloads delete. The discoveries run against shimmed pacman, sudo and
// systemctl that find one of everything.
func TestBuiltStepVerbsAreHelperVerbs(t *testing.T) {
	built := buildAllSteps(t)
	have := helper.Verbs()
	for _, s := range built {
		if s.Verb != "" && !slices.Contains(have, s.Verb) {
			t.Errorf("step %s has verb %q, which the helper doesn't have (have %v)", s.ID, s.Verb, have)
		}
	}
	// The walk must reach the run-time steps, or it proved nothing about them.
	for _, id := range []string{steps.IDRepair, steps.IDRepairCheck, steps.IDOrphansRemove, steps.IDDisableUnits,
		steps.IDDownloads, steps.IDPaccache, steps.IDHyprpm} {
		if !slices.ContainsFunc(built, func(s steps.Step) bool { return s.ID == id }) {
			t.Errorf("no %s step was built: the walk missed it", id)
		}
	}
}

// buildAllSteps builds every step every menu action can produce: each
// queue, each discovery followed through NeedsRoot's rescan and a selection
// of every item, and each in-process step run (dry) for the step it inserts.
func buildAllSteps(t *testing.T) []steps.Step {
	t.Helper()
	shimDir := t.TempDir()
	for name, script := range map[string]string{
		"pacman": `case "$1" in
-Qq) echo foo ;;
-Qkq) echo "foo /usr/lib/foo/missing"; exit 1 ;;
-Qdtq) echo libfoo ;;
-Rs) echo libfoo ;;
esac`,
		"sudo": `[ "$1" = -n ] && shift; exec "$@"`,
		// The wipe's dry run (as the user): paccache -dk0.
		"paccache": `echo "==> finished dry run: 4 candidates (disk space saved: 12.50 MiB)"`,
		"systemctl": `case "$1" in
list-units) echo "cups.service loaded inactive dead CUPS" ;;
is-enabled) echo enabled ;;
esac`,
	} {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shimDir)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Downloads", "old.iso"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	noop := func(context.Context, steps.Env) steps.Result { return steps.Result{} }
	env := actions.Env{
		LookPath:      func(file string) (string, error) { return "/usr/bin/" + file, nil },
		RepairWorkers: 1,
		AuditStep: func() steps.Step {
			return steps.Step{ID: steps.IDAURAudit, Prefetch: steps.PrefetchAURAudit, Run: noop}
		},
		AuditReport: func(context.Context) actions.Discovery { return actions.Report{Text: "audit"} },
		Downloads: func() downloads.Options {
			return downloads.Options{Home: home, Now: func() time.Time { return time.Now().Add(1000 * time.Hour) }}
		},
		DeleteDownloads: func(downloads.Plan) func(context.Context, steps.Env) steps.Result { return noop },
	}

	ctx := context.Background()
	var out []steps.Step
	var add func(q []steps.Step)
	add = func(q []steps.Step) {
		for _, s := range q {
			out = append(out, s)
			if s.Run != nil {
				if r := s.Run(ctx, steps.Env{DryRun: true}); r.InsertNext != nil {
					add([]steps.Step{*r.InsertNext})
				}
			}
		}
	}
	var walk func(id string, d actions.Discovery, env actions.Env)
	walk = func(id string, d actions.Discovery, env actions.Env) {
		switch d := d.(type) {
		case actions.Pending:
			add(d.Steps)
		case actions.NeedsSelection:
			add(d.Steps(d.Items))
		case actions.NeedsRoot:
			walk(id, d.Rescan(ctx, env), env)
		case actions.Notice:
			t.Errorf("%s: discovery ended in a notice: %q", id, d.Text)
		case actions.Report:
		default:
			t.Fatalf("%s: unknown discovery %T", id, d)
		}
	}
	// Once as under sudo, once under polkit, where Repair's recheck is a
	// helper step.
	polkitEnv := env
	polkitEnv.Polkit = true
	for _, env := range []actions.Env{env, polkitEnv} {
		for _, a := range actions.Menu {
			switch {
			case a.Steps != nil:
				add(a.Steps(env))
			case a.Discover != nil:
				walk(a.ID, a.Discover(ctx, env), env)
			}
		}
	}
	return out
}

// TestRepairCheckFitsTheHelper: the polkit recheck's cap is the helper's,
// and the step it builds is one the helper accepts the shape of: check-files
// with the names after "--".
func TestRepairCheckFitsTheHelper(t *testing.T) {
	if actions.HelperMaxNames != helper.MaxNames {
		t.Fatalf("actions.HelperMaxNames = %d, helper.MaxNames = %d", actions.HelperMaxNames, helper.MaxNames)
	}
	s := actions.RepairCheckStep([]string{"a", "b"})
	if s.Verb != steps.VerbCheckFiles || !slices.Contains(helper.Verbs(), s.Verb) {
		t.Fatalf("verb %q", s.Verb)
	}
}
