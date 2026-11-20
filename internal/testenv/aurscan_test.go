package testenv

import (
	"go/ast"
	"go/types"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// aurScan adds, through add, each place where a literal (or a constant, or
// a variable assigned one) paru or yay, bare or as an absolute path
// (/usr/bin/paru), reaches process start (#309). Unlike bareNames it looks
// only at those places, so the name as data (a path join, a package name, a
// display string, a config file name, BuildDir's argument) passes:
//   - the program of exec.Command (first argument), exec.CommandContext
//     (second), exec.LookPath, syscall.Exec and os.StartProcess (first, and
//     the first element of the argv the last two take), proc.Query (second)
//     and proc.QueryIn (fourth);
//   - proc.Spec{Name} and exec.Cmd{Path}, exec.Cmd{Args}'s first element,
//     and a Path or Args assigned to a field;
//   - an Argv field's first element (a steps.Step's argv, of any type), or
//     the program after a leading sudo and its flags;
//   - a shell's -c script naming one as a word.
//
// Not seen (the #131 limits): a name that reaches exec through another
// wrapper, a field, a result or a string built at run time. The product
// starts the helper through internal/sysbin, whose Paru and Yay are
// variables (never constants, or they would fold here) read at the call.
func aurScan(files []*ast.File, info *types.Info, str func(ast.Expr) (string, bool), isString func(types.Type) bool,
	assigned map[types.Object][]ast.Expr, name func(ast.Expr) string, add func(ast.Node, string)) {
	isAUR := func(s string) bool {
		if slices.Contains(aurNames, s) {
			return true
		}
		return strings.HasPrefix(s, "/") && slices.Contains(aurNames, pathpkg.Base(s))
	}
	// val folds e as bareScan's bare does: a constant, or a variable given
	// one directly. It returns the label bareAllowed keys by.
	val := func(e ast.Expr) (string, bool) {
		id, o := namedObj(info, e)
		if s, ok := str(e); ok {
			if !isString(info.Types[e].Type) || !isAUR(s) {
				return "", false
			}
			if _, isConst := o.(*types.Const); isConst {
				return id.Name + ": " + strconv.Quote(s), true
			}
			return strconv.Quote(s), true
		}
		v, ok := o.(*types.Var)
		if !ok || !isString(v.Type()) {
			return "", false
		}
		for _, a := range assigned[v] {
			if s, ok := str(a); ok && isAUR(s) {
				return id.Name + ": " + strconv.Quote(s), true
			}
		}
		return "", false
	}
	// argv0 checks a program-first argument list: its first element, or the
	// one after a leading sudo and its flags.
	argv0 := func(n ast.Node, label string, elts []ast.Expr) {
		if len(elts) == 0 {
			return
		}
		i := 0
		if s, ok := str(elts[0]); ok && s == "sudo" {
			for i = 1; i < len(elts); i++ {
				if f, ok := str(elts[i]); !ok || !strings.HasPrefix(f, "-") {
					break
				}
			}
		}
		if i < len(elts) {
			if s, ok := val(elts[i]); ok {
				add(n, label+"["+s+"]")
			}
		}
	}
	isProc := func(call *ast.CallExpr) bool {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return false
		}
		pn, ok := info.Uses[id].(*types.PkgName)
		return ok && pn.Imported().Path() == modulePath+"/internal/proc"
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if info.Types[n.Fun].IsType() {
					return true
				}
				fn := name(n.Fun)
				prog := -1 // the index of the program argument
				switch {
				case fn == "Command" || fn == "LookPath" || fn == "Exec" || fn == "StartProcess":
					prog = 0
				case fn == "CommandContext" || (isProc(n) && fn == "Query"):
					prog = 1
				case isProc(n) && fn == "QueryIn":
					prog = 3
				}
				if prog >= 0 && prog < len(n.Args) {
					if s, ok := val(n.Args[prog]); ok {
						pre := ""
						if prog > 0 {
							pre = "…, "
						}
						add(n, fn+"("+pre+s+")")
					}
				}
				if (fn == "Exec" || fn == "StartProcess") && len(n.Args) > 1 {
					argv0(n, fn+" argv", flatArgs(n.Args[1:2]))
				}
				if s, ok := shellScriptWith(n.Args, str, aurWord); ok {
					add(n, fn+"("+s+")")
				}
			case *ast.CompositeLit:
				t := name(n.Type)
				for _, el := range n.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					k, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch {
					case (t == "Spec" || t == "Cmd") && (k.Name == "Name" || k.Name == "Path"):
						if s, ok := val(kv.Value); ok {
							add(n, t+"{"+k.Name+": "+s+"}")
						}
					case (t == "Cmd" && k.Name == "Args") || k.Name == "Argv":
						argv0(n, t+"{"+k.Name+"}", flatArgs([]ast.Expr{kv.Value}))
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) != len(n.Rhs) {
					return true
				}
				for i, l := range n.Lhs {
					sel, ok := l.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					switch sel.Sel.Name {
					case "Path":
						if s, ok := val(n.Rhs[i]); ok {
							add(n, ".Path = "+s)
						}
					case "Args", "Argv":
						argv0(n, "."+sel.Sel.Name, flatArgs(n.Rhs[i:i+1]))
					}
				}
			}
			return true
		})
	}
}

// namedObj is the constant or variable e names (x, or pkg.X), else nil.
func namedObj(info *types.Info, e ast.Expr) (*ast.Ident, types.Object) {
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		return e, info.Uses[e]
	case *ast.SelectorExpr:
		return e.Sel, info.Uses[e.Sel]
	}
	return nil, nil
}

// aurBad is one planted exec of the AUR helper per form aurScan covers.
// It is assembled, not literal, only where the absolute path would trip
// TestNoAbsolutePrivilegedExec's cousin; here the paths are the point.
const aurBad = "package x\n" +
	"const yayName = \"yay\"\n" +
	"var viaVar = \"paru\"\n" +
	"func f(c *exec.Cmd) {\n" +
	"\texec.Command(\"paru\", \"-Sc\")\n" +
	"\texec.CommandContext(ctx, \"yay\")\n" +
	"\texec.Command(\"/usr/bin/paru\")\n" +
	"\texec.Command(yayName)\n" +
	"\texec.Command(viaVar)\n" +
	"\texec.LookPath(\"yay\")\n" +
	"\tsyscall.Exec(\"/usr/bin/yay\", []string{\"yay\"}, nil)\n" +
	"\t_ = proc.Spec{Name: \"paru\"}\n" +
	"\t_ = exec.Cmd{Path: \"/usr/bin/yay\"}\n" +
	"\t_ = exec.Cmd{Args: []string{\"paru\", \"-Sc\"}}\n" +
	"\t_ = steps.Step{Argv: []string{\"yay\", \"-Sc\"}}\n" +
	"\t_ = steps.Step{Argv: []string{\"sudo\", \"-n\", \"paru\", \"-Syu\"}}\n" +
	"\t_ = steps.Step{Argv: append([]string{\"paru\"}, flags...)}\n" +
	"\tc.Path = \"/usr/bin/paru\"\n" +
	"\tc.Args = []string{\"yay\"}\n" +
	"\texec.Command(\"sh\", \"-c\", \"yay -Sc --noconfirm\")\n" +
	"}\n"

// aurOK is the name as data, which must pass.
const aurOK = "package x\n" +
	"const Paru = \"paru\"\n" +
	"const Yay = \"yay\"\n" +
	"func f(d Deps) {\n" +
	"\t_ = filepath.Join(home, \".config\", \"paru\")\n" +
	"\t_ = BuildDir(d, Yay)\n" +
	"\t_ = pkgdb.Package{Name: \"yay\"}\n" +
	"\t_ = name == \"paru\"\n" +
	"\tfmt.Println(\"AUR helper\", \"yay\")\n" +
	"\t_ = h.Name == Paru\n" +
	"\t_ = []string{h.Name, \"-Sc\"}\n" +
	"\t_ = steps.Step{Argv: []string{\"sudo\", \"pacman\", \"-S\", \"yay\"}}\n" +
	"\t_ = proc.Spec{Name: sysbin.Yay, Args: []string{\"yay\"}}\n" +
	"\t_ = exec.Command(\"pacman\", \"-Qi\", \"paru\")\n" +
	"\t_ = proc.Query(ctx, \"pacman\", \"-Qi\", \"yay\")\n" +
	"\texec.Command(sysbin.Yay)\n" +
	"\texec.Command(\"echo\", \"yay\")\n" +
	"\texec.Command(\"sh\", \"-c\", \"echo yaywards; ls paruser\")\n" +
	"}\n"

// TestAurScanFindsEveryExecForm: each exec form of a bare or absolute
// paru/yay is flagged (#309).
func TestAurScanFindsEveryExecForm(t *testing.T) {
	got, err := barePrivilegedCalls("x.go", aurBad)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`Command("paru")`, `CommandContext(…, "yay")`, `Command("/usr/bin/paru")`, `Command(yayName: "yay")`,
		`Command(viaVar: "paru")`, `LookPath("yay")`, `Exec("/usr/bin/yay")`, `Exec argv["yay"]`,
		`Spec{Name: "paru"}`, `Cmd{Path: "/usr/bin/yay"}`, `Cmd{Args}["paru"]`,
		`Step{Argv}["yay"]`, `Step{Argv}["paru"]`, `Step{Argv}["paru"]`,
		`.Path = "/usr/bin/paru"`, `.Args["yay"]`, `Command(sh -c "yay -Sc --noconfirm")`,
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %q,\nwant %q", got, want)
	}
}

// TestAurScanIgnoresData: the names as data are not flagged (#309).
func TestAurScanIgnoresData(t *testing.T) {
	got, err := barePrivilegedCalls("x.go", aurOK)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("flagged data: %q", got)
	}
}

// TestAurScanProcQuery: proc.Query's and proc.QueryIn's program argument is
// an exec position (found when it is the helper, and only that argument),
// resolved through the import so another package's Query is not one.
func TestAurScanProcQuery(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/proc/proc.go", "package proc\n"+
		"func Query(ctx int, name string, args ...string) {}\n"+
		"func QueryIn(ctx int, dir string, env []string, name string, args ...string) {}\n")
	write("internal/other/other.go", "package other\nfunc Query(ctx int, name string) {}\n")
	write("internal/x/x.go", "package x\n"+
		"import (\n\t\""+modulePath+"/internal/proc\"\n\t\""+modulePath+"/internal/other\"\n)\n"+
		"func f() {\n"+
		"\tproc.Query(0, \"paru\", \"-Qu\")\n"+
		"\tproc.QueryIn(0, \"/tmp\", nil, \"/usr/bin/yay\", \"-Sc\")\n"+
		"\tproc.Query(0, \"pacman\", \"-Qi\", \"yay\")\n"+
		"\tproc.QueryIn(0, \"yay\", nil, \"pacman\")\n"+
		"\tother.Query(0, \"yay\")\n"+
		"}\n")
	hits, err := bareModuleScan(root, []string{"internal/proc/proc.go", "internal/other/other.go", "internal/x/x.go"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.desc)
	}
	want := []string{`Query(…, "paru")`, `QueryIn(…, "/usr/bin/yay")`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q,\nwant %q", got, want)
	}
}
