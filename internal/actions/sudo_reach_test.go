package actions

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// Every step of the TUI whose program can reach sudo gets the askpass and
// the sudo wrapper (task #194, docs/HANDOVER.md "Nothing may prompt on the
// raw tty"): NeedsPriv (root work) or SudoByName (elevates itself). yay's
// cache step had neither, so its `sudo pacman -Sc` prompted on the tty.

// sudoReaching are the programs a step may name whose own process (or a
// child of it) can run sudo by name. A program in neither list fails the
// scan below: whoever adds a step with a new program decides which it is.
var sudoReaching = []string{"sudo", "topgrade", "yay", "paru", "hyprpm", "makepkg"}

// neverSudo are the programs steps name that never start sudo: bash runs a
// fixed rm, pacman is run directly as the user for -Qk, or is only the line a
// root helper step shows (the helper runs it as root, with no sudo).
var neverSudo = []string{"bash", "pacman"}

// TestEveryStepThatCanReachSudoGetsTheWrapper reads this package's step
// literals (a step is a composite literal with Argv and Title keys): the
// program an Argv starts with is one of sudoReaching or neverSudo, and a
// sudoReaching one sets NeedsPriv or SudoByName.
func TestEveryStepThatCanReachSudoGetsTheWrapper(t *testing.T) {
	fset := token.NewFileSet()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			keys := map[string]ast.Expr{}
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if id, ok := kv.Key.(*ast.Ident); ok {
						keys[id.Name] = kv.Value
					}
				}
			}
			argv, hasArgv := keys["Argv"]
			if _, hasTitle := keys["Title"]; !hasArgv || !hasTitle {
				return true
			}
			seen++
			where := fset.Position(lit.Pos()).String()
			prog := firstString(argv)
			switch {
			case slices.Contains(neverSudo, prog):
			case slices.Contains(sudoReaching, prog):
				if !set(keys["NeedsPriv"]) && !set(keys["SudoByName"]) {
					t.Errorf("%s: a step running %q can reach sudo but has neither NeedsPriv nor SudoByName, so its sudo would prompt on the TUI's terminal", where, prog)
				}
			default:
				t.Errorf("%s: a step runs %q: list it in sudoReaching (and give the step NeedsPriv or SudoByName) or in neverSudo", where, prog)
			}
			return true
		})
	}
	if seen < 8 {
		t.Fatalf("scanned %d step literals, want the package's 11 or so: the scan checked too little", seen)
	}
}

// set reports whether a step literal's flag is given and not the literal false.
func set(e ast.Expr) bool {
	if e == nil {
		return false
	}
	id, ok := e.(*ast.Ident)
	return !ok || id.Name != "false"
}

// firstString is the first string literal in e: the program of an Argv given
// as []string{"x", …} or append([]string{"x", …}, …).
func firstString(e ast.Expr) string {
	prog := ""
	ast.Inspect(e, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.STRING && prog == "" {
			prog, _ = strconv.Unquote(b.Value)
		}
		return prog == ""
	})
	return prog
}

// TestQueuedStepsThatCanReachSudoAsk is the same rule on the queues as built
// for the TUI (sudo: Polkit off): every step whose program is sudo-reaching
// has Asks(), Full Cleanup's yay-cache is NeedsPriv and hyprpm is SudoByName
// without NeedsPriv (it may not need root: TestNoPrivilegedStepAfterHyprpm).
// Under polkit (the GUI) the yay step is the plain user step `yay -Sc --aur`
// and hyprpm gets nothing: the GUI's children have no terminal.
func TestQueuedStepsThatCanReachSudoAsk(t *testing.T) {
	for _, a := range Menu {
		if a.Steps == nil {
			continue
		}
		env := testEnv(true)
		for _, s := range a.Steps(env) {
			if s.Run != nil || len(s.Argv) == 0 {
				continue
			}
			if slices.Contains(sudoReaching, s.Argv[0]) && !s.Asks() {
				t.Errorf("%s: step %s (%v) can reach sudo but gets no askpass or sudo wrapper in the TUI", a.ID, s.ID, s.Argv)
			}
		}
	}

	fc, _ := ByID(IDFullCleanup)
	find := func(q []steps.Step, id string) steps.Step {
		i := slices.IndexFunc(q, func(s steps.Step) bool { return s.ID == id })
		if i < 0 {
			t.Fatalf("no step %s in %v", id, ids(q))
		}
		return q[i]
	}
	tui := fc.Steps(testEnv(true))
	if y := find(tui, steps.IDYayCache); !y.NeedsPriv || y.Argv[0] != "yay" {
		t.Errorf("TUI: yay-cache step %+v, want a NeedsPriv yay step (its sudo pacman -Sc must ask through the popup)", y)
	}
	if h := find(tui, steps.IDHyprpm); !h.SudoByName || h.NeedsPriv {
		t.Errorf("TUI: hyprpm step %+v, want SudoByName and no NeedsPriv", h)
	}

	env := testEnv(true)
	env.Polkit = true
	env.PlanOrphans = func(context.Context) (steps.OrphanPlan, error) { return testPlan, nil }
	gui := fc.Steps(env)
	if y := find(gui, steps.IDYayCache); y.Asks() {
		t.Errorf("polkit: yay-cache step %+v, want a plain user step: no sudo runs there", y)
	}
	if h := find(gui, steps.IDHyprpm); h.Asks() {
		t.Errorf("polkit: hyprpm step %+v, want it as before: no askpass", h)
	}
}
