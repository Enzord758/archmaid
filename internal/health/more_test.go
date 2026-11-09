package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/pkgdb/pkgdbtest"
)

// ---- pacman-lock

func TestPacmanLock(t *testing.T) {
	has := func(b bool) func(string) bool { return func(p string) bool { return b && p == DefaultLockFile } }
	procs := func(n ...string) func() ([]string, error) { return func() ([]string, error) { return n, nil } }
	for _, c := range []struct {
		name   string
		d      Deps
		state  State
		detail string
		copy   string
	}{
		{"no lock", Deps{LockFile: DefaultLockFile, Exists: has(false), Procs: procs()}, OK, "No lock file", ""},
		{"leftover", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("zsh", "firefox")}, Look,
			"pacman's lock is left over from a run that stopped", "sudo rm /var/lib/pacman/db.lck"},
		{"pacman running", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("zsh", "pacman")}, OK, "pacman is running", ""},
		{"yay running", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("yay")}, OK, "pacman is running", ""},
		{"paru", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("paru")}, OK, "pacman is running", ""},
		{"makepkg", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("makepkg")}, OK, "pacman is running", ""},
		{"pamac daemon", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("pamac-daemon")}, OK, "pacman is running", ""},
		{"look-alike names are not pacman", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: procs("pacman-key", "mypacman", "yay-bin")}, Look,
			"pacman's lock is left over from a run that stopped", "sudo rm /var/lib/pacman/db.lck"},
		{"process list fails", Deps{LockFile: DefaultLockFile, Exists: has(true), Procs: func() ([]string, error) { return nil, errors.New("x") }}, Unknown, "can't list processes", ""},
		{"no process lister", Deps{LockFile: DefaultLockFile, Exists: has(true)}, Unknown, "can't list processes", ""},
		{"no file check", Deps{LockFile: DefaultLockFile}, Unknown, "can't check the lock", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkPacmanLock(context.Background(), c.d)
			if got.State != c.state || got.Detail != c.detail || got.Copy != c.copy {
				t.Errorf("= %v %q %q; want %v %q %q", got.State, got.Detail, got.Copy, c.state, c.detail, c.copy)
			}
		})
	}
}

func TestRealProcsAndUptime(t *testing.T) {
	names, err := listProcs()
	if err != nil || len(names) == 0 {
		t.Fatalf("%v %v", names, err)
	}
	if up, err := readUptime(); err != nil || up <= 0 {
		t.Errorf("%v %v", up, err)
	}
	for in, ok := range map[string]bool{"123.45 678.9\n": true, "": false, "x y": false, "-5 1": false, "1e99 1": false} {
		if _, err := parseUptime(in); (err == nil) != ok {
			t.Errorf("parseUptime(%q) err %v", in, err)
		}
	}
}

// ---- time-sync

func timedatectl(out string, err error) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "timedatectl" || !reflect.DeepEqual(args, []string{"show", "-p", "NTP", "-p", "NTPSynchronized"}) {
			return nil, errors.New("unexpected call")
		}
		return []byte(out), err
	}
}

func TestTimeSync(t *testing.T) {
	up := func(d time.Duration) func() (time.Duration, error) {
		return func() (time.Duration, error) { return d, nil }
	}
	for _, c := range []struct {
		name   string
		out    string
		err    error
		up     func() (time.Duration, error)
		state  State
		detail string
	}{
		{"synced", "NTP=yes\nNTPSynchronized=yes\n", nil, up(time.Hour), OK, "Clock is synchronized"},
		{"ntp off", "NTP=no\nNTPSynchronized=no\n", nil, up(time.Hour), Look, "Automatic time sync is off"},
		{"ntp off right after boot still counts", "NTP=no\nNTPSynchronized=yes\n", nil, up(time.Minute), Look, "Automatic time sync is off"},
		{"not synced after 10 min", "NTP=yes\nNTPSynchronized=no\n", nil, up(11 * time.Minute), Look, "Clock isn't synchronized"},
		{"not synced yet, just booted", "NTP=yes\nNTPSynchronized=no\n", nil, up(3 * time.Minute), OK, "Waiting for the first sync"},
		{"exactly 10 min", "NTP=yes\nNTPSynchronized=no\n", nil, up(10 * time.Minute), OK, "Waiting for the first sync"},
		{"uptime unreadable", "NTP=yes\nNTPSynchronized=no\n", nil, func() (time.Duration, error) { return 0, errors.New("x") }, Unknown, "can't read uptime"},
		{"timedatectl fails", "", errors.New("exec: not found"), up(time.Hour), Unknown, "can't read the clock state"},
		{"garbage", "hello\n=\nNTP\n", nil, up(time.Hour), Unknown, "can't read the clock state"},
		{"odd values", "NTP=maybe\nNTPSynchronized=yes\n", nil, up(time.Hour), Unknown, "can't read the clock state"},
		{"missing key", "NTP=yes\n", nil, up(time.Hour), Unknown, "can't read the clock state"},
		{"exit error with output is still read", "NTP=yes\nNTPSynchronized=yes\n", errors.New("exit status 1"), up(time.Hour), OK, "Clock is synchronized"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkTimeSync(context.Background(), Deps{Query: timedatectl(c.out, c.err), Uptime: c.up})
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
		})
	}
	if got := checkTimeSync(context.Background(), Deps{}); got.State != Unknown {
		t.Error("no Query")
	}
	if got := checkTimeSync(context.Background(), Deps{Query: timedatectl("NTP=yes\nNTPSynchronized=no\n", nil)}); got.State != Unknown {
		t.Error("no Uptime")
	}
	// A huge output is cut before parsing: the keys past the cut are not seen.
	huge := strings.Repeat("x=y\n", 5000) + "NTP=yes\nNTPSynchronized=yes\n"
	if got := checkTimeSync(context.Background(), Deps{Query: timedatectl(huge, nil)}); got.State != Unknown {
		t.Errorf("huge: %+v", got)
	}
}

// ---- crashes

func coredump(out string, err error) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "coredumpctl" || !reflect.DeepEqual(args, []string{"list", "--since=-24h", "--no-pager", "--no-legend", "-q"}) {
			return nil, errors.New("unexpected call")
		}
		return []byte(out), err
	}
}

// exitErr is a real *exec.ExitError with the given status.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var e *exec.ExitError
	if !errors.As(err, &e) {
		t.Fatalf("no exit error: %v", err)
	}
	return err
}

func TestCrashes(t *testing.T) {
	line := func(exe string) string {
		return "Mon 2026-11-09 10:02:11 CET 12345 1000 1000 SIGSEGV present " + exe + " 1.2M\n"
	}
	var many strings.Builder
	for i := 0; i < 250; i++ {
		many.WriteString(line(fmt.Sprintf("/usr/bin/p%d", i)))
	}
	for _, c := range []struct {
		name   string
		out    string
		err    error
		state  State
		detail string
	}{
		{"none, exit 1", "", exitErr(t, 1), OK, "No crashes in the last 24 h"},
		{"none, exit 0", "", nil, OK, "No crashes in the last 24 h"},
		{"one", line("/usr/bin/firefox"), nil, Look, "1 crash in 24 h: firefox"},
		{"three, repeats merged in the names", line("/usr/bin/firefox") + line("/usr/lib/steam/steam") + line("/usr/bin/firefox"), nil, Look,
			"3 crashes in 24 h: firefox, steam"},
		{"names capped", line("/a") + line("/b") + line("/c") + line("/d") + line("/e"), nil, Look, "5 crashes in 24 h: a, b, c +2 more"},
		{"signal as lowercase number-like column is not a line", "Mon 2026-11-09 10:02:11 CET 1 2 3 11 present /usr/bin/x 1K\n", nil, OK, "No crashes in the last 24 h"},
		{"line cap", many.String(), nil, Look, "200+ crashes in 24 h: p0, p1, p2 +197 more"},
		{"tool missing", "", errors.New(`exec: "coredumpctl": executable file not found in $PATH`), Unknown, "can't read the crash list"},
		{"exit 2", "", exitErr(t, 2), Unknown, "can't read the crash list"},
		{"garbage", "hello\n\x00\x01\n", nil, OK, "No crashes in the last 24 h"},
		{"hostile exe name", line("/usr/bin/\x1b[31mevil"), nil, Look, "1 crash in 24 h: [31mevil"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkCrashes(context.Background(), Deps{Query: coredump(c.out, c.err)})
			want := c.detail
			if got.State != c.state || (c.name != "hostile exe name" && got.Detail != want) {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, want)
			}
			if got.State == Look && got.Copy != "coredumpctl list --since=-24h" {
				t.Errorf("copy %q", got.Copy)
			}
			if strings.ContainsRune(got.Detail, 0x1b) {
				t.Errorf("escape kept: %q", got.Detail)
			}
		})
	}
	if got := checkCrashes(context.Background(), Deps{}); got.State != Unknown {
		t.Error("no Query")
	}
	// Output past the cap is not parsed: a crash line after 300 KiB of junk is not seen.
	junk := strings.Repeat("junk junk junk\n", 30000) + line("/usr/bin/late")
	if got := checkCrashes(context.Background(), Deps{Query: coredump(junk, nil)}); got.State != OK {
		t.Errorf("late line past the cap was parsed: %+v", got)
	}
}

// ---- aur

func TestAUR(t *testing.T) {
	foreign := func(n ...string) func() ([]Foreign, error) {
		return func() ([]Foreign, error) {
			var out []Foreign
			for _, x := range n {
				out = append(out, Foreign{Name: x, Version: "1"})
			}
			return out, nil
		}
	}
	info := func(pk ...AURPkg) func(context.Context, []string) ([]AURPkg, error) {
		return func(_ context.Context, names []string) ([]AURPkg, error) { return pk, nil }
	}
	seen := func(n ...string) func() (map[string]bool, bool) {
		return func() (map[string]bool, bool) {
			m := map[string]bool{}
			for _, x := range n {
				m[x] = true
			}
			return m, true
		}
	}
	for _, c := range []struct {
		name   string
		d      Deps
		state  State
		detail string
	}{
		{"none installed", Deps{Foreign: foreign(), AURInfo: info()}, OK, "No AUR packages"},
		{"only debug packages", Deps{Foreign: foreign("yay-debug"), AURInfo: info()}, OK, "No AUR packages"},
		{"all current", Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a"}, AURPkg{Name: "b"}), AURSeen: seen("a", "b")}, OK, "2 AUR packages fine"},
		// The live VM case (#273): archmaid-git is a local build, never in the
		// AUR and never in the audit's snapshot. It must not be "gone".
		{"local builds are not gone", Deps{Foreign: foreign("archmaid-git", "archmaid-gui-git", "a"), AURInfo: info(AURPkg{Name: "a"}),
			AURSeen: seen("a")}, OK, "1 AUR package fine · 2 local builds"},
		{"local builds only", Deps{Foreign: foreign("archmaid-git"), AURInfo: info(), AURSeen: seen("a")}, OK, "No AUR packages found · 1 local build"},
		{"snapshot package missing from the AUR", Deps{Foreign: foreign("a", "b", "c"), AURInfo: info(AURPkg{Name: "a"}), AURSeen: seen("a", "b", "c")},
			Look, "not in the AUR anymore: b, c"},
		{"gone and local mixed", Deps{Foreign: foreign("a", "b", "mine"), AURInfo: info(AURPkg{Name: "a"}), AURSeen: seen("a", "b")},
			Look, "not in the AUR anymore: b"},
		{"no snapshot: no gone verdicts", Deps{Foreign: foreign("a", "b", "c"), AURInfo: info(AURPkg{Name: "a"})},
			OK, "1 AUR package fine · 2 not found on the AUR (no audit history to say if they were removed)"},
		{"unreadable snapshot is no snapshot", Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a"}), AURSeen: func() (map[string]bool, bool) { return nil, false }},
			OK, "1 AUR package fine · 1 not found on the AUR (no audit history to say if it was removed)"},
		{"no snapshot, flagged still shows", Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true})}, Look, "flagged out of date: a"},
		{"flagged", Deps{Foreign: foreign("a", "c"), AURInfo: info(AURPkg{Name: "a"}, AURPkg{Name: "c", OutOfDate: true}), AURSeen: seen("a", "c")}, Look, "flagged out of date: c"},
		{"both", Deps{Foreign: foreign("a", "b", "c"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}), AURSeen: seen("a", "b", "c")}, Look, "not in the AUR anymore: b, c · flagged out of date: a"},
		{"many gone", Deps{Foreign: foreign("a", "b", "c", "d", "e", "f"), AURInfo: info(), AURSeen: seen("a", "b", "c", "d", "e", "f")}, Look, "not in the AUR anymore: a, b, c, d +2 more"},
		{"rpc error", Deps{Foreign: foreign("a"), AURInfo: func(context.Context, []string) ([]AURPkg, error) { return nil, errors.New("offline") }}, Unknown, "Can't reach the AUR"},
		{"rpc timeout", Deps{Foreign: foreign("a"), AURInfo: func(context.Context, []string) ([]AURPkg, error) { return nil, context.DeadlineExceeded }}, Unknown, "AUR check timed out"},
		{"local db unreadable", Deps{Foreign: func() ([]Foreign, error) { return nil, errors.New("x") }, AURInfo: info()}, Unknown, "can't read installed packages"},
		{"no seams", Deps{}, Unknown, "can't read installed packages"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkAUR(context.Background(), c.d)
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
		})
	}
}

// The snapshot is read through internal/audit, read-only: a real snapshot file
// gives its names, a missing, corrupt or empty-path one gives "no snapshot",
// and nothing is written or renamed (a corrupt file stays as it is).
func TestSeenIn(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"archmaid-bin": {"maintainer": "alice"}, "old": "bob"}`), 0o600)
	seen, ok := seenIn(good)
	if !ok || !seen["archmaid-bin"] || !seen["old"] || seen["archmaid-git"] || len(seen) != 2 {
		t.Errorf("%v %v", seen, ok)
	}
	if _, ok := seenIn(filepath.Join(dir, "missing.json")); ok {
		t.Error("a missing snapshot counted")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	if _, ok := seenIn(bad); ok {
		t.Error("a corrupt snapshot counted")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Errorf("health wrote or renamed files: %v", ents)
	}
	if b, _ := os.ReadFile(bad); string(b) != "{not json" {
		t.Error("the corrupt snapshot was changed")
	}
	// The default reads the state dir of the (isolated) HOME: none there.
	if _, ok := auditSeen(); ok {
		t.Error("auditSeen found a snapshot in the test HOME")
	}
}

// What is asked: legal names only, no -debug packages, sorted, no repeats, at
// most MaxForeign (an illegal name must never reach the request).
func TestAURCandidates(t *testing.T) {
	var f []Foreign
	for _, n := range []string{"zed", "alpha", "alpha", "foo-debug", "bad name", "x;rm", "", "-lead", "ok_1.2+x"} {
		f = append(f, Foreign{Name: n})
	}
	if got := aurCandidates(f); !reflect.DeepEqual(got, []string{"alpha", "ok_1.2+x", "zed"}) {
		t.Errorf("%v", got)
	}
	f = nil
	for i := 0; i < 1000; i++ {
		f = append(f, Foreign{Name: fmt.Sprintf("p%04d", i)})
	}
	if got := aurCandidates(f); len(got) != MaxForeign {
		t.Errorf("%d names", len(got))
	}
}

func TestForeignAndAURInfoAt(t *testing.T) {
	dir := t.TempDir()
	pkgdbtest.WriteConf(t, filepath.Join(dir, "pacman.conf"), "core")
	pkgdbtest.WriteSync(t, filepath.Join(dir, "sync"), "core", true, pkgdb.Package{Name: "bash", Version: "5-1"})
	pkgdbtest.WriteLocal(t, filepath.Join(dir, "local"),
		pkgdb.Package{Name: "bash", Version: "5-1", Reason: pkgdb.ReasonExplicit},
		pkgdb.Package{Name: "yay", Version: "12-1", Reason: pkgdb.ReasonExplicit})
	got, err := foreignAt(pkgdb.Paths{Sync: filepath.Join(dir, "sync"), Local: filepath.Join(dir, "local"), Conf: filepath.Join(dir, "pacman.conf")})
	if err != nil || len(got) != 1 || got[0].Name != "yay" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := foreignAt(pkgdb.Paths{Sync: filepath.Join(dir, "nope"), Local: filepath.Join(dir, "nope")}); err == nil {
		t.Log("an empty database is not an error") // pkgdb decides; only the happy path is pinned
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": 5, "type": "multiinfo", "resultcount": 2, "results": []map[string]any{
			{"Name": "yay", "PackageBase": "yay", "Version": "12-1", "OutOfDate": 1700000000},
			{"Name": "other", "PackageBase": "other", "Version": "1-1"},
			{"Name": "unasked", "PackageBase": "unasked", "Version": "1-1"},
		}})
	}))
	defer srv.Close()
	pk, err := aurInfoAt(context.Background(), srv.URL, []string{"yay", "other"})
	if err != nil || len(pk) != 2 || !pk[0].OutOfDate || pk[1].OutOfDate {
		t.Errorf("%+v %v", pk, err)
	}
	if _, err := aurInfoAt(context.Background(), "", []string{"yay"}); err == nil {
		t.Error("empty base must fail")
	}
}

// ---- mirror

func mirrorList(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mirrorlist")
	os.WriteFile(p, []byte(s), 0o644)
	return p
}

func TestFirstMirror(t *testing.T) {
	for _, c := range []struct {
		name, list, base, host string
		bad                    bool
	}{
		{"plain", "Server = https://mirror.example.com/archlinux/$repo/os/$arch\n", "https://mirror.example.com/archlinux", "mirror.example.com", false},
		{"comments and blank lines first", "## Generated\n\n#Server = https://no.example/$repo/os/$arch\n  \nServer=http://lan.local:8099/arch/$repo/os/$arch\nServer = https://second/$repo\n",
			"http://lan.local:8099/arch", "lan.local:8099", false},
		{"geo mirror", "Server = https://geo.mirror.pkgbuild.com/$repo/os/$arch\n", "https://geo.mirror.pkgbuild.com", "geo.mirror.pkgbuild.com", false},
		{"trailing comment", "Server = https://m.example/arch/$repo/os/$arch # fast\n", "https://m.example/arch", "m.example", false},
		{"no servers", "# only comments\n", "", "", true},
		{"empty", "", "", "", true},
		{"no $repo", "Server = https://m.example/arch\n", "", "", true},
		{"starts with $repo", "Server = /$repo/os\n", "", "", true},
		{"ftp", "Server = ftp://m.example/$repo/os/$arch\n", "", "", true},
		{"file", "Server = file:///srv/$repo\n", "", "", true},
		{"javascript", "Server = javascript:alert(1)//$repo\n", "", "", true},
		{"user info", "Server = https://user:pw@m.example/$repo/os\n", "", "", true},
		{"query", "Server = https://m.example/x?y=1/$repo\n", "", "", true},
		{"no host", "Server = https:///$repo\n", "", "", true},
		{"space in url", "Server = https://m.example/a b/$repo\n", "", "", true},
		{"huge line", "Server = https://m.example/" + strings.Repeat("a", 100000) + "/$repo\n", "", "", true},
		{"garbage bytes", "\x00\x01\xff\n=\n==\n", "", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, host, err := firstMirror(mirrorList(t, c.list))
			if (err != nil) != c.bad || base != c.base || host != c.host {
				t.Errorf("= %q %q %v; want %q %q bad=%v", base, host, err, c.base, c.host, c.bad)
			}
		})
	}
	if _, _, err := firstMirror(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Error("missing file")
	}
}

func TestParseLastSync(t *testing.T) {
	for in, ok := range map[string]bool{
		"1762680000\n": true, " 1762680000 ": true,
		"": false, "-1762680000": false, "0": false, "1762680000.5": false, "abc": false, "1e9": false, "0x10": false,
		"99999999999999999999999": false, "9999999999": false, "946684799": false, "1762680000\n1762680001": false, "+1762680000": false,
	} {
		if _, got := parseLastSync(in); got != ok {
			t.Errorf("parseLastSync(%q) = %v, want %v", in, got, ok)
		}
	}
}

func TestMirror(t *testing.T) {
	now := t0
	list := mirrorList(t, "Server = https://mirror.example.com/archlinux/$repo/os/$arch\n")
	get := func(body string, err error, urls *[]string) func(context.Context, string) ([]byte, error) {
		return func(_ context.Context, u string) ([]byte, error) {
			if urls != nil {
				*urls = append(*urls, u)
			}
			return []byte(body), err
		}
	}
	ts := func(ago time.Duration) string { return fmt.Sprint(now.Add(-ago).Unix()) }
	for _, c := range []struct {
		name   string
		body   string
		err    error
		state  State
		detail string
	}{
		{"fresh", ts(2 * time.Hour), nil, OK, "mirror.example.com synced 2 h ago"},
		{"exactly 24 h", ts(24 * time.Hour), nil, OK, "mirror.example.com synced 24 h ago"},
		{"31 h behind", ts(31 * time.Hour), nil, Look, "mirror.example.com synced 31 h ago"},
		{"days behind", ts(100 * time.Hour), nil, Look, "mirror.example.com synced 4 d ago"},
		{"a little in the future is fine", ts(-30 * time.Minute), nil, OK, "mirror.example.com synced under a minute ago"},
		{"far in the future", ts(-5 * time.Hour), nil, Unknown, "mirror.example.com lastsync is in the future"},
		{"negative", "-5", nil, Unknown, "mirror.example.com has no usable lastsync"},
		{"zero", "0", nil, Unknown, "mirror.example.com has no usable lastsync"},
		{"garbage", "<html>404</html>", nil, Unknown, "mirror.example.com has no usable lastsync"},
		{"huge number", strings.Repeat("9", 40), nil, Unknown, "mirror.example.com has no usable lastsync"},
		{"fetch fails", "", errors.New("404"), Unknown, "mirror.example.com has no lastsync"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var urls []string
			got := checkMirror(context.Background(), Deps{Now: func() time.Time { return now }, MirrorList: list, MirrorGet: get(c.body, c.err, &urls)})
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
			if !reflect.DeepEqual(urls, []string{"https://mirror.example.com/archlinux/lastsync"}) {
				t.Errorf("fetched %v: only <base>/lastsync may be asked", urls)
			}
		})
	}
	bad := Deps{Now: func() time.Time { return now }, MirrorList: mirrorList(t, "# nothing\n"), MirrorGet: get("1", nil, nil)}
	if got := checkMirror(context.Background(), bad); got.State != Unknown || got.Detail != "can't read the mirrorlist" {
		t.Errorf("%+v", got)
	}
	if got := checkMirror(context.Background(), Deps{}); got.State != Unknown {
		t.Error("no seams")
	}
}

// The real getter for the mirror: 64-byte cap, 5 s, same-host redirects.
func TestMirrorGetLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok/lastsync":
			w.Write([]byte("1762680000\n"))
		case "/big/lastsync":
			w.Write(make([]byte, MirrorBodyMax+1))
		}
	}))
	defer srv.Close()
	d := DefaultDeps()
	if b, err := d.MirrorGet(context.Background(), srv.URL+"/ok/lastsync"); err != nil || string(b) != "1762680000\n" {
		t.Errorf("%q %v", b, err)
	}
	if _, err := d.MirrorGet(context.Background(), srv.URL+"/big/lastsync"); err == nil {
		t.Error("a body over 64 bytes was accepted")
	}
	if _, err := d.MirrorGet(context.Background(), srv.URL+"/none/lastsync"); err != nil {
		t.Log("empty 200 body is fine here")
	}
}

// ---- disk-health

// drivesJSON builds busctl's --json=short reply for the objects given.
func drivesJSON(objs map[string]map[string]map[string]any) string {
	conv := map[string]any{}
	for path, ifs := range objs {
		m := map[string]any{}
		for name, props := range ifs {
			pm := map[string]any{}
			for k, v := range props {
				typ := "s"
				switch v.(type) {
				case bool:
					typ = "b"
				case int, int64:
					typ = "x"
				case []string:
					typ = "as"
				}
				pm[k] = map[string]any{"type": typ, "data": v}
			}
			m[name] = pm
		}
		conv[path] = m
	}
	b, _ := json.Marshal(map[string]any{"type": "a{oa{sa{sv}}}", "data": []any{conv}})
	return string(b)
}

type P = map[string]any

func TestDiskHealth(t *testing.T) {
	ataOK := P{"SmartSupported": true, "SmartUpdated": 1762680000, "SmartFailing": false, "SmartNumBadSectors": 0, "SmartNumAttributesFailing": 0}
	drv := func(model string) P { return P{"Model": model} }
	obj := func(model string, extra map[string]P) map[string]map[string]any {
		m := map[string]map[string]any{ifaceDrive: drv(model)}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	query := func(out string, err error) func(context.Context, string, ...string) ([]byte, error) {
		return func(_ context.Context, name string, args ...string) ([]byte, error) {
			want := append([]string{}, driveCmd...)
			if name != "busctl" || !reflect.DeepEqual(args, want) {
				return nil, errors.New("unexpected call")
			}
			return []byte(out), err
		}
	}
	for _, c := range []struct {
		name   string
		out    string
		err    error
		state  State
		detail string
	}{
		{"ata fine", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("Samsung SSD 870", map[string]P{ifaceAta: ataOK})}),
			nil, OK, "SMART is fine: Samsung SSD 870"},
		{"ata failing", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("WD Blue", map[string]P{ifaceAta: {"SmartSupported": true, "SmartUpdated": 5, "SmartFailing": true}})}),
			nil, Look, "WD Blue: SMART says it's failing"},
		{"ata bad sectors", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("WD Blue", map[string]P{ifaceAta: {"SmartSupported": true, "SmartUpdated": 5, "SmartNumBadSectors": 8}})}),
			nil, Look, "WD Blue: 8 bad sectors"},
		{"ata attributes failing", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("WD Blue", map[string]P{ifaceAta: {"SmartSupported": true, "SmartUpdated": 5, "SmartNumAttributesFailing": 2}})}),
			nil, Look, "WD Blue: 2 SMART attributes failing"},
		{"ata never read: defaults say nothing", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("WD Blue", map[string]P{ifaceAta: {"SmartSupported": true, "SmartUpdated": 0, "SmartFailing": true}})}),
			nil, Unknown, "no SMART data yet"},
		{"ata not supported", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/A": obj("USB stick", map[string]P{ifaceAta: {"SmartSupported": false}})}),
			nil, Unknown, "no SMART data yet"},
		{"nvme fine", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/N": obj("Samsung 990 PRO", map[string]P{ifaceNVMe: {"SmartUpdated": 1762680000, "SmartCriticalWarning": []string{}}})}),
			nil, OK, "SMART is fine: Samsung 990 PRO"},
		{"nvme critical", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/N": obj("Samsung 990 PRO", map[string]P{ifaceNVMe: {"SmartUpdated": 1, "SmartCriticalWarning": []string{"spare", "temperature"}}})}),
			nil, Look, "Samsung 990 PRO: critical warning: spare, temperature"},
		{"one bad one good", drivesJSON(map[string]map[string]map[string]any{
			"/org/freedesktop/UDisks2/drives/A": obj("Good", map[string]P{ifaceAta: ataOK}),
			"/org/freedesktop/UDisks2/drives/B": obj("Bad", map[string]P{ifaceAta: {"SmartSupported": true, "SmartUpdated": 1, "SmartFailing": true}})}),
			nil, Look, "Bad: SMART says it's failing"},
		{"no drives (a VM)", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/block_devices/vda": {"org.freedesktop.UDisks2.Block": P{"Id": "x"}}}),
			nil, Unknown, "no SMART data yet"},
		{"drive with no model uses its id", drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/QEMU_HDD": {ifaceDrive: P{}, ifaceAta: ataOK}}),
			nil, OK, "SMART is fine: QEMU_HDD"},
		{"udisks not running", "", errors.New("Failed to activate service: not running"), Unknown, "udisks2 isn't running"},
		{"garbage", "not json", nil, Unknown, "can't read drive data"},
		{"wrong shape", `{"data":"x"}`, nil, Unknown, "can't read drive data"},
		{"empty data", `{"type":"a{oa{sa{sv}}}","data":[]}`, nil, Unknown, "can't read drive data"},
		{"null", `null`, nil, Unknown, "can't read drive data"},
		{"properties of the wrong type are ignored", `{"data":[{"/d/A":{"` + ifaceDrive + `":{"Model":{"type":"s","data":42}},"` + ifaceAta +
			`":{"SmartSupported":{"data":"yes"},"SmartUpdated":{"data":"soon"},"SmartFailing":{"data":"true"},"SmartNumBadSectors":{"data":1.5}}}}]}`, nil, Unknown, "no SMART data yet"},
		{"huge values", `{"data":[{"/d/A":{"` + ifaceDrive + `":{"Model":{"data":"` + strings.Repeat("M", 100000) + `"}},"` + ifaceAta +
			`":{"SmartSupported":{"data":true},"SmartUpdated":{"data":99999999999999999999999},"SmartNumBadSectors":{"data":99999999999999999999999}}}}]}`, nil, Unknown, "no SMART data yet"},
		{"huge model name is cut", `{"data":[{"/d/A":{"` + ifaceDrive + `":{"Model":{"data":"` + strings.Repeat("M", 100000) + `"}},"` + ifaceAta +
			`":{"SmartSupported":{"data":true},"SmartUpdated":{"data":5},"SmartFailing":{"data":true}}}}]}`, nil, Look, ""},
		{"control characters in a model", `{"data":[{"/d/A":{"` + ifaceDrive + `":{"Model":{"data":"Evil\u001b[2J\u0007Disk"}},"` + ifaceAta +
			`":{"SmartSupported":{"data":true},"SmartUpdated":{"data":5},"SmartFailing":{"data":true}}}}]}`, nil, Look, ""},
		{"too large", strings.Repeat(" ", UDisksMax+1), nil, Unknown, "drive data too large"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkDiskHealth(context.Background(), Deps{Query: query(c.out, c.err)})
			if got.State != c.state || (c.detail != "" && got.Detail != c.detail) {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
			if strings.ContainsAny(got.Detail, "\x1b\x07") || len([]rune(got.Detail)) > DetailMax+1 {
				t.Errorf("unsafe or long detail %q", got.Detail)
			}
		})
	}
	if got := checkDiskHealth(context.Background(), Deps{}); got.State != Unknown {
		t.Error("no Query")
	}
}

// busctl must never start udisksd, and must stay read-only.
func TestDriveCommandIsReadOnly(t *testing.T) {
	s := strings.Join(driveCmd, " ")
	for _, want := range []string{"--auto-start=no", "call org.freedesktop.UDisks2", "GetManagedObjects", "--system", "--json=short"} {
		if !strings.Contains(s, want) {
			t.Errorf("busctl args lack %q: %s", want, s)
		}
	}
	for _, bad := range []string{"SmartUpdate", "SmartSelftestStart", "Format", "Delete", "Set"} {
		if strings.Contains(s, bad) {
			t.Errorf("busctl args mention %q", bad)
		}
	}
}
