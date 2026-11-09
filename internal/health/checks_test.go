package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// fakeQuery answers systemctl by whether --user is among the args.
func fakeQuery(sys, usr string, sysErr, usrErr error) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "systemctl" {
			return nil, errors.New("unexpected program " + name)
		}
		want := []string{"--failed", "--plain", "--no-legend", "--no-pager"}
		if len(args) > 0 && args[0] == "--user" {
			if !reflect.DeepEqual(args[1:], want) {
				return nil, errors.New("bad user args")
			}
			return []byte(usr), usrErr
		}
		if !reflect.DeepEqual(args, want) {
			return nil, errors.New("bad args")
		}
		return []byte(sys), sysErr
	}
}

func TestFailedUnits(t *testing.T) {
	line := func(u string) string { return u + " loaded failed failed Some description\n" }
	many := ""
	for _, u := range []string{"a.service", "b.service", "c.service", "d.service", "e.service", "f.service"} {
		many += line(u)
	}
	for _, c := range []struct {
		name     string
		sys, usr string
		se, ue   error
		state    State
		detail   string
		copy     string
	}{
		{"none", "", "", nil, nil, OK, "No failed services", ""},
		{"system unit", line("cups.service"), "", nil, nil, Look, "1 failed: cups.service", "journalctl -b -u cups.service"},
		{"user unit", "", line("pipewire.service"), nil, nil, Look, "1 failed: pipewire.service (user)", "journalctl --user -b -u pipewire.service"},
		{"both", line("a.service") + line("b.timer"), line("c.service"), nil, nil, Look,
			"3 failed: a.service, b.timer, c.service (user)",
			"journalctl -b -u a.service -u b.timer && journalctl --user -b -u c.service"},
		{"bullet in the output", "● x.service loaded failed failed X\n", "", nil, nil, Look, "1 failed: x.service", "journalctl -b -u x.service"},
		{"more than shown", many, "", nil, nil, Look, "6 failed: a.service, b.service, c.service, d.service +2 more",
			"journalctl -b -u a.service -u b.service -u c.service -u d.service -u e.service -u f.service"},
		{"user bus missing is ignored", line("a.service"), "", nil, errors.New("Failed to connect to bus"), Look, "1 failed: a.service",
			"journalctl -b -u a.service"},
		{"exit error with a listing still counts", line("a.service"), "", errors.New("exit status 1"), nil, Look, "1 failed: a.service",
			"journalctl -b -u a.service"},
		{"both fail", "", "", errors.New("x"), errors.New("y"), Unknown, "can't ask systemd", ""},
		{"escaped name is quoted", `dev-disk-by\x2dlabel.mount loaded failed failed D` + "\n", "", nil, nil, Look,
			`1 failed: dev-disk-by\x2dlabel.mount`, `journalctl -b -u 'dev-disk-by\x2dlabel.mount'`},
		// Hostile output: not unit names, never reaches the copy string.
		{"injection lines ignored", "x.service; rm -rf ~ loaded failed\n$(reboot).service a b\n`id`.service\nfoo bar\n", "", nil, nil, OK, "No failed services", ""},
		{"duplicate", line("a.service") + line("a.service"), "", nil, nil, Look, "1 failed: a.service", "journalctl -b -u a.service"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkFailedUnits(context.Background(), Deps{Query: fakeQuery(c.sys, c.usr, c.se, c.ue)})
			if got.State != c.state || got.Detail != c.detail || got.Copy != c.copy {
				t.Errorf("= %v %q %q\nwant %v %q %q", got.State, got.Detail, got.Copy, c.state, c.detail, c.copy)
			}
		})
	}
	if got := checkFailedUnits(context.Background(), Deps{}); got.State != Unknown {
		t.Errorf("no Query: %v", got.State)
	}
}

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{"a.service": "a.service", "it's.service": `'it'\''s.service'`, "x y": "'x y'"} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func modulesDir(t *testing.T, rels ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(dir, r), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, r, "pkgbase"), []byte("linux-cachyos\n"), 0o644)
	}
	return dir
}

func TestRestart(t *testing.T) {
	for _, c := range []struct {
		name    string
		running string
		rels    []string
		state   State
		detail  string
	}{
		{"replaced", "7.2.10-1-cachyos", []string{"7.2.12-1-cachyos"}, Look, "running 7.2.10-1-cachyos · installed 7.2.12-1-cachyos"},
		{"current", "7.2.12-1-cachyos", []string{"7.2.12-1-cachyos"}, OK, "Running 7.2.12-1-cachyos"},
		{"no module folders at all", "7.2.12-1", nil, OK, "Running 7.2.12-1"},
		{"unknown kernel", "", []string{"7.2.12-1"}, Unknown, "kernel release unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := Deps{RunningKernel: func() string { return c.running }, ModulesDir: modulesDir(t, c.rels...)}
			got := checkRestart(context.Background(), d)
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
		})
	}
	if got := checkRestart(context.Background(), Deps{}); got.State != Unknown {
		t.Errorf("no seam: %v", got.State)
	}
}

func TestDisk(t *testing.T) {
	const G, M = 1 << 30, 1 << 20
	fs := func(m map[string]FSInfo) func(string) (FSInfo, error) {
		return func(p string) (FSInfo, error) {
			if i, ok := m[p]; ok {
				return i, nil
			}
			return FSInfo{}, os.ErrNotExist
		}
	}
	for _, c := range []struct {
		name   string
		m      map[string]FSInfo
		state  State
		detail string
	}{
		{"plenty, separate boot", map[string]FSInfo{"/": {118 * G, 500 * G, 1}, "/boot": {412 * M, 1024 * M, 2}}, OK, "/ 118 G free · /boot 412 M free"},
		{"root under 10 GiB", map[string]FSInfo{"/": {9*G + G/2, 500 * G, 1}}, Look, "/ 9.5 G free"},
		{"root exactly 10 GiB is fine", map[string]FSInfo{"/": {10 * G, 100 * G, 1}}, OK, "/ 10 G free"},
		{"a low percentage alone is fine", map[string]FSInfo{"/": {40 * G, 1000 * G, 1}}, OK, "/ 40 G free"},
		{"boot under 100 MiB", map[string]FSInfo{"/": {118 * G, 500 * G, 1}, "/boot": {99 * M, 1024 * M, 2}}, Look, "/ 118 G free · /boot 99 M free"},
		{"boot exactly 100 MiB", map[string]FSInfo{"/": {118 * G, 500 * G, 1}, "/boot": {100 * M, 1024 * M, 2}}, OK, "/ 118 G free · /boot 100 M free"},
		{"boot on the root device is not separate", map[string]FSInfo{"/": {118 * G, 500 * G, 1}, "/boot": {1 * M, 500 * G, 1}}, OK, "/ 118 G free"},
		{"no /boot", map[string]FSInfo{"/": {118 * G, 500 * G, 1}}, OK, "/ 118 G free"},
		{"root unreadable", map[string]FSInfo{}, Unknown, "can't read disk space"},
		{"empty total", map[string]FSInfo{"/": {}}, Unknown, "can't read disk space"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkDisk(context.Background(), Deps{FS: fs(c.m)})
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
		})
	}
	if got := checkDisk(context.Background(), Deps{}); got.State != Unknown {
		t.Errorf("no FS: %v", got.State)
	}
}

func TestShortBytes(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 512: "512 B", 1024: "1.0 K", 412 << 20: "412 M", 118 << 30: "118 G", 4509715661: "4.2 G", 3 << 40: "3.0 T"} {
		if got := ShortBytes(n); got != want {
			t.Errorf("ShortBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPacnew(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, nil, 0o644)
		return p
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	pacman := mk("pacman.conf.pacnew")
	sshd := mk("sshd_config.pacsave")
	gone := filepath.Join(dir, "merged.conf.pacnew") // warned about, merged and deleted since
	logOf := func(files ...string) string {
		var b strings.Builder
		b.WriteString("[2026-11-08T10:00:00+0100] [PACMAN] Running 'pacman -Syu'\n")
		for _, f := range files {
			kind := "installed"
			if strings.HasSuffix(f, ".pacsave") {
				kind = "saved"
			}
			b.WriteString("[2026-11-08T10:01:00+0100] [ALPM] warning: " + strings.TrimSuffix(strings.TrimSuffix(f, ".pacnew"), ".pacsave") + " " + kind + " as " + f + "\n")
		}
		return b.String()
	}
	write := func(s string) string {
		p := filepath.Join(t.TempDir(), "pacman.log")
		os.WriteFile(p, []byte(s), 0o644)
		return p
	}
	for _, c := range []struct {
		name   string
		log    string
		state  State
		detail string
		copy   string
	}{
		{"none", logOf(), OK, "No .pacnew or .pacsave files", ""},
		{"one", logOf(pacman), Look, "1 file: " + pacman, "sudo pacdiff"},
		{"two kinds", logOf(pacman, sshd), Look, "2 files: " + pacman + ", " + sshd, "sudo pacdiff"},
		{"only existing ones count", logOf(gone), OK, "No .pacnew or .pacsave files", ""},
		{"deleted one dropped, other kept", logOf(gone, pacman), Look, "1 file: " + pacman, "sudo pacdiff"},
		{"relative path in a forged line", "[2026-11-08T10:01:00+0100] [ALPM] warning: x installed as rel/x.pacnew\n", OK, "No .pacnew or .pacsave files", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkPacnew(context.Background(), Deps{PacmanLog: write(c.log), Exists: exists})
			if got.State != c.state || got.Detail != c.detail || got.Copy != c.copy {
				t.Errorf("= %v %q %q; want %v %q %q", got.State, got.Detail, got.Copy, c.state, c.detail, c.copy)
			}
		})
	}
	// More than shown.
	var fs []string
	for _, n := range []string{"a.pacnew", "b.pacnew", "c.pacnew", "d.pacnew", "e.pacnew"} {
		fs = append(fs, mk(n))
	}
	got := checkPacnew(context.Background(), Deps{PacmanLog: write(logOf(fs...)), Exists: exists})
	if !strings.HasPrefix(got.Detail, "5 files: ") || !strings.HasSuffix(got.Detail, " +2 more") {
		t.Errorf("detail %q", got.Detail)
	}
	// No log, unreadable log, no seam.
	if got := checkPacnew(context.Background(), Deps{PacmanLog: filepath.Join(dir, "nope"), Exists: exists}); got.State != Unknown || got.Detail != "no pacman log" {
		t.Errorf("missing log: %v %q", got.State, got.Detail)
	}
	if got := checkPacnew(context.Background(), Deps{PacmanLog: dir, Exists: exists}); got.State != Unknown {
		t.Errorf("log is a directory: %v", got.State)
	}
	if got := checkPacnew(context.Background(), Deps{PacmanLog: write(logOf(pacman))}); got.State != Unknown {
		t.Errorf("no Exists: %v", got.State)
	}
}

// Real-looking pacman.log, see LastFullUpgrade's doc.
const logHead = `[2026-10-20T08:00:01+0200] [PACMAN] Running 'pacman -Syu'
[2026-10-20T08:00:01+0200] [PACMAN] synchronizing package lists
[2026-10-20T08:00:05+0200] [PACMAN] starting full system upgrade
[2026-10-20T08:00:30+0200] [ALPM] transaction started
[2026-10-20T08:00:31+0200] [ALPM] upgraded zlib (1.3.1-1 -> 1.3.2-1)
[2026-10-20T08:00:40+0200] [ALPM] transaction completed
[2026-10-20T08:00:40+0200] [ALPM] running '30-systemd-update.hook'...
`

func TestLastFullUpgrade(t *testing.T) {
	ts := func(s string) time.Time {
		v, err := time.Parse(logTimeLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		name string
		log  string
		want string // "" = none
	}{
		{"one full upgrade", logHead, "2026-10-20T08:00:40+0200"},
		{"a later one wins", logHead + `[2026-11-02T09:00:00+0100] [PACMAN] Running 'pacman -Syu'
[2026-11-02T09:00:04+0100] [PACMAN] starting full system upgrade
[2026-11-02T09:00:20+0100] [ALPM] transaction started
[2026-11-02T09:00:50+0100] [ALPM] transaction completed
`, "2026-11-02T09:00:50+0100"},
		{"a later plain install is not a full upgrade", logHead + `[2026-11-05T09:00:00+0100] [PACMAN] Running 'pacman -S ripgrep'
[2026-11-05T09:00:10+0100] [ALPM] transaction started
[2026-11-05T09:00:11+0100] [ALPM] installed ripgrep (14.1.1-1)
[2026-11-05T09:00:11+0100] [ALPM] transaction completed
`, "2026-10-20T08:00:40+0200"},
		{"a failed later upgrade does not move it", logHead + `[2026-11-05T09:00:00+0100] [PACMAN] Running 'pacman -Syu'
[2026-11-05T09:00:04+0100] [PACMAN] starting full system upgrade
[2026-11-05T09:00:20+0100] [ALPM] transaction started
[2026-11-05T09:00:30+0100] [ALPM] transaction failed
`, "2026-10-20T08:00:40+0200"},
		{"a declined one does not move it", logHead + `[2026-11-05T09:00:00+0100] [PACMAN] Running 'pacman -Syu'
[2026-11-05T09:00:04+0100] [PACMAN] starting full system upgrade
[2026-11-05T09:01:00+0100] [PACMAN] Running 'pacman -S ripgrep'
[2026-11-05T09:01:10+0100] [ALPM] transaction started
[2026-11-05T09:01:11+0100] [ALPM] transaction completed
`, "2026-10-20T08:00:40+0200"},
		{"nothing to do does not move it", logHead + `[2026-11-05T09:00:00+0100] [PACMAN] Running 'pacman -Syu'
[2026-11-05T09:00:04+0100] [PACMAN] starting full system upgrade
`, "2026-10-20T08:00:40+0200"},
		{"only a plain install", `[2026-11-05T09:00:10+0100] [ALPM] transaction started
[2026-11-05T09:00:11+0100] [ALPM] transaction completed
`, ""},
		{"empty", "", ""},
		{"garbage and a bad timestamp", "x\n[\n[bad] [PACMAN] starting full system upgrade\n[bad] [ALPM] transaction completed\n[]\n", ""},
		{"scriptlet lines in between", `[2026-11-05T09:00:04+0100] [PACMAN] starting full system upgrade
[2026-11-05T09:00:20+0100] [ALPM] transaction started
[2026-11-05T09:00:21+0100] [ALPM-SCRIPTLET] ==> something
[2026-11-05T09:00:30+0100] [ALPM] transaction completed
`, "2026-11-05T09:00:30+0100"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LastFullUpgrade(strings.NewReader(c.log))
			if c.want == "" {
				if ok {
					t.Errorf("found %v, want none", got)
				}
				return
			}
			if !ok || !got.Equal(ts(c.want)) {
				t.Errorf("= %v %v, want %s", got, ok, c.want)
			}
		})
	}
}

func TestRunOrderAndAttention(t *testing.T) {
	d := Deps{Now: func() time.Time { return t0 }}
	r := Run(context.Background(), d)
	var ids []string
	for _, c := range r.Checks {
		ids = append(ids, c.ID)
		if c.State != Unknown || c.Title == "" {
			t.Errorf("%s: bare Deps should leave every check Unknown with a title: %v %q", c.ID, c.State, c.Title)
		}
	}
	if !reflect.DeepEqual(ids, []string{"failed-units", "restart", "time-sync", "crashes", "pacman-lock", "pacnew", "aur", "news", "mirror", "disk", "disk-health"}) {
		t.Errorf("order %v", ids)
	}
	if !r.At.Equal(t0) || r.Attention() != 0 {
		t.Errorf("At %v, attention %d", r.At, r.Attention())
	}
	if n := (Report{Checks: []Check{{State: Look}, {State: OK}, {State: Unknown}, {State: Look}}}).Attention(); n != 2 {
		t.Errorf("Attention = %d", n)
	}
	for s, want := range map[State]string{OK: "ok", Look: "look", Unknown: "unknown"} {
		if s.String() != want {
			t.Errorf("%d = %q", s, s.String())
		}
	}
}

// One check that blocks past the deadline, and one that panics, leave the
// others alone.
func TestRunIsolation(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	d := Deps{
		Now: func() time.Time { return t0 },
		Query: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			<-ctx.Done() // honours the deadline, like proc.Query
			return nil, ctx.Err()
		},
		RunningKernel: func() string { panic("boom") },
		FS: func(string) (FSInfo, error) {
			return FSInfo{Avail: 100 << 30, Total: 200 << 30, Dev: 1}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := Run(ctx, d)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("Run took %v", time.Since(start))
	}
	by := map[string]Check{}
	for _, c := range r.Checks {
		by[c.ID] = c
	}
	if by["restart"].State != Unknown || by["restart"].Detail != "check failed" {
		t.Errorf("panicking check: %+v", by["restart"])
	}
	if by["failed-units"].State != Unknown {
		t.Errorf("blocked check: %+v", by["failed-units"])
	}
	if by["disk"].State != OK {
		t.Errorf("an independent check was disturbed: %+v", by["disk"])
	}
}

// Run returns the checks grouped for the sheet: system, packages, hardware.
func TestRunGroups(t *testing.T) {
	r := Run(context.Background(), Deps{Now: func() time.Time { return t0 }})
	want := map[string]string{"failed-units": "system", "restart": "system", "time-sync": "system", "crashes": "system", "pacman-lock": "system",
		"pacnew": "packages", "aur": "packages", "news": "packages", "mirror": "packages", "disk": "hardware", "disk-health": "hardware"}
	if len(r.Checks) != len(want) {
		t.Fatalf("%d checks", len(r.Checks))
	}
	rank := map[string]int{"system": 0, "packages": 1, "hardware": 2}
	last := ""
	for _, c := range r.Checks {
		if c.Group != want[c.ID] {
			t.Errorf("%s: group %q", c.ID, c.Group)
		}
		if last != "" && rank[c.Group] < rank[last] {
			t.Errorf("%s: groups out of order", c.ID)
		}
		last = c.Group
	}
}

// A check that ignores its context is cut off at the deadline.
func TestRunDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	d := Deps{FS: func(string) (FSInfo, error) { <-release; return FSInfo{}, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := Run(ctx, d)
	for _, c := range r.Checks {
		if c.ID == "disk" && (c.State != Unknown || c.Detail != "timed out") {
			t.Errorf("disk: %+v", c)
		}
	}
}
