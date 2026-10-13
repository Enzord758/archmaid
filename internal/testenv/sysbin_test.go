package testenv

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// sysbinFile holds internal/sysbin's paths, the only literals of them.
var sysbinFile = filepath.Join("internal", "sysbin", "sysbin.go")

// sysbinDefaults are internal/sysbin's variables and their defaults, read
// from its source (under testenv the variables are bare names), in All's
// order.
func sysbinDefaults(t *testing.T) (names, paths []string) {
	t.Helper()
	root, _ := moduleGoFiles(t)
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, sysbinFile), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: %s is not a string literal", sysbinFile, n.Name)
				}
				v, _ := strconv.Unquote(lit.Value)
				names = append(names, n.Name)
				paths = append(paths, v)
			}
		}
	}
	if len(paths) != len(sysbin.All()) {
		t.Fatalf("%s: %d variables, All has %d: keep them in step", sysbinFile, len(paths), len(sysbin.All()))
	}
	return names, paths
}

// TestSysbinDefaultsAbsolute: in the product, every internal/sysbin path is
// the program's absolute /usr/bin path (the only place each lives on
// Arch), never a bare name that PATH would decide; and All lists them in
// the declared order (BareSysbin and the e2e build rely on it).
func TestSysbinDefaultsAbsolute(t *testing.T) {
	names, paths := sysbinDefaults(t)
	for i, p := range paths {
		if want := "/usr/bin/" + filepath.Base(p); p != want {
			t.Errorf("sysbin.%s = %q, want an absolute %s", names[i], p, want)
		}
		if got := *sysbin.All()[i]; got != filepath.Base(p) {
			t.Errorf("sysbin.All()[%d] = %q under testenv, want %s's bare name %q", i, got, names[i], filepath.Base(p))
		}
	}
	for _, want := range []string{"su", "sudo", "pkexec", "pkttyagent", "topgrade"} {
		if !slices.ContainsFunc(paths, func(p string) bool { return filepath.Base(p) == want }) {
			t.Errorf("internal/sysbin has no path for %s", want)
		}
	}
}

// sysbinChildEnv marks the re-exec'd half of TestSysbinBareInChild.
const sysbinChildEnv = "ARCHMAID_TESTENV_SYSBIN_CHILD"

// TestSysbinChild is the child: a re-exec'd test binary that never calls
// Run (as the GUI harness's windows and the askpass relays don't) still
// has every sysbin path bare, so PATH's traps and shims decide.
func TestSysbinChild(t *testing.T) {
	if os.Getenv(sysbinChildEnv) == "" {
		t.Skip("child half of TestSysbinBareInChild")
	}
	for _, p := range sysbin.All() {
		if strings.Contains(*p, "/") {
			t.Errorf("%s is not bare in a re-exec'd child", *p)
		}
	}
}

// TestSysbinBareInChild: see TestSysbinChild. The child's TestMain does
// call Run here (this package's), so it also proves Run's own check
// passes; the init is what a child without Run relies on, and that is all
// the child test reads.
func TestSysbinBareInChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run", "^TestSysbinChild$", "-test.v")
	cmd.Env = append(os.Environ(), sysbinChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestSysbinChild") {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// TestRunRefusesAbsoluteSysbin: Run's check fails a binary whose sysbin
// paths are absolute (the init gone, or a test that set one and forgot to
// put it back before Run).
func TestRunRefusesAbsoluteSysbin(t *testing.T) {
	if err := checkSysbinBare(); err != nil {
		t.Fatalf("under testenv: %v", err)
	}
	old := sysbin.Sudo
	sysbin.Sudo = "/usr/bin/" + "sudo"
	defer func() { sysbin.Sudo = old }()
	if err := checkSysbinBare(); err == nil {
		t.Fatal("an absolute sysbin path passed the check")
	}
}

// TestUseSysbinAndDecoys: UseSysbin's shim runs from outside PATH and is
// put back; a Decoy logs its name and argv and fails.
func TestUseSysbinAndDecoys(t *testing.T) {
	log := Decoys(t, "sudo")
	var path string
	t.Run("use", func(t *testing.T) {
		path = UseSysbin(t, &sysbin.Sudo, "exit 3")
		if sysbin.Sudo != path || !filepath.IsAbs(path) || filepath.Base(path) != "sudo" {
			t.Fatalf("sysbin.Sudo = %q, UseSysbin = %q", sysbin.Sudo, path)
		}
		var exit *exec.ExitError
		if err := exec.Command(sysbin.Sudo).Run(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("shim: %v, want exit 3", err)
		}
	})
	if sysbin.Sudo != "sudo" {
		t.Fatalf("sysbin.Sudo = %q after the test, want it put back", sysbin.Sudo)
	}
	var exit *exec.ExitError
	if err := exec.Command("sudo", "-n", "-v").Run(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("decoy: %v, want exit 1", err)
	}
	if got := TrapCalls(log); !slices.Equal(got, []string{"sudo -n -v"}) {
		t.Fatalf("decoy log %q", got)
	}
}

// bareNames are the programs that grant privilege, or that archmaid runs
// to get it (topgrade): no product code may start one by bare name.
var bareNames = []string{"su", "sudo", "sudo-rs", "pkexec", "pkttyagent", "runuser", "doas", "run0", "please", "topgrade"}

// bareAllowed are the bare-name call arguments that run nothing, and why.
var bareAllowed = map[string]string{
	`gui/internal/ui/overview.go:privTag("sudo")`: "a plan row's privilege tag, a label; runs nothing",
}

// TestNoBarePrivilegedExec: no product Go file passes a privileged
// program's bare name as the first or second argument of a call (exec's
// Command, CommandContext and LookPath, proc.Query and any wrapper of
// them), nor as a proc.Spec's or exec.Cmd's program: archmaid runs each
// from internal/sysbin, by absolute path. A step's argv (sudo …, topgrade
// …) keeps its names: privilege.Sudo.Command runs it from sysbin
// (its TestCommandRunsSysbin).
func TestNoBarePrivilegedExec(t *testing.T) {
	root, files := moduleGoFiles(t)
	for _, rel := range files {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		hits, err := barePrivilegedCalls(filepath.Join(root, rel), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if _, ok := bareAllowed[filepath.ToSlash(rel)+":"+h]; !ok {
				t.Errorf("%s: %s runs a privileged program by its bare name, which PATH decides; use internal/sysbin", rel, h)
			}
		}
	}
}

// TestBarePrivilegedCallsFinds: the scan behind TestNoBarePrivilegedExec
// sees what it must and nothing else.
func TestBarePrivilegedCallsFinds(t *testing.T) {
	src := "package x\n" +
		"func f() {\n" +
		"\texec.CommandContext(ctx, \"su\", \"-s\", \"/usr/bin/true\")\n" +
		"\texec.Command(\"sudo\", \"-n\", \"-v\")\n" +
		"\texec.LookPath(\"pkttyagent\")\n" +
		"\tqkqOutput(ctx, \"sudo\", \"-n\")\n" +
		"\t_ = proc.Spec{Name: \"topgrade\"}\n" +
		"\t_ = exec.Cmd{Path: \"pkexec\"}\n" +
		"\texec.Command(sysbin.Sudo, \"-n\", \"-v\")\n" +
		"\t_ = []string{\"sudo\", \"paccache\", \"-r\"}\n" +
		"\tfmt.Println(\"x\", \"y\", \"sudo\")\n" +
		"\t_ = name == \"sudo\"\n" +
		"}\n"
	got, err := barePrivilegedCalls("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`CommandContext(…, "su")`, `Command("sudo")`, `LookPath("pkttyagent")`, `qkqOutput(…, "sudo")`, `Spec{Name: "topgrade"}`, `Cmd{Path: "pkexec"}`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// barePrivilegedCalls parses a Go file (src as for parser.ParseFile) and
// describes each call whose first or second argument, and each composite
// literal whose Name or Path field, is a bareNames literal.
func barePrivilegedCalls(path string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	bare := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(lit.Value)
		return s, err == nil && slices.Contains(bareNames, s)
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
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			for i, a := range n.Args {
				if i > 1 {
					break
				}
				if s, ok := bare(a); ok {
					pre := ""
					if i == 1 {
						pre = "…, "
					}
					out = append(out, name(n.Fun)+"("+pre+strconv.Quote(s)+")")
				}
			}
		case *ast.CompositeLit:
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "Name" || k.Name == "Path") {
					if s, ok := bare(kv.Value); ok {
						out = append(out, name(n.Type)+"{"+k.Name+": "+strconv.Quote(s)+"}")
					}
				}
			}
		}
		return true
	})
	return out, nil
}

// TestE2EBuildsBareSysbin: the e2e harness drives the real binary, whose
// sysbin paths are absolute, with its shims on PATH; it must build it with
// every one of them set back to its bare name, or a tape (the sudo probe,
// Repair's root recheck) would run the real /usr/bin program.
func TestE2EBuildsBareSysbin(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "archmaid", "e2e", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\nSYSBIN=github.com/Enzord758/archmaid/internal/sysbin\n") {
		t.Error("run.sh: no SYSBIN=github.com/Enzord758/archmaid/internal/sysbin")
	}
	names, paths := sysbinDefaults(t)
	for i, n := range names {
		want := "-X $SYSBIN." + n + "=" + filepath.Base(paths[i])
		if !strings.Contains(string(b), want) {
			t.Errorf("cmd/archmaid/e2e/run.sh doesn't build with %q", want)
		}
	}
	// Both builds (plain and cover) take the flags.
	if n := strings.Count(string(b), `-ldflags "$SYSBIN_LDFLAGS"`); n < 2 {
		t.Errorf("run.sh: %d builds use $SYSBIN_LDFLAGS, want both", n)
	}
}
