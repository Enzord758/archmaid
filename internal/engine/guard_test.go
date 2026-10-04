package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// REMOVE THIS FILE WHEN MILESTONE E5 LANDS (see docs/HANDOVER.md, open items).
//
// Until E5 the Session still panics (errNotImplemented) on SubmitPassword,
// Abort and some discoveries. A panic on a Driver goroutine would kill archmaid
// mid-step, without stopping children or writing last-run. So no production
// code may call engine.New yet; only tests and the packages below may.
const removeWhenE5Lands = "TestNoProductionCallerBeforeE5"

const enginePath = "github.com/Enzord758/archmaid/internal/engine"

// engineNewAllowed are the module-relative directories whose non-test files
// may call engine.New before E5.
var engineNewAllowed = map[string]bool{
	"internal/engine":            true,
	"internal/engine/stub":       true,
	"internal/engine/enginetest": true,
}

// TestNoProductionCallerBeforeE5: no non-_test.go file outside the allowed
// packages calls engine.New (the gui/ module included, if present). Remove
// this test when E5 lands; see docs/HANDOVER.md.
func TestNoProductionCallerBeforeE5(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		scanned++
		if engineNewAllowed[filepath.ToSlash(filepath.Dir(rel))] {
			return nil
		}
		lines, err := callsEngineNew(path, nil)
		if err != nil {
			return err
		}
		for _, line := range lines {
			t.Errorf("%s:%d calls engine.New, but the engine panics on SubmitPassword, Abort and some discoveries until E5 (%s: remove it then)", rel, line, removeWhenE5Lands)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 20 {
		t.Fatalf("only %d non-test Go files scanned: walked the wrong tree?", scanned)
	}
}

// TestCallsEngineNewFindsCalls: the scan behind the guard sees calls through
// the default name and an alias, and nothing else.
func TestCallsEngineNewFindsCalls(t *testing.T) {
	src := "package x\n" +
		"import (\n\t\"" + enginePath + "\"\n\teng \"" + enginePath + "\"\n\tother \"example.com/engine\"\n)\n" +
		"func f() {\n" +
		"\t_ = engine.New(engine.Options{})\n" + // line 8
		"\t_ = eng.New(eng.Options{})\n" + // line 9
		"\t_ = other.New()\n" +
		"\t_ = engine.NewThing()\n" +
		"\t_ = engine.Options{}\n" +
		"}\n"
	got, err := callsEngineNew("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 8 || got[1] != 9 {
		t.Fatalf("got lines %v, want [8 9]", got)
	}
	got, err = callsEngineNew("y.go", "package y\nfunc f() { engine.New() }\n")
	if err != nil || len(got) != 0 {
		t.Fatalf("no engine import: got %v, %v; want none", got, err)
	}
}

// callsEngineNew parses a Go file (src as for parser.ParseFile) and returns
// the lines of its calls to New from the engine package, under whatever name
// it is imported as.
func callsEngineNew(path string, src any) ([]int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != enginePath {
			continue
		}
		name := "engine"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = true
	}
	if len(names) == 0 {
		return nil, nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "New" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && names[id.Name] {
			out = append(out, fset.Position(call.Pos()).Line)
		}
		return true
	})
	return out, nil
}
