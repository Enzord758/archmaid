package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// TestMerge: a block is covered if any profile counted it, and is listed
// once; totals are check.sh's (truncated to one decimal).
func TestMerge(t *testing.T) {
	a := "mode: atomic\nm/p/a.go:1.1,2.2 2 0\nm/p/a.go:3.1,4.2 1 0\nm/q/b.go:1.1,1.9 3 5\n"
	b := "mode: atomic\nm/p/a.go:1.1,2.2 2 1\n"
	blocks, err := Merge(strings.NewReader(a), strings.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3: %+v", len(blocks), blocks)
	}
	s, c := Total(blocks)
	if s != 6 || c != 5 || Pct(s, c) != "83.3" {
		t.Errorf("total %d/%d = %s, want 5/6 = 83.3", c, s, Pct(s, c))
	}
	unc := Uncovered(blocks)
	if len(unc) != 1 || unc[0].File != "m/p/a.go" || unc[0].StartLine != 3 {
		t.Errorf("uncovered = %+v, want m/p/a.go:3", unc)
	}
	if _, err := Merge(strings.NewReader("m/p/a.go 1 2\n")); err == nil {
		t.Error("a malformed line was accepted")
	}
}

// TestNameFuncs: blocks are named after their function, method or closure.
func TestNameFuncs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "package p\n\ntype T struct{}\n\nfunc (t *T) M() {\n\tf := func() {\n\t\t_ = 1\n\t}\n\tf()\n}\n\nfunc F() {\n\t_ = 2\n}\n"
	if err := os.Mkdir(filepath.Join(root, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "p", "a.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	mods, err := Modules(root)
	if err != nil {
		t.Fatal(err)
	}
	blocks := []Block{
		{File: "m/p/a.go", StartLine: 7, EndLine: 7},
		{File: "m/p/a.go", StartLine: 9, EndLine: 9},
		{File: "m/p/a.go", StartLine: 13, EndLine: 13},
		{File: "m/p/a.go", StartLine: 3, EndLine: 3},
	}
	if err := NameFuncs(blocks, mods); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"T.M.func", "T.M", "F", "<package>"} {
		if blocks[i].Func != want {
			t.Errorf("block at line %d: Func = %q, want %q", blocks[i].StartLine, blocks[i].Func, want)
		}
	}
}
