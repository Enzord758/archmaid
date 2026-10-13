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
// An entry that no longer matches a hit fails TestNoBarePrivilegedExec.
var bareAllowed = map[string]string{
	`gui/internal/ui/overview.go:privTag("sudo")`:              "a plan row's privilege tag, a label; runs nothing",
	`internal/helperclient/run.go:Errorf(…, Pkexec: "pkexec")`: "the display name in an error's text; Run executes sysbin.Pkexec",
}

// TestNoBarePrivilegedExec: no product Go file passes a privileged
// program's bare name as the first or second argument of a call (exec's
// Command, CommandContext and LookPath, proc.Query and any wrapper of
// them), nor as a proc.Spec's or exec.Cmd's program: archmaid runs each
// from internal/sysbin, by absolute path. The argument is read as the
// compiler reads it (go/types constant folding, bareScan), so a constant,
// a concatenation or a typed constant counts like a literal, and so does
// a variable given a bare name and a shell's -c script that runs one by
// name. A step's argv (sudo …, topgrade …) keeps its names:
// privilege.Sudo.Command runs it from sysbin (its TestCommandRunsSysbin).
func TestNoBarePrivilegedExec(t *testing.T) {
	root, files := moduleGoFiles(t)
	var product []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			product = append(product, rel)
		}
	}
	hits, err := bareModuleScan(root, product)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, h := range hits {
		key := h.file + ":" + h.desc
		if _, ok := bareAllowed[key]; ok {
			used[key] = true
			continue
		}
		t.Errorf("%s: %s runs a privileged program by its bare name, which PATH decides; use internal/sysbin", h.file, h.desc)
	}
	for key := range bareAllowed {
		if !used[key] {
			t.Errorf("bareAllowed: %s matches no call any more; remove it", key)
		}
	}
	for c := range bareConstAllowed {
		if !bareConstUsed[c] {
			t.Errorf("bareConstAllowed: %s is passed nowhere any more; remove it", c)
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

// TestBarePrivilegedCallsFolds (#136, review #134 N2): the scan reads the
// program argument as the compiler does (go/types constant folding), so a
// constant, a concatenation and a typed constant are caught like a
// literal; so are a variable given a bare name and a shell's -c script
// that runs one by name. A constant of a type of its own (doas, as
// logstore.Category's "topgrade") counts once converted to a string, the
// form exec takes; a conversion itself runs nothing.
func TestBarePrivilegedCallsFolds(t *testing.T) {
	src := "package x\n" +
		"const suName = \"su\"\n" +
		"const (\n\tpre = \"pk\"\n\tpkName = pre + \"exec\"\n)\n" +
		"const typedSudo string = \"sudo\"\n" +
		"type prog string\n" +
		"const doas prog = \"doas\"\n" +
		"func runProg(p prog) {}\n" +
		"var pkgSudo = \"sudo\"\n" +
		"func f() {\n" +
		"\texec.CommandContext(ctx, suName, \"-s\", \"/usr/bin/true\")\n" +
		"\texec.Command(\"su\" + \"do\", \"-n\")\n" +
		"\texec.LookPath(pkName)\n" +
		"\texec.Command(typedSudo, \"-v\")\n" +
		"\trunProg(doas)\n" +
		"\texec.Command(string(doas))\n" +
		"\t_ = proc.Spec{Name: suName}\n" +
		"\tname := \"run0\"\n" +
		"\texec.Command(name)\n" +
		"\tvar later string\n" +
		"\tlater = \"please\"\n" +
		"\texec.Command(later, \"x\")\n" +
		"\texec.Command(pkgSudo)\n" +
		"\texec.Command(\"sh\", \"-c\", \"sudo pacman -Syu\")\n" +
		"\texec.CommandContext(ctx, \"/bin/bash\", \"-ec\", \"cd /tmp && su -c true\")\n" +
		"\texec.Command(\"sh\", \"-c\", \"pseudo sudoku; echo su-do\")\n" +
		"\texec.Command(\"echo\", \"sudo pacman\")\n" +
		"\tok := \"paccache\"\n" +
		"\texec.Command(ok)\n" +
		"\texec.Command(sysbin.Sudo, \"-n\", \"-v\")\n" +
		"}\n"
	got, err := barePrivilegedCalls("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`CommandContext(…, suName: "su")`, `Command("sudo")`, `LookPath(pkName: "pkexec")`, `Command(typedSudo: "sudo")`,
		`Command("doas")`, `Spec{Name: suName: "su"}`, `Command(name: "run0")`, `Command(later: "please")`, `Command(pkgSudo: "sudo")`,
		`Command(sh -c "sudo pacman -Syu")`, `CommandContext(/bin/bash -ec "cd /tmp && su -c true")`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q,\nwant %q", got, want)
	}
}

// TestBareConstAllowedNotAsProgram (#140, review #139): the topgrade step's
// ID is exempt as a key, not as a program: passed to exec.Command, as
// proc.Spec's Name or exec.Cmd's Path it is flagged, while a compare and a
// log of it stay clean.
func TestBareConstAllowedNotAsProgram(t *testing.T) {
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
	write("internal/steps/steps.go", "package steps\nconst IDTopgrade = \"topgrade\"\n")
	write("internal/x/x.go", "package x\n"+
		"import \""+modulePath+"/internal/steps\"\n"+
		"func f(id string) {\n"+
		"\t_ = id == steps.IDTopgrade\n"+
		"\tlog.Println(\"step\", steps.IDTopgrade)\n"+
		"\texec.Command(steps.IDTopgrade)\n"+
		"\texec.CommandContext(ctx, steps.IDTopgrade, \"-y\")\n"+
		"\tsyscall.Exec(steps.IDTopgrade, nil, nil)\n"+
		"\t_ = proc.Spec{Name: steps.IDTopgrade}\n"+
		"\t_ = exec.Cmd{Path: steps.IDTopgrade}\n"+
		"}\n")
	hits, err := bareModuleScan(root, []string{"internal/steps/steps.go", "internal/x/x.go"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.desc)
	}
	want := []string{
		`Command(IDTopgrade: "topgrade")`, `CommandContext(…, IDTopgrade: "topgrade")`, `Exec(IDTopgrade: "topgrade")`,
		`Spec{Name: IDTopgrade: "topgrade"}`, `Cmd{Path: IDTopgrade: "topgrade"}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q,\nwant %q", got, want)
	}
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
