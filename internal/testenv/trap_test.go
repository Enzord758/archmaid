package testenv

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestMain runs this package under its own guard, like every other.
func TestMain(m *testing.M) { os.Exit(Run(m)) }

// trapChildEnv marks the re-exec'd half of TestTrapCatchesRealSu: a test
// binary that deliberately reaches the trapped names via PATH, under its own
// Run, so the failure it must end in is not this process's.
const trapChildEnv = "ARCHMAID_TESTENV_TRAP_CHILD"

// TestTrapChild is the child half: it runs every trapped name from PATH and
// checks each was the trap. It passes, and the child's Run must still fail
// the package. Skipped unless re-exec'd.
func TestTrapChild(t *testing.T) {
	if os.Getenv(trapChildEnv) != "1" {
		t.Skip("child half of TestTrapCatchesRealSu")
	}
	for _, name := range Trapped {
		// Belt and braces: never exec a name unless PATH resolves it to a
		// trap. If Run's PATH were ever broken, this fails instead of
		// reaching the real binary.
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("%s: not on PATH: %v", name, err)
		}
		if b, err := os.ReadFile(path); err != nil || !bytes.Contains(b, []byte("archmaid testenv trap")) {
			t.Fatalf("%s resolves to %s, not a trap: refusing to run it", name, path)
		}
		// --version is harmless even for the real binaries.
		cmd := exec.Command(name, "--version")
		var exit *exec.ExitError
		if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != TrapExit {
			t.Errorf("%s --version: %v, want exit %d from the trap", name, err, TrapExit)
		}
	}
}

// bothChildEnv marks the re-exec'd half of TestRunReportsTrapAndStateChange;
// its value is the (scratch) state home the child's Run guards as "real".
const bothChildEnv = "ARCHMAID_TESTENV_BOTH_CHILD"

// TestBothChild hits one trap and creates the guarded state dir. Skipped
// unless re-exec'd. The state home is the parent's scratch dir, never the
// owner's real one.
func TestBothChild(t *testing.T) {
	if os.Getenv(bothChildEnv) == "" {
		t.Skip("child half of TestRunReportsTrapAndStateChange")
	}
	path, err := exec.LookPath("su")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Contains(b, []byte("archmaid testenv trap")) {
		t.Fatalf("su resolves to %s, not a trap: refusing to run it", path)
	}
	_ = exec.Command("su", "--version").Run()
	if err := os.MkdirAll(filepath.Join(os.Getenv(bothChildEnv), "archmaid"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// TestRunReportsTrapAndStateChange: a test that both hits a trap and changes
// the state dir gets both reports, not just the first.
func TestRunReportsTrapAndStateChange(t *testing.T) {
	state := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBothChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), bothChildEnv+"="+state, "XDG_STATE_HOME="+state)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("child: %v, want exit 1\nstderr:\n%s", err, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"a test reached a real privileged binary", "   su --version\n", "the tests changed the real state dir"} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q:\n%s", want, out)
		}
	}
}

// TestTrapCatchesRealSu proves the guard: a test that execs su (and the other
// trapped names) through PATH fails its package, with the calls on stderr.
func TestTrapCatchesRealSu(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestTrapChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), trapChildEnv+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("child: %v, want exit 1\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS") {
		t.Errorf("the child's test itself should pass (only Run fails):\n%s", stdout.String())
	}
	out := stderr.String()
	if !strings.Contains(out, "a test reached a real privileged binary") {
		t.Errorf("no trap report on stderr:\n%s", out)
	}
	for _, name := range Trapped {
		if !strings.Contains(out, "   "+name+" --version\n") {
			t.Errorf("trap report misses %q:\n%s", name+" --version", out)
		}
	}
}

// TestShimAheadOfTrapWins: a test's own shim, first on PATH, runs instead of
// the trap, so the deliberate shims keep working. (If the trap ran, this
// package's Run would fail.)
func TestShimAheadOfTrapWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var exit *exec.ExitError
	if err := exec.Command("su", "-s", "/usr/bin/true", "nobody").Run(); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("su: %v, want the shim's exit 7", err)
	}
}

// TestInstallTrapsLogsArgv: a trap records its argv, keeps nothing from stdin
// and exits TrapExit.
func TestInstallTrapsLogsArgv(t *testing.T) {
	bin, log, err := InstallTraps(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := TrapCalls(log); got != nil {
		t.Fatalf("fresh log has calls: %q", got)
	}
	cmd := exec.Command(filepath.Join(bin, "sudo"), "-S", "-v", "it's")
	cmd.Stdin = strings.NewReader("hunter2\n")
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != TrapExit {
		t.Fatalf("trap: %v, want exit %d", err, TrapExit)
	}
	got := TrapCalls(log)
	if len(got) != 1 || got[0] != "sudo -S -v it's" {
		t.Fatalf("calls = %q", got)
	}
	if b, _ := os.ReadFile(log); bytes.Contains(b, []byte("hunter2")) {
		t.Fatal("the trap stored its stdin")
	}
	if got := TrapCalls(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing log: %q", got)
	}
}

// absoluteAllowed are the absolute paths of a trapped binary that may appear
// as string literals, and why: none of them is ever executed.
var absoluteAllowed = map[string]string{
	"internal/privilege/askpass.go:/usr/bin/sudo":   "sudoPath: compared against the askpass peer's parent, never run",
	"internal/proc/sudo_test.go:/usr/bin/sudo":      "CheckSudo's table: classified, never run",
	"internal/driver/askpass_test.go:/usr/bin/sudo": "a fake /proc entry's Exe for Request.Verify, never run",
	// The GUI module's harness: the sudo shim's Exe in its fake /proc view.
	"gui/internal/ui/realdriver_harness_test.go:/usr/bin/sudo": "the sudo shim's Exe in a fake /proc view for Request.Verify, never run",
}

// TestNoAbsolutePrivilegedExec: the traps work through PATH, so no Go file in
// the module may name a trapped binary by absolute path (which would bypass
// them), except the allow-listed literals that are never executed.
func TestNoAbsolutePrivilegedExec(t *testing.T) {
	root, files := moduleGoFiles(t)
	for _, rel := range files {
		lits, err := absoluteTrapped(filepath.Join(root, rel), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range lits {
			if _, ok := absoluteAllowed[filepath.ToSlash(rel)+":"+s]; !ok {
				t.Errorf("%s: %q names a trapped binary by absolute path, which bypasses the PATH traps; exec it by name", rel, s)
			}
		}
	}
}

// TestEveryTestPackageRunsUnderTestenv: the traps only guard a package whose
// TestMain calls Run, so every package with tests must have one that does
// (merged with any dispatch it needs first, like --askpass).
func TestEveryTestPackageRunsUnderTestenv(t *testing.T) {
	root, files := moduleGoFiles(t)
	wired := map[string]bool{}
	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		dir := filepath.Dir(rel)
		if _, ok := wired[dir]; !ok {
			wired[dir] = false
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("func TestMain(")) &&
			(bytes.Contains(b, []byte("testenv.Run(m)")) || dir == filepath.Join("internal", "testenv")) {
			wired[dir] = true
		}
	}
	for dir, ok := range wired {
		if !ok {
			t.Errorf("%s: no TestMain calling testenv.Run(m); add one (see internal/testenv)", dir)
		}
	}
	if len(wired) < 5 {
		t.Fatalf("only %d test packages found: walked the wrong tree?", len(wired))
	}
}

// moduleGoFiles returns the module root and every .go file under it, relative
// to it, skipping hidden dirs (.git, .claude worktrees) and testdata.
func moduleGoFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not at %s: %v", root, err)
	}
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
		if strings.HasSuffix(path, ".go") {
			rel, _ := filepath.Rel(root, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("only %d Go files under %s: walked the wrong tree?", len(files), root)
	}
	return root, files
}

// TestAbsoluteTrappedFindsLiterals: the scan behind
// TestNoAbsolutePrivilegedExec sees what it must and nothing else.
func TestAbsoluteTrappedFindsLiterals(t *testing.T) {
	src := "package x\n" +
		"import \"os/exec\"\n" +
		"const p = `/usr/bin/pkexec`\n" +
		"func f() {\n" +
		"\texec.Command(\"/usr/bin/su\", \"-s\", \"/usr/bin/true\")\n" +
		"\texec.Command(\"/sbin/runuser\")\n" +
		"\texec.Command(\"sudo\", \"-n\")\n" +
		"\t_ = \"/home/u/bin/sudo\"\n" +
		"\t_ = \"/usr/bin/sudoku\"\n" +
		"}\n"
	got, err := absoluteTrapped("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	// Built, not literal, so the module scan doesn't flag this file.
	want := []string{"/usr/bin/" + "pkexec", "/usr/bin/" + "su", "/sbin/" + "runuser"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// absoluteTrapped parses a Go file (src as for parser.ParseFile) and returns
// its string literals that name a trapped binary in a system directory.
func absoluteTrapped(path string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err == nil && systemPath(s) && slices.Contains(Trapped, filepath.Base(s)) {
			out = append(out, s)
		}
		return true
	})
	return out, nil
}

// systemPath reports whether s is an absolute path where a real binary could
// live (a made-up one like a fake process table's /home/u/bin/sudo is not).
func systemPath(s string) bool {
	for _, dir := range []string{"/usr/", "/bin/", "/sbin/", "/run/", "/opt/", "/etc/"} {
		if strings.HasPrefix(s, dir) {
			return true
		}
	}
	return false
}
