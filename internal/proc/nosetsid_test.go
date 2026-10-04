package proc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Steps must run as plain children of archmaid: same session, same process
// group. sudo keys its ticket to the tty *and* the session, so one primed
// ticket covers every step — and everything topgrade and yay spawn — only
// while they share archmaid's session. Per-child Setsid once gave every
// internal sudo its own ticket (a password popup per sudo call during
// topgrade); a shared-pty attempt froze topgrade. Both were reverted. See
// docs/HANDOVER.md, "Steps run as plain children — never add Setsid".
//
// So TestNoSetsid checks both the behaviour (a started child shares our
// session and group) and the source (nothing in the TUI's code sets
// SysProcAttr's Setsid, Setpgid or Setctty). The root helper,
// cmd/archmaid-helper, is exempt: it runs as root outside the TUI's session
// and detaches its own children on purpose.

// sessionFields are the SysProcAttr fields that move a child out of our
// session or process group, or give it a controlling terminal of its own.
var sessionFields = map[string]bool{"Setsid": true, "Setpgid": true, "Setctty": true}

// guardExempt are the directories (relative to the repo root) the source
// guard skips.
var guardExempt = map[string]bool{"cmd/archmaid-helper": true}

func TestNoSetsid(t *testing.T) {
	t.Run("child shares our session", func(t *testing.T) {
		ch, quit := make(chan Msg, 1), make(chan struct{})
		defer close(quit)
		c := New(Options{KillGrace: time.Second}).Start(Spec{Name: "sleep", Args: []string{"30"}}, ch, quit)
		if c == nil {
			t.Fatal("did not start")
		}
		defer c.Stop()
		self, err := Read(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		kid, err := Read(c.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if kid.Session != self.Session || kid.Pgrp != self.Pgrp {
			t.Fatalf("child session %d, group %d; ours %d, %d — a step must stay in archmaid's session",
				kid.Session, kid.Pgrp, self.Session, self.Pgrp)
		}
	})

	t.Run("source sets no session fields", func(t *testing.T) {
		root := repoRoot(t)
		var files int
		for _, dir := range []string{"cmd", "internal"} {
			err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(root, path)
				rel = filepath.ToSlash(rel)
				if d.IsDir() {
					if guardExempt[rel] || d.Name() == "testdata" {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				src, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files++
				for _, f := range sessionFieldUses(t, rel, src) {
					t.Errorf("%s: sets %s — steps must share archmaid's session (see docs/HANDOVER.md)", f.pos, f.field)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if files == 0 {
			t.Fatalf("no Go files found under %s/{cmd,internal}", root)
		}
	})

	t.Run("guard catches each form", func(t *testing.T) {
		src := `package x
import "syscall"
func a(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	c.SysProcAttr.Setctty = true
	attr := syscall.SysProcAttr{}
	attr.Setsid = true
	_ = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} // fine
}`
		got := sessionFieldUses(t, "x.go", []byte(src))
		var fields []string
		for _, f := range got {
			fields = append(fields, f.field)
		}
		if strings.Join(fields, ",") != "Setsid,Setpgid,Setctty,Setsid" {
			t.Fatalf("guard found %v, want Setsid, Setpgid, Setctty, Setsid", fields)
		}
	})
}

type sessionFieldUse struct {
	pos   token.Position
	field string
}

// sessionFieldUses finds every SysProcAttr literal that sets a session field
// and every selector naming one (attr.Setsid = …, syscall.Setsid()).
func sessionFieldUses(t *testing.T, name string, src []byte) []sessionFieldUse {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var uses []sessionFieldUse
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if typeName(n.Type) != "SysProcAttr" {
				return true
			}
			for _, elt := range n.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && sessionFields[key.Name] {
						uses = append(uses, sessionFieldUse{fset.Position(key.Pos()), key.Name})
					}
				}
			}
		case *ast.SelectorExpr:
			if sessionFields[n.Sel.Name] {
				uses = append(uses, sessionFieldUse{fset.Position(n.Sel.Pos()), n.Sel.Name})
			}
		}
		return true
	})
	return uses
}

// typeName is the bare name of a (possibly package-qualified) type.
func typeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// repoRoot is the directory holding go.mod, found from the test's working
// directory (the package's own).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's directory")
		}
		dir = parent
	}
}
