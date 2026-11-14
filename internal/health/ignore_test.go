package health

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- part 1: "flagged out of date" is a note

func TestAURFlaggedNote(t *testing.T) {
	foreign := func(n ...string) func() ([]Foreign, error) {
		return func() ([]Foreign, error) {
			var out []Foreign
			for _, x := range n {
				out = append(out, Foreign{Name: x, Version: "1"})
			}
			return out, nil
		}
	}
	snap := func(n ...string) func() (map[string]bool, bool) {
		return func() (map[string]bool, bool) {
			m := map[string]bool{}
			for _, x := range n {
				m[x] = true
			}
			return m, true
		}
	}
	ago := func(d time.Duration) time.Time { return t0.Add(-d) }
	day := 24 * time.Hour
	for _, c := range []struct {
		name   string
		pk     []AURPkg
		names  []string
		state  State
		detail string
		items  int
	}{
		{"the owner's case: flagged 8 months ago", []AURPkg{{Name: "unigine-superposition", OutOfDate: true, Flagged: ago(245 * day)}},
			[]string{"unigine-superposition"}, OK, "1 AUR package fine · 1 flagged out of date: unigine-superposition (flagged 8 months ago)", 0},
		{"days and no age", []AURPkg{{Name: "a", OutOfDate: true, Flagged: ago(3 * day)}, {Name: "b", OutOfDate: true}},
			[]string{"a", "b"}, OK, "2 AUR packages fine · 2 flagged out of date: a (flagged 3 days ago), b", 0},
		{"hours and years", []AURPkg{{Name: "a", OutOfDate: true, Flagged: ago(5 * time.Hour)}, {Name: "b", OutOfDate: true, Flagged: ago(800 * day)}},
			[]string{"a", "b"}, OK, "2 AUR packages fine · 2 flagged out of date: a (flagged 5 hours ago), b (flagged 2 years ago)", 0},
		{"a flag dated in the future shows no age", []AURPkg{{Name: "a", OutOfDate: true, Flagged: t0.Add(day)}},
			[]string{"a"}, OK, "1 AUR package fine · 1 flagged out of date: a", 0},
		{"gone is still LOOK, with the note after it", []AURPkg{{Name: "a", OutOfDate: true, Flagged: ago(day)}},
			[]string{"a", "b"}, Look, "not in the AUR anymore: b · 1 flagged out of date: a (flagged 1 day ago)", 1},
		{"many names are cut", []AURPkg{{Name: "a", OutOfDate: true}, {Name: "b", OutOfDate: true}, {Name: "c", OutOfDate: true}, {Name: "d", OutOfDate: true}, {Name: "e", OutOfDate: true}},
			[]string{"a", "b", "c", "d", "e"}, OK, "5 AUR packages fine · 5 flagged out of date: a, b, c, d +1 more", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := Deps{Now: func() time.Time { return t0 }, Foreign: foreign(c.names...), AURSeen: snap(c.names...),
				AURInfo: func(context.Context, []string) ([]AURPkg, error) { return c.pk, nil }}
			got := checkAUR(context.Background(), d)
			if got.State != c.state || got.Detail != c.detail || len(got.Items) != c.items {
				t.Errorf("= %v %q items %v; want %v %q, %d items", got.State, got.Detail, got.Items, c.state, c.detail, c.items)
			}
		})
	}
}

// ---- part 2: items and ignoring

func unitsDeps(sys string) Deps {
	return Deps{Query: fakeQuery(sys, "", nil, nil)}
}

func TestItemsOfEveryLookCheck(t *testing.T) {
	ctx := context.Background()
	line := func(u string) string { return u + " loaded failed failed X\n" }
	// failed-units
	c := checkFailedUnits(ctx, Deps{Query: fakeQuery(line("a.service")+line("b.timer"), line("c.service"), nil, nil)})
	want := []Item{{"unit:a.service", "a.service"}, {"unit:b.timer", "b.timer"}, {"user-unit:c.service", "c.service (user)"}}
	if !reflect.DeepEqual(c.Items, want) {
		t.Errorf("units: %v, want %v", c.Items, want)
	}
	// restart: keyed by the installed kernel, so a newer one re-alerts
	rd := func(running string, rels ...string) Check {
		return checkRestart(ctx, Deps{RunningKernel: func() string { return running }, ModulesDir: modulesDir(t, rels...)})
	}
	if c := rd("7.2.10-1-cachyos", "7.2.12-1-cachyos"); len(c.Items) != 1 || c.Items[0].Key != "restart:7.2.12-1-cachyos" {
		t.Errorf("restart: %+v", c.Items)
	}
	if c := rd("7.2.10-1-cachyos", "7.2.13-1-cachyos"); c.Items[0].Key != "restart:7.2.13-1-cachyos" {
		t.Errorf("restart, a newer kernel: %+v", c.Items)
	}
	// time-sync, pacman-lock, mirror, news: one item each
	up := func() (time.Duration, error) { return time.Hour, nil }
	if c := checkTimeSync(ctx, Deps{Query: timedatectl("NTP=no\nNTPSynchronized=no\n", nil), Uptime: up}); len(c.Items) != 1 || c.Items[0].Key != "time-sync" {
		t.Errorf("time-sync: %+v", c.Items)
	}
	if c := checkTimeSync(ctx, Deps{Query: timedatectl("NTP=yes\nNTPSynchronized=no\n", nil), Uptime: up}); len(c.Items) != 1 || c.Items[0].Key != "time-sync" {
		t.Errorf("time-sync not synced: %+v", c.Items)
	}
	lock := Deps{LockFile: DefaultLockFile, Exists: func(string) bool { return true }, Procs: func() ([]string, error) { return nil, nil }}
	if c := checkPacmanLock(ctx, lock); len(c.Items) != 1 || c.Items[0].Key != "pacman-lock" {
		t.Errorf("pacman-lock: %+v", c.Items)
	}
	// crashes: one item per executable
	crash := "Mon 2026-11-09 10:02:11 CET 1 1000 1000 SIGSEGV present /usr/bin/firefox 1M\nMon 2026-11-09 10:02:11 CET 1 1000 1000 SIGSEGV present /usr/bin/steam 1M\n"
	if c := checkCrashes(ctx, Deps{Query: coredump(crash, nil)}); !reflect.DeepEqual(c.Items, []Item{{"crash:firefox", "firefox"}, {"crash:steam", "steam"}}) {
		t.Errorf("crashes: %+v", c.Items)
	}
	// disk: one item per low mount
	const G, M = 1 << 30, 1 << 20
	fs := map[string]FSInfo{"/": {4 * G, 100 * G, 1}, "/boot": {10 * M, 500 * M, 2}}
	c = checkDisk(ctx, Deps{FS: func(p string) (FSInfo, error) { return fs[p], nil }})
	if !reflect.DeepEqual(c.Items, []Item{{"disk:/", "/"}, {"disk:/boot", "/boot"}}) {
		t.Errorf("disk: %+v", c.Items)
	}
	fs["/"] = FSInfo{50 * G, 100 * G, 1}
	if c = checkDisk(ctx, Deps{FS: func(p string) (FSInfo, error) { return fs[p], nil }}); !reflect.DeepEqual(c.Items, []Item{{"disk:/boot", "/boot"}}) {
		t.Errorf("disk, only /boot: %+v", c.Items)
	}
	// every Look item key is valid
	for _, k := range []string{"unit:a.service", "restart:7.2.12-1", "pacnew:/etc/pacman.conf.pacnew", "aur-gone:foo", "news:1762500000", "mirror:192.168.1.189:8099", "drive:Samsung_SSD"} {
		if !ValidKey(k) {
			t.Errorf("%q is not a valid key", k)
		}
	}
}

func TestItemsPacnewAURDrivesNewsMirror(t *testing.T) {
	ctx := context.Background()
	// pacnew
	dir := t.TempDir()
	f := filepath.Join(dir, "pacman.conf.pacnew")
	os.WriteFile(f, nil, 0o644)
	log := filepath.Join(dir, "pacman.log")
	os.WriteFile(log, []byte("[2026-11-08T10:01:00+0100] [ALPM] warning: "+strings.TrimSuffix(f, ".pacnew")+" installed as "+f+"\n"), 0o644)
	c := checkPacnew(ctx, Deps{PacmanLog: log, Exists: func(p string) bool { _, err := os.Lstat(p); return err == nil }})
	if len(c.Items) != 1 || c.Items[0].Key != "pacnew:"+f {
		t.Errorf("pacnew: %+v", c.Items)
	}
	// news: keyed by the newest unread post's date
	var gets atomic.Int32
	nd := newsDeps(t, logHead, feed(item("Newest", "https://archlinux.org/news/n/", dNew), item("Mid", "", dMid)), nil, &gets)
	nc := checkNews(ctx, nd)
	if len(nc.Items) != 1 || nc.Items[0].Key != "news:1794039569" || nc.Items[0].Label != "Newest" {
		t.Errorf("news: %+v", nc.Items)
	}
	// mirror: keyed by the host checked
	list := mirrorList(t, "Server = https://mirror.example.com/archlinux/$repo/os/$arch\n")
	mc := checkMirror(ctx, Deps{Now: func() time.Time { return t0 }, MirrorList: list,
		MirrorGet: func(context.Context, string) ([]byte, error) {
			return []byte(itoa(t0.Add(-40 * time.Hour).Unix())), nil
		}})
	if len(mc.Items) != 1 || mc.Items[0].Key != "mirror:mirror.example.com" {
		t.Errorf("mirror: %+v", mc)
	}
}

// itemKey drops control characters and cuts on a rune boundary.
func TestItemKey(t *testing.T) {
	if got := itemKey("unit:", "a\x1b[31m\nb .service"); got != "unit:a[31mb.service" || !ValidKey(got) {
		t.Errorf("%q", got)
	}
	long := itemKey("pacnew:", strings.Repeat("é", 400))
	if len(long) > KeyMax || !ValidKey(long) {
		t.Errorf("len %d valid %v", len(long), ValidKey(long))
	}
	for _, k := range []string{"", strings.Repeat("a", KeyMax+1), "a\nb", "a\x00b", "\xff"} {
		if ValidKey(k) {
			t.Errorf("%q is valid", k)
		}
	}
}

// Run applies the ignore list: ignoring one of two failed units leaves the other
// Look, ignoring both makes the check Ignored and Attention 0, a new item
// re-alerts, and an Unknown or OK check is never touched.
func TestRunIgnoring(t *testing.T) {
	line := func(u string) string { return u + " loaded failed failed X\n" }
	run := func(sys string, ign map[string]time.Time) Report {
		return Run(context.Background(), Deps{Now: func() time.Time { return t0 }, Query: fakeQuery(sys, "", nil, nil),
			IgnoreLoad: func() map[string]time.Time { return ign }})
	}
	unitsOf := func(r Report) Check { return r.Checks[0] }
	two := line("a.service") + line("b.service")
	at := t0.Add(-time.Hour)

	r := run(two, nil)
	if c := unitsOf(r); c.State != Look || len(c.Items) != 2 || r.Attention() != 1 || len(r.Ignored()) != 0 {
		t.Fatalf("nothing ignored: %+v attention %d", c, r.Attention())
	}
	// one of two ignored: still Look for the other
	r = run(two, map[string]time.Time{"unit:a.service": at})
	c := unitsOf(r)
	if c.State != Look || !reflect.DeepEqual(c.Items, []Item{{"unit:b.service", "b.service"}}) ||
		!reflect.DeepEqual(c.IgnoredItems, []Item{{"unit:a.service", "a.service"}}) || !strings.HasSuffix(c.Detail, " · 1 ignored") || r.Attention() != 1 {
		t.Fatalf("one ignored: %+v attention %d", c, r.Attention())
	}
	// both ignored: Ignored, counts nowhere, listed for the Undo list
	r = run(two, map[string]time.Time{"unit:a.service": at, "unit:b.service": at, "unit:gone.service": at})
	c = unitsOf(r)
	if c.State != Ignored || c.Items != nil || len(c.IgnoredItems) != 2 || r.Attention() != 0 {
		t.Fatalf("all ignored: %+v attention %d", c, r.Attention())
	}
	ign := r.Ignored()
	if len(ign) != 2 || ign[0].CheckID != "failed-units" || ign[0].Title != TitleFailedUnits || ign[0].Item.Key != "unit:a.service" || !ign[0].Since.Equal(at) {
		t.Errorf("Ignored() = %+v", ign)
	}
	if c.Detail == "" || c.Copy == "" {
		t.Errorf("an ignored row keeps its detail and command: %+v", c)
	}
	// a new item re-alerts: c.service is not in the list
	r = run(two+line("c.service"), map[string]time.Time{"unit:a.service": at, "unit:b.service": at})
	c = unitsOf(r)
	if c.State != Look || !reflect.DeepEqual(c.Items, []Item{{"unit:c.service", "c.service"}}) || len(c.IgnoredItems) != 2 || r.Attention() != 1 {
		t.Fatalf("a new unit: %+v", c)
	}
	// an unrelated key changes nothing; OK stays OK
	r = run("", map[string]time.Time{"unit:a.service": at})
	if c := unitsOf(r); c.State != OK || c.Items != nil || c.IgnoredItems != nil {
		t.Errorf("OK check: %+v", c)
	}
	// a nil list ignores nothing
	if r := Run(context.Background(), Deps{Query: fakeQuery(two, "", nil, nil)}); r.Attention() != 1 {
		t.Errorf("no IgnoreLoad: attention %d", r.Attention())
	}
	// A Look check without items is ignorable by its check ID.
	got := applyIgnores(Check{ID: "x", Title: "X", State: Look, Detail: "d"}, map[string]time.Time{"x": at})
	if got.State != Ignored || len(got.IgnoredItems) != 1 || got.IgnoredItems[0].Key != "x" {
		t.Errorf("no items: %+v", got)
	}
	if got := applyIgnores(Check{ID: "x", State: Unknown}, map[string]time.Time{"x": at}); got.State != Unknown {
		t.Errorf("Unknown changed: %+v", got)
	}
	if Ignored.String() != "ignored" {
		t.Error("State string")
	}
}

// A different kernel re-alerts, the same one stays ignored.
func TestIgnoreRestartKeyedByKernel(t *testing.T) {
	run := func(installed string, ign map[string]time.Time) Check {
		r := Run(context.Background(), Deps{Now: func() time.Time { return t0 },
			RunningKernel: func() string { return "7.2.10-1-cachyos" }, ModulesDir: modulesDir(t, installed),
			IgnoreLoad: func() map[string]time.Time { return ign }})
		return r.Checks[1]
	}
	ign := map[string]time.Time{"restart:7.2.12-1-cachyos": t0}
	if c := run("7.2.12-1-cachyos", ign); c.State != Ignored {
		t.Errorf("same kernel: %+v", c)
	}
	if c := run("7.2.13-1-cachyos", ign); c.State != Look {
		t.Errorf("newer kernel: %+v", c)
	}
}

// ---- the store

func TestIgnoreStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state") // does not exist yet
	now := t0
	s := &IgnoreStore{Dir: dir, Now: func() time.Time { return now }}
	if got := s.Load(); len(got) != 0 {
		t.Fatalf("empty store: %v", got)
	}
	if err := s.Ignore("unit:a.service"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := s.Ignore("aur-gone:foo"); err != nil {
		t.Fatal(err)
	}
	// ignoring again keeps the first time
	now = now.Add(time.Hour)
	if err := s.Ignore("unit:a.service"); err != nil {
		t.Fatal(err)
	}
	got := (&IgnoreStore{Dir: dir}).Load() // a fresh store reads it back
	if len(got) != 2 || !got["unit:a.service"].Equal(t0) || !got["aur-gone:foo"].Equal(t0.Add(time.Hour)) {
		t.Fatalf("loaded %v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, IgnoreFile))
	if !strings.HasPrefix(string(raw), `{"v":1,"ignored":{`) || !strings.Contains(string(raw), `"unit:a.service":"2026-11-09T12:00:00Z"`) {
		t.Errorf("file %s", raw)
	}
	// the dir 0700, the file 0600, no temp file left behind
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(dir, IgnoreFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("stray files: %v", ents)
	}
	// Unignore
	if err := s.Unignore("unit:a.service"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); len(got) != 1 || got["aur-gone:foo"].IsZero() {
		t.Errorf("after Unignore: %v", got)
	}
	if err := s.Unignore("never-there"); err != nil {
		t.Errorf("unignoring a key that is not there: %v", err)
	}
	if err := s.Unignore("aur-gone:foo"); err != nil || len(s.Load()) != 0 {
		t.Errorf("last key: %v %v", err, s.Load())
	}
	// bad keys are refused and nothing is written
	for _, k := range []string{"", "a\nb", strings.Repeat("k", KeyMax+1)} {
		if err := s.Ignore(k); err != ErrBadKey {
			t.Errorf("Ignore(%q) = %v", k, err)
		}
	}
	// the default store points at the (isolated) state dir
	if p := DefaultIgnoreStore().path(); !strings.HasSuffix(p, filepath.Join("archmaid", IgnoreFile)) {
		t.Errorf("default path %q", p)
	}
}

func TestIgnoreStoreHostileFile(t *testing.T) {
	write := func(dir, body string, mode os.FileMode) string {
		p := filepath.Join(dir, IgnoreFile)
		os.Remove(p)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, mode)
		return p
	}
	good := `{"v":1,"ignored":{"unit:a.service":"2026-11-09T12:00:00Z"}}`
	for _, c := range []struct {
		name, body string
		mode       os.FileMode
		want       int
	}{
		{"fine", good, 0o600, 1},
		{"garbage", "not json", 0o600, 0},
		{"null", "null", 0o600, 0},
		{"empty", "", 0o600, 0},
		{"another version", `{"v":2,"ignored":{"a":"2026-11-09T12:00:00Z"}}`, 0o600, 0},
		{"group writable", good, 0o660, 0},
		{"world writable", good, 0o666, 0},
		{"huge", `{"v":1,"ignored":{"a":"` + strings.Repeat("x", maxIgnoreFile) + `"}}`, 0o600, 0},
		{"bad keys are dropped, bad times kept as zero", `{"v":1,"ignored":{"":"2026-11-09T12:00:00Z","a\nb":"2026-11-09T12:00:00Z","ok":"yesterday"}}`, 0o600, 1},
		{"wrong shape", `{"v":1,"ignored":["a"]}`, 0o600, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(dir, c.body, c.mode)
			if got := (&IgnoreStore{Dir: dir}).Load(); len(got) != c.want {
				t.Errorf("Load = %v, want %d keys", got, c.want)
			}
		})
	}
	// A symlink is never followed: Load reads empty, and Ignore replaces the
	// link itself, leaving what it pointed at untouched.
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	os.WriteFile(target, []byte(good), 0o600)
	if err := os.Symlink(target, filepath.Join(dir, IgnoreFile)); err != nil {
		t.Fatal(err)
	}
	s := &IgnoreStore{Dir: dir}
	if got := s.Load(); len(got) != 0 {
		t.Fatalf("a symlinked file was read: %v", got)
	}
	if err := s.Ignore("unit:b.service"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != good {
		t.Errorf("the symlink's target was written through: %s", b)
	}
	if fi, _ := os.Lstat(filepath.Join(dir, IgnoreFile)); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the symlink is still there")
	}
	if got := s.Load(); len(got) != 1 || got["unit:b.service"].IsZero() {
		t.Errorf("after replacing: %v", got)
	}
	// A directory where the file should be is not read and not removed.
	dir2 := t.TempDir()
	os.Mkdir(filepath.Join(dir2, IgnoreFile), 0o700)
	if got := (&IgnoreStore{Dir: dir2}).Load(); len(got) != 0 {
		t.Errorf("a directory was read: %v", got)
	}
	// The state dir being a file is an error, not a panic.
	f := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(f, nil, 0o600)
	if err := (&IgnoreStore{Dir: f}).Ignore("x"); err == nil {
		t.Error("ignoring into a file path")
	}
	// A relative or empty state dir stores nothing.
	if got := (&IgnoreStore{Dir: "relative/dir"}).Load(); len(got) != 0 {
		t.Errorf("relative dir: %v", got)
	}
	if err := (&IgnoreStore{Dir: "relative/dir"}).Ignore("x"); err == nil {
		t.Error("relative dir accepted")
	}
	// The cap.
	dir3 := t.TempDir()
	s3 := &IgnoreStore{Dir: dir3}
	var sb strings.Builder
	sb.WriteString(`{"v":1,"ignored":{`)
	for i := 0; i < MaxIgnored; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`"k` + itoa(int64(i)) + `":"2026-11-09T12:00:00Z"`)
	}
	sb.WriteString(`}}`)
	write(dir3, sb.String(), 0o600)
	if len(s3.Load()) != MaxIgnored {
		t.Fatalf("cap fixture: %d", len(s3.Load()))
	}
	if err := s3.Ignore("one-more"); err != ErrIgnoreFull {
		t.Errorf("Ignore past the cap = %v", err)
	}
}
