package pkgdb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// fixture copies the checked-in database tree (core.db gzip, extra.db zstd,
// local/) to a scratch dir, so a test may change it.
func fixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "testdata/db"
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// openDir opens a dbpath laid out like /var/lib/pacman. With an order it
// writes a pacman.conf naming exactly those repos; without one the conf is
// left out and every sync/*.db is read.
func openDir(dir string, order []string) (*DB, error) {
	p := Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local")}
	if order != nil {
		p.Conf = filepath.Join(dir, "pacman.conf")
		var b strings.Builder
		b.WriteString("[options]\nHoldPkg = pacman\n")
		for _, r := range order {
			b.WriteString("\n[" + r + "]\nInclude = /etc/pacman.d/mirrorlist\n")
		}
		if err := os.WriteFile(p.Conf, []byte(b.String()), 0o644); err != nil {
			return nil, err
		}
	}
	return Open(p)
}

func openFixture(t *testing.T, order ...string) *DB {
	t.Helper()
	if order == nil {
		order = []string{"core", "extra"}
	}
	db, err := openDir(fixture(t), order)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

type tent struct{ dir, desc string }

func tarBytes(t testing.TB, ents []tent) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range ents {
		if err := tw.WriteHeader(&tar.Header{Name: e.dir + "/desc", Mode: 0o644, Size: int64(len(e.desc)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.desc))
	}
	tw.Close()
	return b.Bytes()
}

func gz(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

func zst(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w, _ := zstd.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

func desc(name, ver, rest string) string {
	return fmt.Sprintf("%%NAME%%\n%s\n\n%%VERSION%%\n%s\n\n%s", name, ver, rest)
}

// syncOnly writes one gzip sync DB and returns the dbpath (no local dir).
func syncOnly(t testing.TB, repo string, raw []byte) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sync"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "sync", repo+".db"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func names(ps []Package) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// ── opening ──────────────────────────────────────────────────────────────────

// TestOpenReadsGzipAndZstd: core.db is gzip, extra.db zstd; the magic bytes
// pick the decoder and both load.
func TestOpenReadsGzipAndZstd(t *testing.T) {
	db := openFixture(t)
	eq(t, "repos", db.Repos(), []string{"core", "extra"})
	eq(t, "problems", db.Warnings(), []string(nil))
	if g, ok := db.Lookup("glibc"); !ok || g.Repo != "core" {
		t.Fatalf("glibc = %+v, %v (gzip repo)", g, ok)
	}
	if g, ok := db.Lookup("git"); !ok || g.Repo != "extra" {
		t.Fatalf("git = %+v, %v (zstd repo)", g, ok)
	}
}

func TestOpenPlainTar(t *testing.T) {
	raw := tarBytes(t, []tent{{"x-1", desc("x", "1-1", "")}})
	db, err := openDir(syncOnly(t, "plain", raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Lookup("x"); !ok {
		t.Fatal("plain tar not read")
	}
}

// TestRepoOrder: the first repo in pacman.conf order wins a name; the others
// are "also in". Reversing the order flips the winner.
func TestRepoOrder(t *testing.T) {
	db := openFixture(t)
	v, _ := db.Lookup("vim")
	if v.Repo != "core" || v.Version != "9.1-1" || !reflect.DeepEqual(v.AlsoIn, []string{"extra"}) {
		t.Fatalf("vim = %s %s %v, want core 9.1-1 [extra]", v.Repo, v.Version, v.AlsoIn)
	}
	db = openFixture(t, "extra", "core")
	v, _ = db.Lookup("vim")
	if v.Repo != "extra" || v.Version != "9.1-2" || !reflect.DeepEqual(v.AlsoIn, []string{"core"}) {
		t.Fatalf("reordered vim = %s %s %v, want extra 9.1-2 [core]", v.Repo, v.Version, v.AlsoIn)
	}
}

// TestConfDecidesWhichReposLoad: pacman uses only the [sections] of
// pacman.conf. A repo with a file on disk but no section is ignored (a stale
// file); a section without a file is skipped and named in Warnings.
func TestConfDecidesWhichReposLoad(t *testing.T) {
	db := openFixture(t, "ghost", "extra")
	eq(t, "repos", db.Repos(), []string{"extra"})
	if g, _ := db.Lookup("glibc"); g.Repo != "" || !g.Foreign {
		t.Fatal("core has no section in pacman.conf but was loaded")
	}
	if w := db.Warnings(); len(w) != 1 || !strings.Contains(w[0], "ghost") {
		t.Fatalf("warnings = %q, want the missing ghost.db named", w)
	}
}

// TestConfParsing: comments, spaces, duplicate and bad section names, and an
// unreadable or missing pacman.conf (all sync files are read then, with a note).
func TestConfParsing(t *testing.T) {
	dir := fixture(t)
	conf := filepath.Join(dir, "pacman.conf")
	os.WriteFile(conf, []byte("# [core]\n[options]\n  [ extra ]  # second\n[core] # first\n[extra]\n[bad name]\n[]\nnot a section\n"), 0o644)
	got, err := repoNames(conf)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "repos", got, []string{"extra", "core"})
	os.WriteFile(conf, bytes.Repeat([]byte("#"), maxConfBytes+1), 0o644)
	if _, err := repoNames(conf); err == nil {
		t.Fatal("an oversized pacman.conf was read")
	}

	db, err := Open(Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "missing.conf")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "repos without conf", db.Repos(), []string{"core", "extra"})
	if w := db.Warnings(); len(w) != 1 || !strings.Contains(w[0], "pacman.conf") {
		t.Fatalf("warnings = %q", w)
	}
}

func dnames(ds []Dep) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Raw+"|"+d.Name+"|"+d.Desc)
	}
	return out
}

func TestParsedFields(t *testing.T) {
	db := openFixture(t)
	b, _ := db.Lookup("bash")
	eq(t, "depends", dnames(b.Depends), []string{"glibc>=2.38|glibc|"})
	eq(t, "provides", dnames(b.Provides), []string{"sh|sh|"})
	if b.DownloadSize != 900000 || b.InstalledSize != 2000000 || b.URL != "https://example.org/bash" ||
		b.Packager != "Arch Dev <dev@example.org>" || !b.BuildDate.Equal(time.Unix(1760000000, 0)) ||
		!reflect.DeepEqual(b.Licenses, []string{"GPL-3.0-or-later"}) {
		t.Fatalf("bash = %+v", b)
	}
	g, _ := db.Lookup("gvim")
	eq(t, "conflicts", dnames(g.Conflicts), []string{"vim|vim|"})
	eq(t, "replaces", dnames(g.Replaces), []string{"vim-gtk|vim-gtk|"})
	git, _ := db.Lookup("git")
	eq(t, "optdepends", dnames(git.OptDepends), []string{"hello|hello|demo plugin", "tk|tk|gui"})
}

func TestSyncAge(t *testing.T) {
	dir := fixture(t)
	old := time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	os.Chtimes(filepath.Join(dir, "sync", "core.db"), newer, newer)
	os.Chtimes(filepath.Join(dir, "sync", "extra.db"), old, old)
	db, err := openDir(dir, []string{"core", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if !db.SyncTime().Equal(old) {
		t.Fatalf("SyncTime = %v, want the oldest, %v", db.SyncTime(), old)
	}
	if got := db.SyncAge(old.Add(2 * time.Hour)); got != 2*time.Hour {
		t.Fatalf("SyncAge = %v, want 2h", got)
	}
	if got := db.SyncAge(old.Add(-time.Hour)); got != 0 {
		t.Fatalf("SyncAge before the mtime = %v, want 0", got)
	}
	empty := &DB{}
	empty.cur.Store(&snapshot{})
	if got := empty.SyncAge(time.Now()); got != 0 {
		t.Fatalf("SyncAge with no sync DB = %v, want 0", got)
	}
}

// ── Lookup and the installed state ───────────────────────────────────────────

func TestLookupMergesInstalledState(t *testing.T) {
	db := openFixture(t)
	v, _ := db.Lookup("vim")
	if !v.Installed || v.InstalledVersion != "9.0-1" || v.Version != "9.1-1" || v.Reason != ReasonExplicit || v.Foreign || !v.Update {
		t.Fatalf("vim = installed %v %q version %q reason %v foreign %v update %v", v.Installed, v.InstalledVersion, v.Version, v.Reason, v.Foreign, v.Update)
	}
	if v.InstallDate.IsZero() {
		t.Fatal("install date not read")
	}
	if g, _ := db.Lookup("glibc"); g.Update || g.Reason != ReasonDep {
		t.Fatalf("glibc = update %v reason %v, want an installed dependency at the same version", g.Update, g.Reason)
	}
	h, _ := db.Lookup("hello")
	if h.Reason != ReasonDep {
		t.Fatalf("hello reason = %v, want ReasonDep", h.Reason)
	}
	if g, _ := db.Lookup("gvim"); g.Installed || g.Reason != ReasonNone || g.RequiredBy != nil {
		t.Fatal("gvim is not installed")
	}
	if a, ok := db.Lookup("myaur"); !ok || !a.Foreign || !a.Installed || a.Repo != "" || a.Update {
		t.Fatalf("myaur = %+v, %v; want installed foreign", a, ok)
	}
	if _, ok := db.Lookup("nope"); ok {
		t.Fatal("Lookup found a package that does not exist")
	}
}

// TestUpdateOnlyWhenSyncIsNewer: a different version is not an update when the
// installed one is the newer (-Qu lists only newer sync versions).
func TestUpdateOnlyWhenSyncIsNewer(t *testing.T) {
	dir := fixture(t)
	f := filepath.Join(dir, "local", "glibc-2.40-1", "desc")
	data, _ := os.ReadFile(f)
	os.WriteFile(f, bytes.Replace(data, []byte("2.40-1"), []byte("2.41-1"), 1), 0o644)
	db, _ := openDir(dir, []string{"core", "extra"})
	g, _ := db.Lookup("glibc")
	if g.Update || g.InstalledVersion != "2.41-1" {
		t.Fatalf("glibc = update %v installed %q; a newer installed version is not an update", g.Update, g.InstalledVersion)
	}
	if v, _ := db.Lookup("vim"); !v.Update {
		t.Fatal("vim 9.0 -> 9.1 is an update")
	}
}

func TestInstalledViews(t *testing.T) {
	db := openFixture(t)
	eq(t, "explicit", names(db.Installed(ViewExplicit)), []string{"bash", "git", "myaur", "scripty", "vim"})
	// glibc: needed by bash. hello: optional for git. dash: provides the sh that
	// scripty needs. aurlib: needed by myaur. Left: libold and aurorphan.
	eq(t, "dependencies", names(db.Installed(ViewDependencies)), []string{"aurlib", "dash", "glibc", "hello"})
	eq(t, "orphans", names(db.Installed(ViewOrphans)), []string{"aurorphan", "libold"})
	eq(t, "foreign", names(db.Installed(ViewForeign)), []string{"aurlib", "aurorphan", "myaur"})
	eq(t, "nothing", len(db.Installed(View(99))), 0)
	eq(t, "counts", db.Count(), Counts{Explicit: 5, Dependencies: 4, Orphans: 2, Foreign: 3})
	// An installed package has its sync entry's detail.
	for _, p := range db.Installed(ViewExplicit) {
		if p.Name == "vim" && (p.Description == "" || p.Repo != "core") {
			t.Fatalf("installed vim = %+v, want the sync detail", p)
		}
	}
}

func TestRecent(t *testing.T) {
	dir := fixture(t)
	for i, n := range []string{"bash-5.2-1", "vim-9.0-1"} {
		f := filepath.Join(dir, "local", n, "desc")
		data, _ := os.ReadFile(f)
		os.WriteFile(f, bytes.Replace(data, []byte("1761000000"), []byte(fmt.Sprint(1762000000+i)), 1), 0o644)
	}
	db, _ := openDir(dir, []string{"core", "extra"})
	eq(t, "recent", names(db.Recent(2)), []string{"vim", "bash"})
	eq(t, "all dated", len(db.Recent(-1)), 11)
	eq(t, "zero", len(db.Recent(0)), 0)
	rec := Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "none")}
	db, _ = Open(rec)
	eq(t, "no local", len(db.Recent(5)), 0)
}

func TestRequiredByAndOptionalFor(t *testing.T) {
	db := openFixture(t)
	g, _ := db.Lookup("glibc")
	eq(t, "glibc required", g.RequiredBy, []string{"bash", "git", "vim"})
	eq(t, "glibc optional", g.OptionalFor, []string(nil))
	h, _ := db.Lookup("hello")
	eq(t, "hello required", h.RequiredBy, []string(nil))
	eq(t, "hello optional", h.OptionalFor, []string{"git"})
	// Provides-aware: scripty depends on "sh", which dash (and bash) provide.
	d, _ := db.Lookup("dash")
	eq(t, "dash required", d.RequiredBy, []string{"scripty"})
	b, _ := db.Lookup("bash")
	eq(t, "bash required", b.RequiredBy, []string{"scripty"})
	// Not installed: no reverse links.
	n, _ := db.Lookup("gvim")
	if n.RequiredBy != nil || n.OptionalFor != nil || n.Orphan {
		t.Fatal("an uninstalled package has no reverse links")
	}
	// A package that depends on what it provides itself is not its own consumer.
	dir := syncOnly(t, "r", gz(t, tarBytes(t, []tent{{"x-1", desc("x", "1", "")}})))
	os.MkdirAll(filepath.Join(dir, "local", "x-1"), 0o755)
	os.WriteFile(filepath.Join(dir, "local", "x-1", "desc"), []byte(desc("x", "1", "%REASON%\n1\n\n%DEPENDS%\ny\n\n%PROVIDES%\ny=1\n\n")), 0o644)
	db, err := openDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if x, _ := db.Lookup("x"); x.RequiredBy != nil || !x.Orphan {
		t.Fatalf("x = %+v, want an orphan that is not its own consumer", x)
	}
}

// ── search ───────────────────────────────────────────────────────────────────

func TestSearchRanking(t *testing.T) {
	raw := tarBytes(t, []tent{
		{"a", desc("vim", "1-1", "%DESC%\nan editor\n\n")},
		{"b", desc("vimpager", "1-1", "%DESC%\na pager\n\n")},
		{"c", desc("gvim", "1-1", "%DESC%\ngui editor\n\n")},
		{"d", desc("nano", "1-1", "%DESC%\nlike vim but small\n\n")},
		{"e", desc("emacs", "1-1", "%DESC%\nuses nonvim things\n\n")},
		{"f", desc("vi-mode", "1-1", "%DESC%\nVIM keys\n\n")},
	})
	db, _ := openDir(syncOnly(t, "r", gz(t, raw)), nil)
	// exact, prefix, name contains, then description contains (by name).
	eq(t, "vim", names(db.Search("VIM", 0)), []string{"vim", "vimpager", "gvim", "emacs", "nano", "vi-mode"})
	eq(t, "limit", names(db.Search("vim", 2)), []string{"vim", "vimpager"})
	eq(t, "two terms", names(db.Search("vim editor", 0)), []string{"gvim", "vim"})
	eq(t, "one rune", len(db.Search("v", 0)), 0)
	eq(t, "no match", len(db.Search("zzz", 0)), 0)
	eq(t, "blank", len(db.Search("   ", 0)), 0)
	eq(t, "empty", len(db.Search("", 0)), 0)
}

func TestSearchDefaultCap(t *testing.T) {
	var ents []tent
	for i := 0; i < MaxSearch+30; i++ {
		ents = append(ents, tent{fmt.Sprint(i), desc(fmt.Sprintf("lib%d", i), "1-1", "")})
	}
	db, _ := openDir(syncOnly(t, "r", gz(t, tarBytes(t, ents))), nil)
	if n := len(db.Search("lib", 0)); n != MaxSearch {
		t.Fatalf("results = %d, want the default cap %d", n, MaxSearch)
	}
}

// ── untrusted input ──────────────────────────────────────────────────────────

// TestMalformedEntriesAreSkipped: a missing or illegal %NAME%, a missing
// version and an oversized tent drop that tent only.
func TestMalformedEntriesAreSkipped(t *testing.T) {
	big := desc("big", "1-1", "%DESC%\n"+strings.Repeat("x", maxEntryBytes+10)+"\n\n")
	raw := tarBytes(t, []tent{
		{"ok-1", desc("ok", "1-1", "")},
		{"noname-1", "%VERSION%\n1-1\n\n%DESC%\nhas no name\n\n"},
		{"empty-1", ""},
		{"nover-1", "%NAME%\nnover\n\n"},
		{"evil-1", desc("../etc/passwd", "1-1", "")},
		{"evil2-1", desc("bad name", "1-1", "")},
		{"evil3-1", desc("-rf", "1-1", "")},
		{"evil4-1", desc("a\x1b[2Jb", "1-1", "")},
		{"big-1", big},
	})
	db, err := openDir(syncOnly(t, "r", gz(t, raw)), nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "packages", names(db.Search("ok", 0)), []string{"ok"})
	for _, n := range []string{"big", "nover", "-rf"} {
		if _, ok := db.Lookup(n); ok {
			t.Fatalf("%q loaded", n)
		}
	}
}

// TestUntrustedTextIsCleaned: control characters, escapes and bidi overrides
// in descriptions and list items are removed, and long fields are capped.
func TestUntrustedTextIsCleaned(t *testing.T) {
	var deps strings.Builder
	for i := 0; i < maxListItems+50; i++ {
		fmt.Fprintf(&deps, "dep%d\n", i)
	}
	rest := "%DESC%\nfine\x1b]52;c;ZXZpbA==\x07 text‮ reversed\x00\n\n" +
		"%URL%\n" + strings.Repeat("u", 5000) + "\n\n" +
		"%PACKAGER%\nMallory\x1b[31m\r\n\n" +
		"%OPTDEPENDS%\nfoo: bar\x1b[2J\n\n%DEPENDS%\n" + deps.String() + "\n"
	raw := tarBytes(t, []tent{{"x-1", desc("x", "1\x1b-1", rest)}})
	db, _ := openDir(syncOnly(t, "r", gz(t, raw)), nil)
	p, ok := db.Lookup("x")
	if !ok {
		t.Fatal("x missing")
	}
	all := strings.Join(append([]string{p.Description, p.URL, p.Packager, p.Version}, dnames(p.OptDepends)...), "|")
	for _, bad := range []string{"\x1b", "\x07", "\x00", "‮", "\r"} {
		if strings.Contains(all, bad) {
			t.Fatalf("%q survived in %q", bad, all)
		}
	}
	if p.Description != "fine text reversed" || p.Version != "1" {
		t.Fatalf("desc %q version %q", p.Description, p.Version)
	}
	if n := utf8.RuneCountInString(p.URL); n > maxFieldRunes+1 {
		t.Fatalf("url is %d runes, want it capped", n)
	}
	if len(p.Depends) != maxListItems {
		t.Fatalf("depends = %d, want %d", len(p.Depends), maxListItems)
	}
}

func TestHugeDescIsCapped(t *testing.T) {
	raw := tarBytes(t, []tent{{"x-1", desc("x", "1-1", "%DESC%\n"+strings.Repeat("é", 100000)+"\n\n")}})
	db, _ := openDir(syncOnly(t, "r", gz(t, raw)), nil)
	p, _ := db.Lookup("x")
	if n := utf8.RuneCountInString(p.Description); n != maxDescRunes+1 {
		t.Fatalf("desc is %d runes, want %d", n, maxDescRunes+1)
	}
}

func TestOddNumbers(t *testing.T) {
	raw := tarBytes(t, []tent{{"x-1", desc("x", "1-1", "%CSIZE%\n-5\n\n%ISIZE%\nlots\n\n%BUILDDATE%\n99999999999999\n\n")}})
	db, _ := openDir(syncOnly(t, "r", gz(t, raw)), nil)
	p, _ := db.Lookup("x")
	if p.DownloadSize != 0 || p.InstalledSize != 0 || !p.BuildDate.IsZero() {
		t.Fatalf("p = %+v, want zero sizes and date", p)
	}
}

// TestBrokenDatabasesAreLeftOut: a truncated, empty, garbage or
// undecodable sync DB is named in Problems and the others still load.
func TestBrokenDatabasesAreLeftOut(t *testing.T) {
	good := tarBytes(t, []tent{{"x-1", desc("x", "1-1", "")}})
	trunc := gz(t, tarBytes(t, []tent{{"y-1", desc("y", "1-1", "")}, {"z-1", desc("z", "1-1", strings.Repeat("%DESC%\nlong\n\n", 100))}}))
	cases := map[string][]byte{
		"truncated":  trunc[:len(trunc)-30],
		"garbage":    []byte("this is not an archive at all, not even close, really not at all"),
		"empty":      {},
		"emptytar":   tarBytes(t, nil),
		"badgzip":    {0x1f, 0x8b, 0x00},
		"badzstd":    {0x28, 0xb5, 0x2f, 0xfd, 0x00},
		"midentry":   gz(t, tarBytes(t, []tent{{"y-1", desc("y", "1-1", "")}})[:530]),
		"badheader":  gz(t, append(tarBytes(t, []tent{{"y-1", desc("y", "1-1", "")}})[:1024], bytes.Repeat([]byte("x"), 512)...)),
		"zstdcutoff": zst(t, tarBytes(t, []tent{{"y-1", desc("y", "1-1", "")}}))[:25],
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			dir := syncOnly(t, "good", gz(t, good))
			os.WriteFile(filepath.Join(dir, "sync", "bad.db"), raw, 0o644)
			db, err := openDir(dir, []string{"bad", "good"})
			if err != nil {
				t.Fatal(err)
			}
			eq(t, "repos", db.Repos(), []string{"good"})
			if len(db.Warnings()) == 0 || !strings.Contains(db.Warnings()[0], "bad.db") {
				t.Fatalf("problems = %v, want the bad one named", db.Warnings())
			}
			if _, ok := db.Lookup("y"); ok {
				t.Fatal("half a database was loaded")
			}
			if _, ok := db.Lookup("x"); !ok {
				t.Fatal("the good database was lost")
			}
		})
	}
}

func TestTooManyEntries(t *testing.T) {
	defer func(n int) { maxEntries = n }(maxEntries)
	maxEntries = 3
	var ents []tent
	for i := 0; i < 5; i++ {
		ents = append(ents, tent{fmt.Sprint(i), desc(fmt.Sprint("p", i), "1-1", "")})
	}
	_, err := openDir(syncOnly(t, "r", gz(t, tarBytes(t, ents))), nil)
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("err = %v, want too many entries", err)
	}
}

func TestOpenFailsWithNothingReadable(t *testing.T) {
	if _, err := openDir(t.TempDir(), nil); err == nil {
		t.Fatal("want an error for an empty dbpath")
	}
}

// TestLocalProblems: a symlinked desc, an invalid tent and an oversized one
// are skipped and named; a missing local dir does not stop the sync side.
func TestLocalProblems(t *testing.T) {
	dir := fixture(t)
	local := filepath.Join(dir, "local")
	os.MkdirAll(filepath.Join(local, "link-1"), 0o755)
	os.WriteFile(filepath.Join(dir, "real-desc"), []byte(desc("passwd", "1", "")), 0o644)
	os.Symlink(filepath.Join(dir, "real-desc"), filepath.Join(local, "link-1", "desc"))
	os.MkdirAll(filepath.Join(local, "bad-1"), 0o755)
	os.WriteFile(filepath.Join(local, "bad-1", "desc"), []byte("%VERSION%\n1\n"), 0o644)
	os.MkdirAll(filepath.Join(local, "huge-1"), 0o755)
	os.WriteFile(filepath.Join(local, "huge-1", "desc"), []byte(desc("huge", "1", "%DESC%\n"+strings.Repeat("x", maxEntryBytes+1)+"\n")), 0o644)
	os.MkdirAll(filepath.Join(local, "nodesc-1"), 0o755)
	os.WriteFile(filepath.Join(local, "ALPM_DB_VERSION"), []byte("9\n"), 0o644)
	db, err := openDir(dir, []string{"core", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Warnings()) != 4 {
		t.Fatalf("problems = %q, want 4", db.Warnings())
	}
	if _, ok := db.Lookup("passwd"); ok || func() bool { _, ok := db.Lookup("huge"); return ok }() || len(db.installedAll()) != 11 {
		t.Fatal("a bad local tent was taken")
	}

	os.RemoveAll(local)
	db, err = openDir(dir, []string{"core", "extra"})
	if err != nil || len(db.Warnings()) != 1 || len(db.installedAll()) != 0 {
		t.Fatalf("no local dir: err %v problems %v", err, db.Warnings())
	}
}

func TestTooManyInstalled(t *testing.T) {
	defer func(n int) { maxLocal = n }(maxLocal)
	maxLocal = 2
	dir := syncOnly(t, "r", gz(t, tarBytes(t, []tent{{"x-1", desc("x", "1-1", "")}})))
	for _, n := range []string{"a", "b", "c"} {
		os.MkdirAll(filepath.Join(dir, "local", n+"-1"), 0o755)
		os.WriteFile(filepath.Join(dir, "local", n+"-1", "desc"), []byte(desc(n, "1", "")), 0o644)
	}
	db, err := openDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := db.Warnings(); len(p) != 1 || !strings.Contains(p[0], "too many installed") {
		t.Fatalf("problems = %v", p)
	}
}

func TestReadSyncMissingFile(t *testing.T) {
	if _, err := readSync(filepath.Join(t.TempDir(), "none.db"), "x"); err == nil {
		t.Fatal("want an error")
	}
}

// ── benchmark ────────────────────────────────────────────────────────────────

const benchPackages = 15000

func benchDB(b testing.TB) *DB {
	ents := make([]tent, benchPackages)
	for i := range ents {
		name := fmt.Sprintf("pkg%05d-%s", i, []string{"git", "lib", "qt", "python", "utils", "tools", "kde", "gtk"}[i%8])
		d := fmt.Sprintf("%%DESC%%\nA package number %d that does something useful with the %s toolkit and friends\n\n"+
			"%%DEPENDS%%\nglibc\nzlib\nopenssl\n\n%%URL%%\nhttps://example.org/%s\n\n", i, name, name)
		ents[i] = tent{name + "-1.0-1", desc(name, "1.0-1", d)}
	}
	db, err := openDir(syncOnly(b, "bench", gz(b, tarBytes(b, ents))), nil)
	if err != nil {
		b.Fatal(err)
	}
	return db
}

// TestSearchIsFast: 15k packages scanned in well under the 50 ms budget, a
// generous bound (the benchmark below gives the real figure).
func TestSearchIsFast(t *testing.T) {
	db := benchDB(t)
	start := time.Now()
	res := db.Search("toolkit", 0)
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("search took %v for %d packages", d, benchPackages)
	}
	if len(res) != MaxSearch {
		t.Fatalf("results = %d", len(res))
	}
}

func BenchmarkSearch15k(b *testing.B) {
	db := benchDB(b)
	for _, q := range []string{"pkg0123", "toolkit", "qt", "git qt", "nomatch"} {
		b.Run(strings.ReplaceAll(q, " ", "_"), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				db.Search(q, 0)
			}
		})
	}
}

func BenchmarkOpen15k(b *testing.B) {
	dir := syncOnly(b, "bench", nil)
	benchDBInto(b, dir)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := openDir(dir, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDBInto(b testing.TB, dir string) {
	ents := make([]tent, benchPackages)
	for i := range ents {
		name := fmt.Sprintf("pkg%05d", i)
		ents[i] = tent{name + "-1.0-1", desc(name, "1.0-1", "%DESC%\nA package that does something useful\n\n%DEPENDS%\nglibc\nzlib\n\n")}
	}
	if err := os.WriteFile(filepath.Join(dir, "sync", "bench.db"), gz(b, tarBytes(b, ents)), 0o644); err != nil {
		b.Fatal(err)
	}
}
