// Command covblocks lists the statements a coverage profile leaves
// uncovered: every block with a zero count, its file and line range, the
// function it sits in and its statement count. It is the raw material of
// docs/coverage-map.md and is read-only: it reads profiles and Go source,
// writes only to stdout, and runs nothing.
//
// Usage (from the repo root):
//
//	go run ./scripts/covblocks [-json] [-summary] [-src] PROFILE...
//
// Several profiles are merged first, as scripts/check.sh counts one: a block
// (same file and range) is covered if any line for it, in any profile, has a
// non-zero count. Profiles from `go test -coverprofile` and from
// `go tool covdata textfmt` (scripts/cover-merge.sh) read the same.
//
// File names in a profile are import paths
// (github.com/Enzord758/archmaid/internal/tui/update.go); they are mapped to
// disk through the module paths in ./go.mod and ./gui/go.mod.
//
//	-summary   per-package statements, covered, uncovered and percent, then the total
//	-json      the uncovered blocks as a JSON array (file, start, end, func, stmts)
//	-src       each uncovered block followed by its source lines
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Block is one coverage block of a profile.
type Block struct {
	File      string `json:"file"` // import-path file name, as in the profile
	StartLine int    `json:"start"`
	StartCol  int    `json:"-"`
	EndLine   int    `json:"end"`
	EndCol    int    `json:"-"`
	Stmts     int    `json:"stmts"`
	Func      string `json:"func"`
	Covered   bool   `json:"-"`
}

func (b Block) key() string {
	return fmt.Sprintf("%s:%d.%d,%d.%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
}

// Pkg is the import path of the block's package.
func (b Block) Pkg() string { return path.Dir(b.File) }

func main() {
	asJSON := flag.Bool("json", false, "print the uncovered blocks as JSON")
	summary := flag.Bool("summary", false, "print per-package totals instead of blocks")
	src := flag.Bool("src", false, "print each uncovered block's source lines")
	root := flag.String("root", ".", "the repo root (holds go.mod and gui/go.mod)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: covblocks [-json] [-summary] [-src] PROFILE...")
		os.Exit(2)
	}
	var readers []io.Reader
	for _, p := range flag.Args() {
		f, err := os.Open(p)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		readers = append(readers, f)
	}
	blocks, err := Merge(readers...)
	if err != nil {
		fatal(err)
	}
	mods, err := Modules(*root)
	if err != nil {
		fatal(err)
	}
	if *summary {
		WriteSummary(os.Stdout, blocks)
		return
	}
	unc := Uncovered(blocks)
	if err := NameFuncs(unc, mods); err != nil {
		fatal(err)
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(unc); err != nil {
			fatal(err)
		}
	default:
		for _, b := range unc {
			fmt.Printf("%s:%d-%d\t%s\t%d\n", b.File, b.StartLine, b.EndLine, b.Func, b.Stmts)
			if *src {
				printSrc(os.Stdout, b, mods)
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "covblocks:", err)
	os.Exit(1)
}

// Merge reads coverage profiles and returns every block once, sorted by
// file and position; a block is covered if any profile counted it.
func Merge(rs ...io.Reader) ([]Block, error) {
	byKey := map[string]*Block{}
	for _, r := range rs {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "mode:") {
				continue
			}
			b, count, err := parseLine(line)
			if err != nil {
				return nil, err
			}
			k := b.key()
			if old, ok := byKey[k]; ok {
				old.Covered = old.Covered || count > 0
				continue
			}
			b.Covered = count > 0
			byKey[k] = &b
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Block, 0, len(byKey))
	for _, b := range byKey {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.StartCol < b.StartCol
	})
	return out, nil
}

// parseLine parses `file.go:a.b,c.d stmts count`.
func parseLine(line string) (Block, int64, error) {
	bad := func() (Block, int64, error) { return Block{}, 0, fmt.Errorf("bad profile line %q", line) }
	f := strings.Fields(line)
	if len(f) != 3 {
		return bad()
	}
	colon := strings.LastIndex(f[0], ":")
	if colon < 0 {
		return bad()
	}
	var b Block
	b.File = f[0][:colon]
	if _, err := fmt.Sscanf(f[0][colon+1:], "%d.%d,%d.%d", &b.StartLine, &b.StartCol, &b.EndLine, &b.EndCol); err != nil {
		return bad()
	}
	stmts, err1 := strconv.Atoi(f[1])
	count, err2 := strconv.ParseInt(f[2], 10, 64)
	if err1 != nil || err2 != nil {
		return bad()
	}
	b.Stmts = stmts
	return b, count, nil
}

// Uncovered returns the blocks with no count that hold statements.
func Uncovered(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if !b.Covered && b.Stmts > 0 {
			out = append(out, b)
		}
	}
	return out
}

// Total is the statement count and the covered part of blocks.
func Total(blocks []Block) (stmts, covered int) {
	for _, b := range blocks {
		stmts += b.Stmts
		if b.Covered {
			covered += b.Stmts
		}
	}
	return stmts, covered
}

// Pct is covered/stmts in percent, truncated to one decimal like check.sh.
func Pct(stmts, covered int) string {
	if stmts == 0 {
		return "none"
	}
	return fmt.Sprintf("%.1f", float64(int(1000*int64(covered)/int64(stmts)))/10)
}

// WriteSummary prints per-package totals and the overall total.
func WriteSummary(w io.Writer, blocks []Block) {
	byPkg := map[string][]Block{}
	var pkgs []string
	for _, b := range blocks {
		if _, ok := byPkg[b.Pkg()]; !ok {
			pkgs = append(pkgs, b.Pkg())
		}
		byPkg[b.Pkg()] = append(byPkg[b.Pkg()], b)
	}
	sort.Strings(pkgs)
	fmt.Fprintf(w, "%-60s %6s %7s %9s %6s\n", "package", "stmts", "covered", "uncovered", "pct")
	for _, p := range pkgs {
		s, c := Total(byPkg[p])
		fmt.Fprintf(w, "%-60s %6d %7d %9d %6s\n", p, s, c, s-c, Pct(s, c))
	}
	s, c := Total(blocks)
	fmt.Fprintf(w, "%-60s %6d %7d %9d %6s\n", "total", s, c, s-c, Pct(s, c))
}

// Module maps an import-path prefix to a directory on disk.
type Module struct{ Path, Dir string }

// Modules reads the module paths of root/go.mod and root/gui/go.mod,
// longest first so the GUI module wins over the root for its own files.
func Modules(root string) ([]Module, error) {
	var mods []Module
	for _, dir := range []string{root, filepath.Join(root, "gui")} {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			if os.IsNotExist(err) && dir != root {
				continue
			}
			return nil, err
		}
		for _, l := range strings.Split(string(b), "\n") {
			if p, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
				mods = append(mods, Module{Path: strings.TrimSpace(p), Dir: dir})
				break
			}
		}
	}
	sort.Slice(mods, func(i, j int) bool { return len(mods[i].Path) > len(mods[j].Path) })
	return mods, nil
}

// DiskPath maps a profile file name to its file on disk ("" if no module
// holds it).
func DiskPath(file string, mods []Module) string {
	for _, m := range mods {
		if rest, ok := strings.CutPrefix(file, m.Path+"/"); ok {
			return filepath.Join(m.Dir, filepath.FromSlash(rest))
		}
	}
	return ""
}

// NameFuncs fills in each block's Func: the enclosing function or method
// ("Recv.Name"), "Outer.func" for a block in a closure, "<package>" for one
// outside every function, "?" when the source can't be read.
func NameFuncs(blocks []Block, mods []Module) error {
	type fn struct {
		name       string
		start, end int
		lits       [][2]int
	}
	cache := map[string][]fn{}
	for i := range blocks {
		b := &blocks[i]
		fns, ok := cache[b.File]
		if !ok {
			p := DiskPath(b.File, mods)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				cache[b.File] = nil
				b.Func = "?"
				continue
			}
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					name = recvName(fd.Recv.List[0].Type) + "." + name
				}
				x := fn{name: name, start: fset.Position(fd.Pos()).Line, end: fset.Position(fd.End()).Line}
				ast.Inspect(fd, func(n ast.Node) bool {
					if fl, ok := n.(*ast.FuncLit); ok {
						x.lits = append(x.lits, [2]int{fset.Position(fl.Pos()).Line, fset.Position(fl.End()).Line})
					}
					return true
				})
				fns = append(fns, x)
			}
			cache[b.File] = fns
		}
		b.Func = "<package>"
		for _, f := range fns {
			if b.StartLine >= f.start && b.EndLine <= f.end {
				b.Func = f.name
				for _, l := range f.lits {
					if b.StartLine >= l[0] && b.EndLine <= l[1] && !(b.StartLine == f.start) {
						b.Func = f.name + ".func"
						break
					}
				}
				break
			}
		}
	}
	return nil
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func printSrc(w io.Writer, b Block, mods []Module) {
	data, err := os.ReadFile(DiskPath(b.File, mods))
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for n := b.StartLine; n <= b.EndLine && n <= len(lines); n++ {
		fmt.Fprintf(w, "    %5d  %s\n", n, lines[n-1])
	}
}
