package helper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// pkg-install-local's tests. Nothing executes: pacman is a fake that reads
// the staged files it is given (so the tests can see what root would have
// installed), the "home" is a temp folder named by a temp passwd file, and
// the walk starts below the temp folder (walkAnchor) instead of "/". The
// user is the test process's own uid; a foreign owner is faked on the fstat
// the walk sees (fstatHook), a symlink or a mode is real.

type localFx struct {
	t       *testing.T
	anchor  string
	home    string // anchor/h1/h2
	out     string // home/.cache/archmaid/aur/<base>/out
	base    string
	staging string // the staging root
	passwd  string
	uid     uint32
	sys     *localSys
	opts    func(*Options) // per-test tweak
}

const baseName = "foo"

func newLocalFx(t *testing.T) *localFx {
	t.Helper()
	anchor := t.TempDir()
	fx := &localFx{t: t, anchor: anchor, base: baseName, uid: uint32(os.Getuid())}
	fx.home = filepath.Join(anchor, "h1", "h2")
	fx.out = filepath.Join(fx.home, ".cache", "archmaid", "aur", baseName, "out")
	if err := os.MkdirAll(fx.out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(anchor, "h1"), fx.home} {
		os.Chmod(d, 0o700)
	}
	fx.staging = filepath.Join(anchor, "cache", "staging")
	fx.passwd = filepath.Join(anchor, "passwd")
	fx.setPasswd(fx.home)
	fx.sys = newLocalSys(t, fx)
	return fx
}

func (fx *localFx) setPasswd(home string) {
	line := fmt.Sprintf("root:x:0:0::/root:/bin/sh\ntester:x:%d:%d::%s:/bin/sh\n", fx.uid, fx.uid, home)
	if err := os.WriteFile(fx.passwd, []byte(line), 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

// pkg writes a built package file with content into out and returns the
// argv path and its LocalFile.
func (fx *localFx) pkg(name, content string) (string, LocalFile) {
	fx.t.Helper()
	p := filepath.Join(fx.out, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		fx.t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return fx.base + "/out/" + name, LocalFile{Name: name, Size: int64(len(content)), Sha256: hex.EncodeToString(sum[:])}
}

// run runs the verb as the test user.
func (fx *localFx) run(args ...string) (int, []map[string]any) {
	fx.t.Helper()
	o, out := rootOpts(fx.t, fx.sys)
	o.CallerUID = strconv.Itoa(int(fx.uid))
	o.PasswdPath = fx.passwd
	o.StagingRoot = fx.staging
	o.walkAnchor = fx.anchor
	o.KernelPkgbases = func() ([]string, error) { return []string{"/usr/lib/modules/6.1.0-1/pkgbase"}, nil }
	o.PacmanDBLock = filepath.Join(fx.anchor, "db.lck")
	if fx.opts != nil {
		fx.opts(&o)
	}
	code := Run(append([]string{"pkg-install-local"}, args...), o)
	return code, events(fx.t, out)
}

// good is a one-file call that must succeed.
func (fx *localFx) good() (args []string, want string) {
	arg, lf := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "foo bytes")
	return []string{PkgLocalHash([]LocalFile{lf}), arg}, PkgLocalHash([]LocalFile{lf})
}

func (fx *localFx) noStagingLeft() {
	fx.t.Helper()
	ents, err := os.ReadDir(fx.staging)
	if err != nil && !os.IsNotExist(err) {
		fx.t.Fatal(err)
	}
	if len(ents) != 0 {
		fx.t.Errorf("staging not cleaned: %v", ents)
	}
}

func (fx *localFx) wantRefused(code int, evs []map[string]any, wantCode int, reason string) {
	fx.t.Helper()
	if code != wantCode {
		fx.t.Errorf("exit %d, want %d: %s", code, wantCode, resultErr(evs))
	}
	if got, _ := evs[len(evs)-1]["reason"].(string); got != reason {
		fx.t.Errorf("reason %q, want %q (%s)", got, reason, resultErr(evs))
	}
	if len(fx.sys.streams) != 0 {
		fx.t.Errorf("pacman ran: %v", fx.sys.streams)
	}
	fx.noStagingLeft()
}

// localSys is the fake pacman. Query answers from fixtures; Stream records
// -U and what the staged files held at that moment.
type localSys struct {
	t  *testing.T
	fx *localFx

	mu        sync.Mutex
	installed map[string]string
	updates   []string
	arch      string
	info      map[string]string // file name → extra/override fields; "!" = the whole block
	queries   [][]string
	streams   [][]string
	// at -U: path → content
	seen   map[string]string
	exit   int
	onUQ   func(argv []string) // before each query
	onStrm func(argv []string)
}

func newLocalSys(t *testing.T, fx *localFx) *localSys {
	return &localSys{t: t, fx: fx, installed: map[string]string{"bash": "5.2-1", "glibc": "2.40-1", "oldpkg": "1-1"},
		arch: "x86_64\n", info: map[string]string{}, seen: map[string]string{}}
}

// infoBlock is the `pacman -Qip` text for a file: right unless overridden.
func (p *localSys) infoBlock(file string) string {
	if b, ok := p.info[file+"!"]; ok {
		return b
	}
	m := localPkgPattern.FindStringSubmatch(file)
	if m == nil {
		p.t.Fatalf("test file %q does not match the pattern", file)
	}
	name, ver, arch, extra := m[1], m[2]+"-"+m[3], m[4], p.info[file]
	return fmt.Sprintf("Name            : %s\nVersion         : %s\nDescription     : a test package\nArchitecture    : %s\nURL             : None\nLicenses        : MIT\nProvides        : None\nDepends On      : None\nConflicts With  : None\nReplaces        : None\nInstall Script  : No\n%s", name, ver, arch, extra)
}

func (p *localSys) Query(argv []string) (string, string, int, error) {
	p.mu.Lock()
	p.queries = append(p.queries, slices.Clone(argv))
	hook := p.onUQ
	p.mu.Unlock()
	if hook != nil {
		hook(argv)
	}
	if len(argv) >= 2 && argv[0] == PacmanConf && argv[1] == "Architecture" {
		return p.arch, "", 0, nil
	}
	if len(argv) < 2 || argv[0] != Pacman {
		p.t.Fatalf("query is not pacman: %q", argv)
	}
	op := argv[1]
	var names []string
	if i := slices.Index(argv, "--"); i >= 0 {
		names = argv[i+1:]
	}
	// Whatever pacman is asked to read must be a staged copy, never the
	// user's file.
	if op == "-Qip" || op == "-Qlp" {
		if len(names) != 1 || !strings.HasPrefix(names[0], p.fx.staging+"/run-") {
			p.t.Fatalf("pacman %s reads %q, not a staged copy", op, names)
		}
		if op == "-Qlp" {
			return "foo /usr/bin/foo\nfoo /usr/share/doc/foo/README\n", "", 0, nil
		}
		if x, ok := p.info[filepath.Base(names[0])+"#exit"]; ok {
			return "", "error: could not open " + x, 1, nil
		}
		return p.infoBlock(filepath.Base(names[0])), "", 0, nil
	}
	switch op {
	case "-Qu":
		if len(p.updates) == 0 {
			return "", "", 1, nil
		}
		return strings.Join(p.updates, "\n") + "\n", "", 0, nil
	case "-Q":
		var b strings.Builder
		for n, v := range p.installed {
			fmt.Fprintf(&b, "%s %s\n", n, v)
		}
		return b.String(), "", 0, nil
	case "-Qqo":
		return "linux-cachyos\n", "", 0, nil
	case "-Qi":
		return "Name : base\nDepends On      : filesystem glibc>=2.40\n", "", 0, nil
	}
	p.t.Fatalf("unexpected query: %q", argv)
	return "", "", 2, nil
}

func (p *localSys) Stream(argv []string, line func(stream, text string)) (int, error) {
	p.mu.Lock()
	p.streams = append(p.streams, slices.Clone(argv))
	p.mu.Unlock()
	if len(argv) < 3 || argv[0] != Pacman || argv[1] != "-U" {
		p.t.Fatalf("unexpected root command: %q", argv)
	}
	if p.onStrm != nil {
		p.onStrm(argv)
	}
	for _, a := range argv[slices.Index(argv, "--")+1:] {
		b, err := os.ReadFile(a)
		if err != nil {
			p.t.Errorf("staged file %s: %v", a, err)
		}
		p.seen[a] = string(b)
	}
	line("out", "installed")
	return p.exit, nil
}

// ---- the happy path, the hash, the argv ------------------------------------

func TestLocalInstallHappy(t *testing.T) {
	fx := newLocalFx(t)
	a1, f1 := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "foo bytes")
	a2, f2 := fx.pkg("foo-docs-1:2.0-3-any.pkg.tar.xz", "docs bytes")
	h := PkgLocalHash([]LocalFile{f1, f2})
	code, evs := fx.run(h, a1, a2)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	if len(fx.sys.streams) != 1 {
		t.Fatalf("streams %v", fx.sys.streams)
	}
	argv := fx.sys.streams[0]
	s1, s2 := argv[len(argv)-2], argv[len(argv)-1]
	want := []string{Pacman, "-U", "--noconfirm", "--color", "always", "--", s1, s2}
	if !slices.Equal(argv, want) {
		t.Errorf("argv %q", argv)
	}
	// build order kept; staged under the staging root; bytes are the user's
	if filepath.Base(s1) != f1.Name || filepath.Base(s2) != f2.Name || filepath.Dir(s1) != filepath.Dir(s2) ||
		filepath.Dir(filepath.Dir(s1)) != fx.staging {
		t.Errorf("staged paths %s %s", s1, s2)
	}
	if fx.sys.seen[s1] != "foo bytes" || fx.sys.seen[s2] != "docs bytes" {
		t.Errorf("staged content %v", fx.sys.seen)
	}
	fx.noStagingLeft()
	// the log carries .PKGINFO's text and the install-script note
	var log []string
	for _, e := range ofType(evs, EventLine) {
		log = append(log, e["text"].(string))
	}
	all := strings.Join(log, "\n")
	for _, w := range []string{"Name            : foo", "no install script (.INSTALL)", "/usr/bin/foo", "Version         : 1:2.0-3"} {
		if !strings.Contains(all, w) {
			t.Errorf("log lacks %q:\n%s", w, all)
		}
	}
	// the call is order-independent for the hash
	if PkgLocalHash([]LocalFile{f2, f1}) != h {
		t.Error("hash depends on the order of the files")
	}
}

func TestPkgLocalHashGolden(t *testing.T) {
	// "archmaid pkg local v1\n" and one sorted line per file; the same
	// golden as internal/aurbuild's TestLocalHash (tied in cmd/archmaid-helper).
	fs := []LocalFile{{"b-1-1-any.pkg.tar.zst", 3, strings.Repeat("b", 64)}, {"a-1-1-any.pkg.tar.zst", 2, strings.Repeat("a", 64)}}
	sum := sha256.Sum256([]byte("archmaid pkg local v1\n" +
		"file a-1-1-any.pkg.tar.zst 2 " + strings.Repeat("a", 64) + "\n" +
		"file b-1-1-any.pkg.tar.zst 3 " + strings.Repeat("b", 64) + "\n"))
	if got, want := PkgLocalHash(fs), "sha256:"+hex.EncodeToString(sum[:]); got != want {
		t.Errorf("hash %s, want %s", got, want)
	}
	// a name, a size or a byte changes it
	base := PkgLocalHash(fs)
	for i, mut := range []func(f []LocalFile){
		func(f []LocalFile) { f[0].Name = "c-1-1-any.pkg.tar.zst" },
		func(f []LocalFile) { f[0].Size++ },
		func(f []LocalFile) { f[0].Sha256 = strings.Repeat("c", 64) },
		func(f []LocalFile) { f[1] = f[0] },
	} {
		c := slices.Clone(fs)
		mut(c)
		if PkgLocalHash(c) == base {
			t.Errorf("mutation %d did not change the hash", i)
		}
	}
}

// TestInstallLocalArgvPinned: the one root command, and none of the options
// that would widen it.
func TestInstallLocalArgvPinned(t *testing.T) {
	got := installLocalArgv([]string{"/s/a", "/s/b"})
	if g := strings.Join(got, " "); g != "/usr/bin/pacman -U --noconfirm --color always -- /s/a /s/b" {
		t.Errorf("argv %s", g)
	}
	for _, bad := range []string{"-S", "-Sy", "-y", "-u", "-d", "-dd", "--nodeps", "--overwrite", "--noscriptlet", "--needed",
		"--asdeps", "--asexplicit", "--force", "--dbonly", "--noprogressbar", "--config", "--dbpath", "--root", "--sysroot", "--hookdir", "--gpgdir", "--cachedir", "--ignore", "--ignoregroup"} {
		if slices.Contains(got, bad) {
			t.Errorf("argv holds %s", bad)
		}
	}
	// every target is after "--"
	i := slices.Index(got, "--")
	for _, a := range got[:i] {
		if strings.HasPrefix(a, "/s/") {
			t.Errorf("target %s before --", a)
		}
	}
	for _, c := range [][]string{localInfoPkgArgv("/s/a"), localListArgv("/s/a")} {
		if strings.Join(c[len(c)-2:], " ") != "-- /s/a" || !slices.Contains(c, "never") {
			t.Errorf("read argv %q", c)
		}
	}
}

// ---- input -----------------------------------------------------------------

func TestLocalArgsTable(t *testing.T) {
	ok := "foo/out/foo-1.0-1-x86_64.pkg.tar.zst"
	many := make([]string, 0, MaxLocalFiles+1)
	for i := range MaxLocalFiles + 1 {
		many = append(many, fmt.Sprintf("foo/out/p%d-1-1-any.pkg.tar.zst", i))
	}
	h := "sha256:" + strings.Repeat("0", 64)
	bad := [][]string{
		{"/etc/passwd"}, {"/home/x/.cache/archmaid/aur/foo/out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"../foo/out/foo-1.0-1-x86_64.pkg.tar.zst"}, {"foo/../out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"foo/out/../foo-1.0-1-x86_64.pkg.tar.zst"}, {"foo/out/../../x/out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"foo//out/foo-1.0-1-x86_64.pkg.tar.zst"}, {"foo/out//foo-1.0-1-x86_64.pkg.tar.zst"}, {"/foo/out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"foo/out/"}, {""}, {"foo/out"}, {"foo"}, {"foo/out/sub/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"foo/build/foo-1.0-1-x86_64.pkg.tar.zst"}, {"foo/out/./foo-1.0-1-x86_64.pkg.tar.zst"}, {"./foo/out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{".foo/out/foo-1.0-1-x86_64.pkg.tar.zst"}, {"-foo/out/foo-1.0-1-x86_64.pkg.tar.zst"}, {"../out/foo-1.0-1-x86_64.pkg.tar.zst"},
		{"foo/out/foo-1.0-1-x86_64.pkg.tar.gz"}, {"foo/out/foo-1.0-1-x86_64.pkg.tar"}, {"foo/out/foo-1.0-1-x86_64.pkg.tar.zst.sig"},
		{"foo/out/foo-1.0-1-x86_64.pkg.tar.zst "}, {"foo/out/foo 1.0-1-x86_64.pkg.tar.zst"}, {"foo/out/foo-1.0-1-x86_64.pkg.tar.zst\n"},
		{"foo/out/foo-1.0-x86_64.pkg.tar.zst"}, {"foo/out/foo-1.0-a-x86_64.pkg.tar.zst"}, {"foo/out/foo-1.0-1-x86-64.pkg.tar.zst"},
		{"foo/out/-foo-1.0-1-x86_64.pkg.tar.zst"}, {"foo/out/foo-1.0-1-x86_64.pkg.tar.zst\x00"}, {"foo/out/fo\x00o-1.0-1-x86_64.pkg.tar.zst"},
		{"foo/out/é-1.0-1-x86_64.pkg.tar.zst"}, {"foo/out/foo-1.0-1-x86_64.pkg.tar.zst;rm"}, {"foo/out/$(x)-1.0-1-any.pkg.tar.zst"},
		{ok, ok}, {ok, "bar/out/foo-1.0-1-x86_64.pkg.tar.zst"}, many,
	}
	for _, files := range bad {
		fx := newLocalFx(t)
		fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "x")
		code, evs := fx.run(append([]string{h}, files...)...)
		fx.wantRefused(code, evs, ExitUsage, ReasonBadRequest)
		if len(fx.sys.queries) != 0 {
			t.Errorf("%q ran queries %v", files, fx.sys.queries)
		}
		if _, err := os.Stat(fx.staging); err == nil {
			t.Errorf("%q created the staging folder", files)
		}
	}
	// a bad or missing hash, no files
	for _, args := range [][]string{{}, {h}, {"foo", ok}, {"sha256:xyz", ok}, {strings.ToUpper(h), ok}, {ok, h}} {
		fx := newLocalFx(t)
		code, evs := fx.run(args...)
		fx.wantRefused(code, evs, ExitUsage, ReasonBadRequest)
	}
	// 64 files is the most
	fx := newLocalFx(t)
	var args []string
	var lfs []LocalFile
	for i := range MaxLocalFiles {
		a, f := fx.pkg(fmt.Sprintf("p%d-1-1-any.pkg.tar.zst", i), fmt.Sprintf("p%d", i))
		args, lfs = append(args, a), append(lfs, f)
	}
	if code, evs := fx.run(append([]string{PkgLocalHash(lfs)}, args...)...); code != ExitOK {
		t.Errorf("32 files: exit %d: %s", code, resultErr(evs))
	}
	// names real makepkg output has
	for _, n := range []string{"foo-1.0-1-x86_64.pkg.tar.zst", "lib32-foo-1.0.r5.gabc-2-x86_64_v3.pkg.tar.xz", "Foo_Bar+x-1:2.0~rc1-1-any.pkg.tar.zst", "@scope-1-1-any.pkg.tar.zst", "foo-2024.01.02_3-10-aarch64.pkg.tar.zst"} {
		if !localPkgPattern.MatchString(n) {
			t.Errorf("%q should be a package name", n)
		}
	}
}

// ---- the path walk ---------------------------------------------------------

// levels are the folders of the walk, from the first one below the anchor.
func (fx *localFx) levels() []string {
	return []string{
		filepath.Join(fx.anchor, "h1"), fx.home,
		filepath.Join(fx.home, ".cache"), filepath.Join(fx.home, ".cache", "archmaid"),
		filepath.Join(fx.home, ".cache", "archmaid", "aur"), filepath.Join(fx.home, ".cache", "archmaid", "aur", fx.base), fx.out,
	}
}

func TestLocalSymlinkAtEveryLevel(t *testing.T) {
	for i := range 8 { // 7 folders and the file
		fx := newLocalFx(t)
		args, _ := fx.good()
		target := t.TempDir() // a real copy elsewhere; a link to it must not be followed
		path := filepath.Join(fx.out, "foo-1.0-1-x86_64.pkg.tar.zst")
		if i < 7 {
			path = fx.levels()[i]
		}
		moved := filepath.Join(target, "moved")
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, path); err != nil {
			t.Fatal(err)
		}
		code, evs := fx.run(args...)
		fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
		if !strings.Contains(resultErr(evs), "symbolic link") {
			t.Errorf("level %d: error %q", i, resultErr(evs))
		}
	}
	// a link to a root-only file, as the file itself
	fx := newLocalFx(t)
	args, _ := fx.good()
	p := filepath.Join(fx.out, "foo-1.0-1-x86_64.pkg.tar.zst")
	os.Remove(p)
	os.Symlink("/etc/passwd", p)
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
}

func TestLocalForeignOwnerAtEveryLevel(t *testing.T) {
	name := "foo-1.0-1-x86_64.pkg.tar.zst"
	for i := range 8 { // 7 folders and the file
		fx := newLocalFx(t)
		args, _ := fx.good()
		target := filepath.Join(fx.out, name)
		if i < 7 {
			target = fx.levels()[i]
		}
		fx.opts = func(o *Options) {
			o.fstatHook = func(l string, st *unix.Stat_t) {
				if l == target {
					st.Uid = fx.uid + 1
				}
			}
		}
		code, evs := fx.run(args...)
		fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
		if !strings.Contains(resultErr(evs), "owned by uid") {
			t.Errorf("level %d: error %q", i, resultErr(evs))
		}
	}
	// the folders above the home may be root's (or the user's), nobody else's
	fx := newLocalFx(t)
	args, _ := fx.good()
	above := fx.levels()[0]
	fx.opts = func(o *Options) {
		o.fstatHook = func(l string, st *unix.Stat_t) {
			if l == above {
				st.Uid = 0
			}
		}
	}
	if code, evs := fx.run(args...); code != ExitOK {
		t.Errorf("a root-owned folder above the home: exit %d: %s", code, resultErr(evs))
	}
	// but the home itself and everything below it must be the user's
	for i := 1; i < 7; i++ {
		fx = newLocalFx(t)
		args, _ = fx.good()
		target := fx.levels()[i]
		fx.opts = func(o *Options) {
			o.fstatHook = func(l string, st *unix.Stat_t) {
				if l == target {
					st.Uid = 0
				}
			}
		}
		code, evs := fx.run(args...)
		fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
	}
}

func TestLocalWritableByOthersAtEveryLevel(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o702, 0o777, 0o720} {
		for i := range 8 {
			fx := newLocalFx(t)
			args, _ := fx.good()
			path := filepath.Join(fx.out, "foo-1.0-1-x86_64.pkg.tar.zst")
			fm := mode &^ 0o100
			if i < 7 {
				path, fm = fx.levels()[i], mode
			}
			if err := os.Chmod(path, fm); err != nil {
				t.Fatal(err)
			}
			code, evs := fx.run(args...)
			fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
			if !strings.Contains(resultErr(evs), "writable by group or others") {
				t.Errorf("level %d mode %o: error %q", i, mode, resultErr(evs))
			}
			os.Chmod(path, 0o700) // let the temp dir go
		}
	}
	// group-readable (not writable) is fine
	fx := newLocalFx(t)
	args, _ := fx.good()
	os.Chmod(fx.out, 0o750)
	os.Chmod(filepath.Join(fx.out, "foo-1.0-1-x86_64.pkg.tar.zst"), 0o644)
	if code, evs := fx.run(args...); code != ExitOK {
		t.Errorf("exit %d: %s", code, resultErr(evs))
	}
}

func TestLocalNotRegularFile(t *testing.T) {
	name := "foo-1.0-1-x86_64.pkg.tar.zst"
	for kind, mk := range map[string]func(p string){
		"directory": func(p string) { os.Mkdir(p, 0o700) },
		"fifo":      func(p string) { unix.Mkfifo(p, 0o600) },
		"missing":   func(p string) {},
	} {
		fx := newLocalFx(t)
		args, _ := fx.good()
		p := filepath.Join(fx.out, name)
		os.Remove(p)
		mk(p)
		code, evs := fx.run(args...)
		if kind == "missing" {
			fx.wantRefused(code, evs, ExitStale, ReasonStale)
			continue
		}
		fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
		if !strings.Contains(resultErr(evs), "not a regular file") {
			t.Errorf("%s: %q", kind, resultErr(evs))
		}
	}
	// a hard link to a file that is not the user's is refused by its owner
	fx := newLocalFx(t)
	args, _ := fx.good()
	fx.opts = func(o *Options) {
		o.fstatHook = func(l string, st *unix.Stat_t) {
			if strings.HasSuffix(l, "/"+name) {
				st.Uid = 0
			}
		}
	}
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
}

func TestLocalSizeCap(t *testing.T) {
	old := localSizeLimit
	t.Cleanup(func() { localSizeLimit = old })
	localSizeLimit = 10

	// too big on stat
	fx := newLocalFx(t)
	arg, lf := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", strings.Repeat("x", 11))
	code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
	if !strings.Contains(resultErr(evs), "a package is 1 byte to 10") {
		t.Errorf("error %q", resultErr(evs))
	}

	// empty
	fx = newLocalFx(t)
	arg, lf = fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "")
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
	if !strings.Contains(resultErr(evs), "a package is 1 byte to 10") {
		t.Errorf("error %q", resultErr(evs))
	}

	// exactly the limit is fine
	fx = newLocalFx(t)
	arg, lf = fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", strings.Repeat("x", 10))
	if code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg); code != ExitOK {
		t.Errorf("exit %d: %s", code, resultErr(evs))
	}

	// small on stat, grows past the cap while it is copied: the copy stops
	fx = newLocalFx(t)
	arg, lf = fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "12345678")
	fx.opts = func(o *Options) {
		o.afterOpen = func(p string) {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("123456789")
			f.Close()
		}
	}
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonUnsafeFile)
	if !strings.Contains(resultErr(evs), "grew past") {
		t.Errorf("error %q", resultErr(evs))
	}
}

// ---- the hash and the swap -------------------------------------------------

func TestLocalHashMismatch(t *testing.T) {
	fx := newLocalFx(t)
	arg, lf := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "confirmed bytes")
	// the user confirmed other bytes, a different size, a different name
	for i, other := range []LocalFile{
		{lf.Name, lf.Size, strings.Repeat("0", 64)},
		{lf.Name, lf.Size + 1, lf.Sha256},
		{"foo-1.0-2-x86_64.pkg.tar.zst", lf.Size, lf.Sha256},
	} {
		code, evs := fx.run(PkgLocalHash([]LocalFile{other}), arg)
		fx.wantRefused(code, evs, ExitStale, ReasonStale)
		if len(fx.sys.queries) > 0 && slices.ContainsFunc(fx.sys.queries, func(q []string) bool { return q[1] == "-Qip" }) {
			t.Errorf("case %d: pacman parsed a file whose hash was wrong", i)
		}
	}
	// a hash for fewer or more files
	code, evs := fx.run(PkgLocalHash(nil), arg)
	fx.wantRefused(code, evs, ExitStale, ReasonStale)
}

// TestLocalSwapAfterWalk: a file replaced or edited after the walk opened it.
// A rename over the path cannot matter: root reads the fd it holds. An edit
// of the very file shows in the hash.
func TestLocalSwapAfterWalk(t *testing.T) {
	name := "foo-1.0-1-x86_64.pkg.tar.zst"

	// 1. renamed over: the installed bytes are the confirmed ones
	fx := newLocalFx(t)
	arg, lf := fx.pkg(name, "the reviewed build")
	fx.opts = func(o *Options) {
		o.afterOpen = func(p string) {
			evil := p + ".evil"
			os.WriteFile(evil, []byte("EVIL replacement"), 0o600)
			if err := os.Rename(evil, p); err != nil {
				t.Error(err)
			}
		}
	}
	code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	for p, c := range fx.sys.seen {
		if c != "the reviewed build" {
			t.Errorf("%s installed %q", p, c)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(fx.out, name)); string(b) != "EVIL replacement" {
		t.Error("the swap did not happen: the test proves nothing")
	}

	// 2. the folder swapped for a link to another: same
	fx = newLocalFx(t)
	arg, lf = fx.pkg(name, "the reviewed build")
	evilDir := t.TempDir()
	os.WriteFile(filepath.Join(evilDir, name), []byte("EVIL replacement"), 0o600)
	fx.opts = func(o *Options) {
		o.afterOpen = func(p string) {
			os.Rename(fx.out, fx.out+".moved")
			os.Symlink(evilDir, fx.out)
		}
	}
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	for _, c := range fx.sys.seen {
		if c != "the reviewed build" {
			t.Errorf("installed %q", c)
		}
	}

	// 3. edited in place (same inode) after the confirm: stale, nothing run
	fx = newLocalFx(t)
	arg, lf = fx.pkg(name, "the reviewed build")
	fx.opts = func(o *Options) {
		o.afterOpen = func(p string) { os.WriteFile(p, []byte("the EVIL build!!"), 0o600) }
	}
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitStale, ReasonStale)

	// 4. edited before the walk (after the confirm): stale
	fx = newLocalFx(t)
	arg, lf = fx.pkg(name, "the reviewed build")
	os.WriteFile(filepath.Join(fx.out, name), []byte("changed since"), 0o600)
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitStale, ReasonStale)
}

// ---- the caller ------------------------------------------------------------

func TestLocalCallerAndHome(t *testing.T) {
	// The home is the passwd entry's: a HOME in the environment is never read.
	fx := newLocalFx(t)
	args, _ := fx.good()
	evil := t.TempDir()
	t.Setenv("HOME", evil)
	t.Setenv("XDG_CACHE_HOME", evil)
	if code, evs := fx.run(args...); code != ExitOK {
		t.Errorf("exit %d: %s", code, resultErr(evs))
	}
	// a passwd entry that points elsewhere finds no file: stale, and the
	// real files are not touched
	fx = newLocalFx(t)
	args, _ = fx.good()
	other := filepath.Join(fx.anchor, "h1", "other")
	os.Mkdir(other, 0o700)
	fx.setPasswd(other)
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitStale, ReasonStale)

	for name, uid := range map[string]string{"empty": "", "root": "0", "letters": "abc", "negative": "-1", "huge": "99999999999", "unknown": "424242"} {
		fx = newLocalFx(t)
		args, _ = fx.good()
		fx.opts = func(o *Options) { o.CallerUID = uid }
		code, evs = fx.run(args...)
		if code != ExitRefused {
			t.Errorf("%s: exit %d", name, code)
		}
		if len(fx.sys.streams) != 0 {
			t.Errorf("%s: ran %v", name, fx.sys.streams)
		}
	}
	// home field shapes
	for _, home := range []string{"relative/dir", "/", "/a/../b", "/a//b", ""} {
		if h, err := homeFromPasswd(writePasswd(t, "x:x:1000:1000::"+home+":/bin/sh\n"), 1000); err == nil {
			t.Errorf("home %q accepted as %q", home, h)
		}
	}
	// the first entry of a uid wins; comments and short lines are skipped
	p := writePasswd(t, "# c\nshort:x:1000\nfirst:x:1000:1000::/home/first:/bin/sh\nsecond:x:1000:1000::/home/second:/bin/sh\n")
	if h, err := homeFromPasswd(p, 1000); err != nil || h != "/home/first" {
		t.Errorf("home %q, %v", h, err)
	}
}

func writePasswd(t *testing.T, s string) string {
	p := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocalNeedsRoot(t *testing.T) {
	fx := newLocalFx(t)
	args, _ := fx.good()
	fx.opts = func(o *Options) { o.Euid = 1000 }
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitRefused, "")
	if _, err := os.Stat(fx.staging); err == nil {
		t.Error("staged without being root")
	}
}

// ---- .PKGINFO --------------------------------------------------------------

func TestLocalPkginfoMismatch(t *testing.T) {
	name := "foo-1.0-1-x86_64.pkg.tar.zst"
	block := func(n, v, a string) string {
		return fmt.Sprintf("Name            : %s\nVersion         : %s\nArchitecture    : %s\nInstall Script  : No\n", n, v, a)
	}
	for what, info := range map[string]string{
		"other name":        block("bar", "1.0-1", "x86_64"),
		"name prefix":       block("foo-x", "1.0-1", "x86_64"),
		"other version":     block("foo", "1.0-2", "x86_64"),
		"other arch":        block("foo", "1.0-1", "any"),
		"no name":           "Version : 1.0-1\nArchitecture : x86_64\n",
		"no arch":           "Name : foo\nVersion : 1.0-1\n",
		"empty":             "",
		"description trick": "Name : bar\nVersion : 1.0-1\nArchitecture : x86_64\nDescription : x\n Name : foo\n",
	} {
		fx := newLocalFx(t)
		fx.sys.info[name+"!"] = info
		arg, lf := fx.pkg(name, "foo bytes")
		code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
		fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)
		_ = what
	}
	// the first Name wins: a later one cannot rename the package
	fx := newLocalFx(t)
	fx.sys.info[name+"!"] = block("bar", "1.0-1", "x86_64") + "Name : foo\n"
	arg, lf := fx.pkg(name, "foo bytes")
	code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)

	// an architecture this pacman does not accept, though name and PKGINFO agree
	fx = newLocalFx(t)
	arg, lf = fx.pkg("foo-1.0-1-aarch64.pkg.tar.zst", "foo bytes")
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)

	// pacman cannot read the file
	fx = newLocalFx(t)
	fx.sys.info[name+"#exit"] = "not an archive"
	arg, lf = fx.pkg(name, "foo bytes")
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)

	// the same package twice under two file names
	fx = newLocalFx(t)
	a1, f1 := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "one")
	a2, f2 := fx.pkg("foo-2.0-1-x86_64.pkg.tar.zst", "two")
	code, evs = fx.run(PkgLocalHash([]LocalFile{f1, f2}), a1, a2)
	fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)

	// architectures: any, and what pacman-conf lists (auto: this machine's)
	fx = newLocalFx(t)
	fx.sys.arch = "x86_64\nx86_64_v3\n"
	arg, lf = fx.pkg("foo-1.0-1-x86_64_v3.pkg.tar.zst", "v3")
	if code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg); code != ExitOK {
		t.Errorf("x86_64_v3: exit %d: %s", code, resultErr(evs))
	}
	fx = newLocalFx(t)
	fx.sys.arch = "auto\n"
	fx.opts = func(o *Options) { o.uname = func() (string, error) { return "x86_64", nil } }
	arg, lf = fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "x")
	if code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg); code != ExitOK {
		t.Errorf("auto: exit %d: %s", code, resultErr(evs))
	}
	fx = newLocalFx(t)
	fx.sys.arch = ""
	arg, lf = fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "x")
	code, evs = fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	fx.wantRefused(code, evs, ExitRefused, ReasonBadPackage)
}

func TestLocalLogsInstallScript(t *testing.T) {
	fx := newLocalFx(t)
	name := "foo-1.0-1-x86_64.pkg.tar.zst"
	fx.sys.info[name] = "" // unused
	fx.sys.info[name+"!"] = "Name : foo\nVersion : 1.0-1\nArchitecture : x86_64\nDescription : \x1b[31mred\x1b]0;title\x07\nInstall Script  : Yes\n"
	arg, lf := fx.pkg(name, "foo bytes")
	code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	var all []string
	for _, e := range ofType(evs, EventLine) {
		all = append(all, e["text"].(string))
	}
	log := strings.Join(all, "\n")
	if !strings.Contains(log, "has an install script (.INSTALL): it runs as root") {
		t.Errorf("no install-script note:\n%s", log)
	}
	// a package's own text never carries an escape into the log
	if strings.ContainsRune(log, 0x1b) || strings.ContainsRune(log, 7) {
		t.Errorf("escape in the log: %q", log)
	}
}

// ---- protected, conflicts, pending updates, busy ---------------------------

func TestLocalProtectedConflictUpdates(t *testing.T) {
	type tc struct {
		name, file, extra, reason string
		names                     []string
		code                      int
	}
	for _, c := range []tc{
		{"protected name", "sudo-1.9-1-x86_64.pkg.tar.zst", "", ReasonProtected, []string{"sudo"}, ExitRefused},
		{"kernel", "linux-cachyos-6.1-1-x86_64.pkg.tar.zst", "", ReasonProtected, []string{"linux-cachyos"}, ExitRefused},
		{"archmaid prefix", "archmaid-bin-2-1-x86_64.pkg.tar.zst", "", ReasonProtected, []string{"archmaid-bin"}, ExitRefused},
		{"provides protected", "mysudo-1-1-x86_64.pkg.tar.zst", "Provides        : polkit=1\n", ReasonProtected, []string{"mysudo"}, ExitRefused},
		{"provides kernel", "mykernel-1-1-x86_64.pkg.tar.zst", "Provides        : linux-cachyos\n", ReasonProtected, []string{"mykernel"}, ExitRefused},
		{"base dep", "filesystem-1-1-any.pkg.tar.zst", "", ReasonProtected, []string{"filesystem"}, ExitRefused},
		{"conflicts installed", "foo-1.0-1-x86_64.pkg.tar.zst", "Conflicts With  : oldpkg\n", ReasonConflict, []string{"foo", "oldpkg"}, ExitRefused},
		{"replaces installed", "foo-1.0-1-x86_64.pkg.tar.zst", "Replaces        : oldpkg<2\n", ReasonConflict, []string{"foo", "oldpkg"}, ExitRefused},
	} {
		fx := newLocalFx(t)
		fx.sys.info[c.file] = c.extra
		// the fixture has Conflicts/Replaces/Provides "None" first; the
		// parser takes the first, so rewrite the block instead
		fx.sys.info[c.file+"!"] = blockWith(c.file, c.extra)
		arg, lf := fx.pkg(c.file, "bytes")
		code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg)
		fx.wantRefused(code, evs, c.code, c.reason)
		wantReason(t, evs, c.reason, c.names, 0)
		_ = c.name
	}
	// conflicting with something not installed, or itself, is fine
	fx := newLocalFx(t)
	fx.sys.info["foo-1.0-1-x86_64.pkg.tar.zst!"] = blockWith("foo-1.0-1-x86_64.pkg.tar.zst", "Conflicts With  : foo-git absent<1\nReplaces        : foo\n")
	arg, lf := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "bytes")
	if code, evs := fx.run(PkgLocalHash([]LocalFile{lf}), arg); code != ExitOK {
		t.Errorf("exit %d: %s", code, resultErr(evs))
	}

	// updates pending: exit 3, nothing copied, nothing parsed
	fx = newLocalFx(t)
	fx.sys.updates = []string{"glibc 2.40-1 -> 2.41-1", "bash 5.2-1 -> 5.3-1"}
	args, _ := fx.good()
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitStale, ReasonUpdatesPending)
	wantReason(t, evs, ReasonUpdatesPending, nil, 2)
	if _, err := os.Stat(fx.staging); err == nil {
		t.Error("staged during pending updates")
	}
}

func blockWith(file, extra string) string {
	m := localPkgPattern.FindStringSubmatch(file)
	b := fmt.Sprintf("Name            : %s\nVersion         : %s-%s\nArchitecture    : %s\n", m[1], m[2], m[3], m[4])
	for _, f := range []string{"Provides", "Conflicts With", "Replaces"} {
		if !strings.Contains(extra, f) {
			b += fmt.Sprintf("%-16s: None\n", f)
		}
	}
	return b + extra + "Install Script  : No\n"
}

func TestLocalBusy(t *testing.T) {
	// db.lck at the start
	fx := newLocalFx(t)
	args, _ := fx.good()
	os.WriteFile(filepath.Join(fx.anchor, "db.lck"), nil, 0o600)
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitBusy, ReasonBusy)
	if _, err := os.Stat(fx.staging); err == nil {
		t.Error("staged while pacman was running")
	}
	// db.lck appears while the package is read: nothing installed
	fx = newLocalFx(t)
	args, _ = fx.good()
	fx.sys.onUQ = func(argv []string) {
		if len(argv) > 1 && argv[1] == "-Qip" {
			os.WriteFile(filepath.Join(fx.anchor, "db.lck"), nil, 0o600)
		}
	}
	code, evs = fx.run(args...)
	fx.wantRefused(code, evs, ExitBusy, ReasonBusy)
	// another helper holds the lock
	fx = newLocalFx(t)
	args, _ = fx.good()
	o, _ := rootOpts(t, fx.sys)
	release, err := acquireLock(o.LockPath, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fx.opts = func(op *Options) { op.LockPath = o.LockPath }
	code, evs = fx.run(args...)
	fx.wantRefused(code, evs, ExitBusy, ReasonBusy)
}

// ---- staging ---------------------------------------------------------------

func TestLocalStagingCleanup(t *testing.T) {
	// success: checked in the happy path; failure of pacman:
	fx := newLocalFx(t)
	args, _ := fx.good()
	fx.sys.exit = 1
	code, evs := fx.run(args...)
	if code != ExitFailed {
		t.Errorf("exit %d: %s", code, resultErr(evs))
	}
	fx.noStagingLeft()

	// a panic in the middle of the install
	fx = newLocalFx(t)
	args, _ = fx.good()
	fx.sys.onStrm = func([]string) { panic("boom") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("no panic")
			}
		}()
		fx.run(args...)
	}()
	fx.noStagingLeft()

	// a panic while reading the package
	fx = newLocalFx(t)
	args, _ = fx.good()
	fx.sys.onUQ = func(argv []string) {
		if len(argv) > 1 && argv[1] == "-Qip" {
			panic("boom")
		}
	}
	func() {
		defer func() { recover() }()
		fx.run(args...)
	}()
	fx.noStagingLeft()

	// every refusal after the copy removes the copy (mismatch, bad package,
	// protected): covered in their tests via wantRefused.

	// what a killed run left is removed by the next one, and only "run-*"
	fx = newLocalFx(t)
	args, _ = fx.good()
	os.MkdirAll(filepath.Join(fx.staging, "run-dead", "sub"), 0o700)
	os.WriteFile(filepath.Join(fx.staging, "run-dead", "sub", "x.pkg.tar.zst"), []byte("old"), 0o600)
	if code, evs := fx.run(args...); code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	fx.noStagingLeft()

	// the staging folders are private and fresh
	fx = newLocalFx(t)
	args, _ = fx.good()
	fx.sys.onStrm = func(argv []string) {
		dir := filepath.Dir(argv[len(argv)-1])
		st, err := os.Stat(dir)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("staging folder %v %v", st, err)
		}
		if f, err := os.Stat(argv[len(argv)-1]); err != nil || f.Mode().Perm()&0o077 != 0 {
			t.Errorf("staged file %v %v", f, err)
		}
		if r, err := os.Stat(fx.staging); err != nil || r.Mode().Perm() != 0o700 {
			t.Errorf("staging root %v %v", r, err)
		}
	}
	if code, evs := fx.run(args...); code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
}

func TestLocalStagingRootMustBeSafe(t *testing.T) {
	for what, prep := range map[string]func(fx *localFx){
		"symlink": func(fx *localFx) {
			os.MkdirAll(filepath.Dir(fx.staging), 0o755)
			os.Symlink(t.TempDir(), fx.staging)
		},
		"group writable": func(fx *localFx) {
			os.MkdirAll(fx.staging, 0o700)
			os.Chmod(fx.staging, 0o770)
		},
		"a file": func(fx *localFx) {
			os.MkdirAll(filepath.Dir(fx.staging), 0o755)
			os.WriteFile(fx.staging, nil, 0o600)
		},
	} {
		fx := newLocalFx(t)
		args, _ := fx.good()
		prep(fx)
		code, evs := fx.run(args...)
		if code != ExitFailed || len(fx.sys.streams) != 0 {
			t.Errorf("%s: exit %d, ran %v (%s)", what, code, fx.sys.streams, resultErr(evs))
		}
	}
}

// TestLocalBytesInStagingAreTheirOwn: the file is read once. pacman gets
// paths only under the staging folder, whatever the argv named.
func TestLocalOnlyStagedPathsReachPacman(t *testing.T) {
	fx := newLocalFx(t)
	args, _ := fx.good()
	if code, evs := fx.run(args...); code != ExitOK {
		t.Fatalf("exit %d: %s", code, resultErr(evs))
	}
	for _, q := range fx.sys.queries {
		for _, a := range q {
			if strings.Contains(a, fx.home) {
				t.Errorf("pacman was given a path in the user's home: %q", q)
			}
		}
	}
	for _, a := range fx.sys.streams[0] {
		if strings.Contains(a, fx.home) {
			t.Errorf("pacman -U was given a path in the user's home: %q", fx.sys.streams[0])
		}
	}
}

// the result event of a stale run carries reason stale and exit 3, and no plan.
func TestLocalStaleHasNoPlan(t *testing.T) {
	fx := newLocalFx(t)
	arg, _ := fx.pkg("foo-1.0-1-x86_64.pkg.tar.zst", "x")
	code, evs := fx.run("sha256:"+strings.Repeat("a", 64), arg)
	fx.wantRefused(code, evs, ExitStale, ReasonStale)
	if len(ofType(evs, EventPlan)) != 0 {
		t.Error("a plan event for a local install")
	}
	var buf bytes.Buffer
	_ = buf
}

// The lead example, pinned as text: one file, size 7, sha abab...ab.
func TestPkgLocalHashLeadExample(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	sum := sha256.Sum256([]byte("archmaid pkg local v1\nfile a-1-1-any.pkg.tar.zst 7 " + sha + "\n"))
	if got := PkgLocalHash([]LocalFile{{"a-1-1-any.pkg.tar.zst", 7, sha}}); got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("hash %s", got)
	}
	l := []LocalFile{{"a-1-1-any.pkg.tar.zst", 7, sha}, {"a-1-1-any.pkg.tar.zst2", 1, sha}}
	if PkgLocalHash(l) != PkgLocalHash([]LocalFile{l[1], l[0]}) {
		t.Error("order matters")
	}
}

// A staged copy that is not what was copied (disk fault, a bug) is never installed.
func TestLocalReadBackMustMatch(t *testing.T) {
	fx := newLocalFx(t)
	args, _ := fx.good()
	fx.opts = func(o *Options) {
		o.afterStage = func(dir string) {
			os.WriteFile(filepath.Join(dir, "foo-1.0-1-x86_64.pkg.tar.zst"), []byte("other"), 0o600)
		}
	}
	code, evs := fx.run(args...)
	fx.wantRefused(code, evs, ExitFailed, "")
}

// A root caller is refused by caller() itself, not only by the owner checks.
func TestLocalCallerRoot(t *testing.T) {
	p := writePasswd(t, "root:x:0:0::/root:/bin/sh\nu:x:1000:1000::/home/u:/bin/sh\n")
	s := &session{o: Options{CallerUID: "0", PasswdPath: p}}
	if _, _, err := s.caller(); err == nil {
		t.Error("uid 0 accepted")
	}
	s.o.CallerUID = "1000"
	if u, h, err := s.caller(); err != nil || u != 1000 || h != "/home/u" {
		t.Errorf("%d %q %v", u, h, err)
	}
}
