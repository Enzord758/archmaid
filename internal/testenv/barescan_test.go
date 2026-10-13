package testenv

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The scan behind TestNoBarePrivilegedExec (#131; constant folding since
// #136, review #134 N2). It type-checks each package from source with
// go/types and reads a call's program argument the way the compiler does:
// a literal, a named constant, a concatenation of constants and a typed
// constant (or one converted) all fold to their string. Imports from
// inside the module (the root's and gui/'s) are type-checked from source
// too, so a constant from another archmaid package folds; every other
// import (the standard library, gotk4) is an empty stand-in, and the type
// errors that leaves are ignored: they don't stop the checker from
// recording the arguments' values.
//
// Known limits (docs/HANDOVER.md, the #131 landmine): a variable counts
// only through a value assigned to it directly (its declaration or a plain
// `=`/`:=` anywhere in its package), not through a field, a function's
// result, a parameter or a range loop; a string built at run time
// (Sprintf, Join) is unseen; and a shell's -c script is read only from a
// call's own arguments (or a []string literal, or append to one, among
// them), not from a step's Argv or a proc.Spec's Args. An absolute
// /usr/bin path is TestNoAbsolutePrivilegedExec's.

// modulePath is the root module's import path; gui/'s module is
// modulePath + "/gui", in the gui directory, so one mapping serves both.
const modulePath = "github.com/Enzord758/archmaid"

// shells are the programs whose -c script bareScan reads.
var shells = []string{"sh", "bash", "dash", "zsh", "ksh", "mksh"}

// shellC matches an option cluster that holds -c (-c, -ec, -lc…).
var shellC = regexp.MustCompile(`^-[a-zA-Z]*c[a-zA-Z]*$`)

// bareWord matches a bareNames name as a word of a shell script (between
// blanks or shell punctuation, so "pseudo", "sudoku" and "su-do" don't).
var bareWord = func() *regexp.Regexp {
	var q []string
	for _, n := range bareNames {
		q = append(q, regexp.QuoteMeta(n))
	}
	return regexp.MustCompile("(?:^|[\\s;&|()`!{}])(?:" + strings.Join(q, "|") + ")(?:$|[\\s;&|()`])")
}()

// bareConstAllowed are the constants (package path.Name) whose value is a
// bareNames name but that are never a program to run, wherever they are
// compared or logged; as the program of an exec-like call (exec.Command,
// CommandContext, syscall.Exec, proc.Spec{Name}, exec.Cmd{Path}) they are
// flagged like any other (#140). bareConstUsed records which ones a scan
// met (for the stale check in TestNoBarePrivilegedExec).
var (
	bareConstAllowed = map[string]string{
		modulePath + "/internal/steps.IDTopgrade": "the topgrade step's ID, a key compared, looked up and logged; the step's program is its Argv, which privilege.Sudo.Command runs from sysbin",
	}
	bareConstUsed = map[string]bool{}
)

// bareHit is one call or literal that names a privileged program bare.
type bareHit struct {
	file string // slash-separated, relative to the module root
	pos  token.Pos
	desc string // as bareAllowed keys it, after "file:"
}

// barePrivilegedCalls parses one Go file (src as for parser.ParseFile) as
// a package of its own and describes its hits, in source order.
func barePrivilegedCalls(path string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	im := &srcImporter{fset: fset, pkgs: map[string]*types.Package{}}
	var out []string
	for _, h := range bareScan(fset, "x", []*ast.File{f}, im) {
		out = append(out, h.desc)
	}
	return out, nil
}

// bareModuleScan parses the module's files (rel to root) and scans them
// package by package: one directory's files of one package name.
func bareModuleScan(root string, rels []string) ([]bareHit, error) {
	fset := token.NewFileSet()
	type key struct{ dir, name string }
	groups := map[key][]*ast.File{}
	var order []key
	for _, rel := range rels {
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		k := key{filepath.ToSlash(filepath.Dir(rel)), f.Name.Name}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], f)
	}
	im := &srcImporter{root: root, fset: fset, pkgs: map[string]*types.Package{}}
	var out []bareHit
	for _, k := range order {
		path := modulePath
		if k.dir != "." {
			path += "/" + k.dir
		}
		for _, h := range bareScan(fset, path, groups[k], im) {
			rel, err := filepath.Rel(root, h.file)
			if err != nil {
				return nil, err
			}
			h.file = filepath.ToSlash(rel)
			out = append(out, h)
		}
	}
	return out, nil
}

// srcImporter type-checks the module's own packages from source (cached)
// and gives every other import an empty, complete stand-in.
type srcImporter struct {
	root string // "" for a lone file: nothing is the module's
	fset *token.FileSet
	pkgs map[string]*types.Package
}

func (im *srcImporter) Import(path string) (*types.Package, error) {
	if p, ok := im.pkgs[path]; ok {
		return p, nil
	}
	rel, inModule := strings.CutPrefix(path, modulePath)
	inModule = inModule && im.root != "" && (rel == "" || strings.HasPrefix(rel, "/"))
	var p *types.Package
	if inModule {
		p = im.fromSource(path, filepath.Join(im.root, filepath.FromSlash(rel)))
	}
	if p == nil {
		p = types.NewPackage(path, pathpkg.Base(path))
		p.MarkComplete()
	}
	im.pkgs[path] = p
	return p, nil
}

// fromSource type-checks dir's non-test files of its most common package
// name (a //go:build ignore file can name another), or returns nil.
func (im *srcImporter) fromSource(path, dir string) *types.Package {
	entries, err := os.ReadDir(dir) // sorted by name
	if err != nil {
		return nil
	}
	byName := map[string][]*ast.File{}
	best := ""
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(im.fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		pkg := f.Name.Name
		byName[pkg] = append(byName[pkg], f)
		if best == "" || len(byName[pkg]) > len(byName[best]) || (len(byName[pkg]) == len(byName[best]) && pkg < best) {
			best = pkg
		}
	}
	files := byName[best]
	if len(files) == 0 {
		return nil
	}
	conf := types.Config{Importer: im, Error: func(error) {}, FakeImportC: true}
	p, _ := conf.Check(path, im.fset, files, nil)
	return p
}

// bareScan type-checks files as package path and returns, in source order,
// each call whose first or second argument, and each composite literal
// whose Name or Path field, folds to a bareNames name (directly, or as a
// variable assigned one), and each call that runs a shell with a -c
// script naming one as a word.
func bareScan(fset *token.FileSet, path string, files []*ast.File, im types.Importer) []bareHit {
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: im, Error: func(error) {}, FakeImportC: true}
	_, _ = conf.Check(path, fset, files, info)

	// The values assigned to each variable anywhere in the package.
	assigned := map[types.Object][]ast.Expr{}
	obj := func(id *ast.Ident) types.Object {
		if o := info.Defs[id]; o != nil {
			return o
		}
		return info.Uses[id]
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) && (n.Tok == token.ASSIGN || n.Tok == token.DEFINE) {
					for i, l := range n.Lhs {
						if id, ok := ast.Unparen(l).(*ast.Ident); ok {
							if o := obj(id); o != nil {
								assigned[o] = append(assigned[o], n.Rhs[i])
							}
						}
					}
				}
			case *ast.ValueSpec:
				if len(n.Names) == len(n.Values) {
					for i, id := range n.Names {
						if o := obj(id); o != nil {
							assigned[o] = append(assigned[o], n.Values[i])
						}
					}
				}
			}
			return true
		})
	}
	str := func(e ast.Expr) (string, bool) {
		tv, ok := info.Types[e]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
			return "", false
		}
		return constant.StringVal(tv.Value), true
	}
	// A program name reaches exec as a string: a value of a type of its
	// own (logstore.Category's "topgrade") must be converted first, and
	// the conversion is what counts.
	isString := func(t types.Type) bool {
		b, ok := types.Unalias(t).(*types.Basic)
		return ok && b.Info()&types.IsString != 0
	}
	// named is the constant or variable e names (x, or pkg.X), else nil.
	named := func(e ast.Expr) (*ast.Ident, types.Object) {
		var id *ast.Ident
		switch e := ast.Unparen(e).(type) {
		case *ast.Ident:
			id = e
		case *ast.SelectorExpr:
			id = e.Sel
		default:
			return nil, nil
		}
		return id, info.Uses[id]
	}
	// prog is true where e is the program of an exec-like call or literal:
	// there bareConstAllowed exempts nothing.
	bare := func(e ast.Expr, prog bool) (string, bool) {
		id, o := named(e)
		if s, ok := str(e); ok {
			if !isString(info.Types[e].Type) || !slices.Contains(bareNames, s) {
				return "", false
			}
			if c, isConst := o.(*types.Const); isConst {
				if q := c.Pkg().Path() + "." + c.Name(); !prog && bareConstAllowed[q] != "" {
					bareConstUsed[q] = true
					return "", false
				}
				return id.Name + ": " + strconv.Quote(s), true
			}
			return strconv.Quote(s), true
		}
		v, ok := o.(*types.Var)
		if !ok || !isString(v.Type()) {
			return "", false
		}
		for _, a := range assigned[v] {
			if s, ok := str(a); ok && slices.Contains(bareNames, s) {
				return id.Name + ": " + strconv.Quote(s), true
			}
		}
		return "", false
	}
	name := func(e ast.Expr) string {
		switch e := e.(type) {
		case *ast.Ident:
			return e.Name
		case *ast.SelectorExpr:
			return e.Sel.Name
		}
		return "?"
	}
	// The calls and literals that run their program: exec.Command(name…),
	// exec.CommandContext(ctx, name…), syscall.Exec(path…), proc.Spec{Name},
	// exec.Cmd{Path}. A bareConstAllowed constant is no exemption there.
	execCalls := []string{"Command", "CommandContext", "Exec"}
	execLits := []string{"Spec", "Cmd"}
	var out []bareHit
	add := func(n ast.Node, desc string) {
		out = append(out, bareHit{file: fset.Position(n.Pos()).Filename, pos: n.Pos(), desc: desc})
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				for i, a := range n.Args {
					if i > 1 || info.Types[n.Fun].IsType() { // a conversion runs nothing
						break
					}
					if s, ok := bare(a, slices.Contains(execCalls, name(n.Fun))); ok {
						pre := ""
						if i == 1 {
							pre = "…, "
						}
						add(n, name(n.Fun)+"("+pre+s+")")
					}
				}
				if s, ok := shellScript(n.Args, str); ok {
					add(n, name(n.Fun)+"("+s+")")
				}
			case *ast.CompositeLit:
				for _, el := range n.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "Name" || k.Name == "Path") {
						if s, ok := bare(kv.Value, slices.Contains(execLits, name(n.Type))); ok {
							add(n, name(n.Type)+"{"+k.Name+": "+s+"}")
						}
					}
				}
			}
			return true
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].pos < out[j].pos
	})
	return out
}

// shellScript reports a shell run with a -c script that names a bareNames
// program as a word, among args (a []string literal, or append to one,
// spread in place): `sh -c "script"` as a description.
func shellScript(args []ast.Expr, str func(ast.Expr) (string, bool)) (string, bool) {
	flat := flatArgs(args)
	for i, a := range flat {
		sh, ok := str(a)
		if !ok || !slices.Contains(shells, pathpkg.Base(sh)) {
			continue
		}
		for j := i + 1; j+1 < len(flat); j++ {
			opt, ok := str(flat[j])
			if !ok || !strings.HasPrefix(opt, "-") {
				break
			}
			if !shellC.MatchString(opt) {
				continue
			}
			if script, ok := str(flat[j+1]); ok && bareWord.MatchString(script) {
				return sh + " " + opt + " " + strconv.Quote(script), true
			}
			break
		}
	}
	return "", false
}

// flatArgs spreads a []string literal argument, or an append whose first
// argument is one, into the argument list.
func flatArgs(args []ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, a := range args {
		switch e := ast.Unparen(a).(type) {
		case *ast.CompositeLit:
			if _, ok := e.Type.(*ast.ArrayType); ok {
				out = append(out, e.Elts...)
				continue
			}
		case *ast.CallExpr:
			if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "append" && len(e.Args) > 0 {
				out = append(out, flatArgs(e.Args)...)
				continue
			}
		}
		out = append(out, a)
	}
	return out
}
