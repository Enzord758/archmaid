package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// shimPacmanLog installs a fake pacman that logs its argv to a file and
// then runs body; it returns the log's path.
func shimPacmanLog(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + body
	if err := os.WriteFile(filepath.Join(dir, "pacman"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestOrphanNamesCheckedBeforeSudo: a name with an escape, a space or a
// leading '-' in pacman -Qdtq's output fails the probe; no removal step is
// inserted, so nothing reaches `sudo pacman -Rns`, and no preview runs.
func TestOrphanNamesCheckedBeforeSudo(t *testing.T) {
	for name, out := range map[string]string{
		"escape":  `printf 'libok\n\033[31mevil\033[0m\n'`,
		"space":   `printf 'libok\nevil --noconfirm\n'`,
		"dash":    `printf -- '--overwrite\n'`,
		"control": `printf 'a\001b\n'`,
	} {
		t.Run(name, func(t *testing.T) {
			log := shimPacmanLog(t, out+"\n")
			res := probe(t, testEnv(false)).Run(context.Background(), steps.Env{})
			if res.Err == nil || res.InsertNext != nil {
				t.Fatalf("result %+v, want an error and nothing inserted", res)
			}
			if !strings.Contains(res.Output, "not a valid package name") || strings.ContainsRune(res.Output, '\x1b') {
				t.Fatalf("output %q, want the refusal with the name quoted", res.Output)
			}
			b, _ := os.ReadFile(log)
			if strings.Contains(string(b), "-Rs") {
				t.Fatalf("the preview ran with a bad name: %s", b)
			}
		})
	}
}

// TestParsedPacmanIsColourless: every user-side pacman whose output is
// parsed is asked for --color never.
func TestParsedPacmanIsColourless(t *testing.T) {
	log := shimPacmanLog(t, "case \"$1\" in\n-Qdtq) echo liba ;;\n-Qq) echo liba ;;\nesac\n")
	ctx := context.Background()
	if _, err := orphanPackages(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := orphanRemovalPreview(ctx, []string{"liba"}); err != nil {
		t.Fatal(err)
	}
	if _, err := brokenCandidates(ctx, 1); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		"-Qdtq --color never",
		"-Rs --print --color never -- liba",
		"-Qq --color never",
		"-Qkq --color never -- liba",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("pacman calls %q, want %q", got, want)
	}
}

// TestBrokenNamesCheckedBeforeSudo: a flagged package whose name is not a
// package name fails the user scan, and the root recheck's result likewise,
// so no such name reaches `sudo -n pacman -Qkq` or the reinstall.
func TestBrokenNamesCheckedBeforeSudo(t *testing.T) {
	shimPacmanLog(t, "case \"$1\" in\n-Qq) echo liba ;;\n-Qkq) printf '\\033[1mevil /usr/x\\n'; exit 1 ;;\nesac\n")
	if _, err := brokenCandidates(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "not a valid package name") {
		t.Fatalf("brokenCandidates err = %v, want the refusal", err)
	}
	sudo := t.TempDir()
	if err := os.WriteFile(filepath.Join(sudo, "sudo"), []byte("#!/bin/sh\nprintf 'evil;rm /usr/x\\n'; exit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sudo+string(os.PathListSeparator)+os.Getenv("PATH"))
	if pkgs, _, err := brokenPackages(context.Background(), []string{"liba"}); err == nil || pkgs != nil {
		t.Fatalf("brokenPackages = %v, %v, want the refusal", pkgs, err)
	}
}

func TestPackageNameOK(t *testing.T) {
	for _, ok := range []string{"a", "libfoo", "gtk4", "c++", "python-foo_bar.baz", "Foo", "@x"} {
		if !PackageNameOK(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a\x1b[0m", "a\n", ".x", strings.Repeat("a", 129)} {
		if PackageNameOK(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
