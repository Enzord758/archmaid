package privilege

import (
	"bufio"
	"encoding/binary"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pam_faillock's state, read from fixture tally files in a temp dir (the
// conf's dir= points there). Nothing here runs faillock: it is trapped.

// record is one fixture tally record.
type record struct {
	source string
	status uint16
	time   int64
}

// tally encodes records as pam_faillock writes them: struct tally
// { char source[52]; uint16_t reserved; uint16_t status; uint64_t time; },
// host byte order, 64 bytes each.
func tally(recs ...record) []byte {
	var b []byte
	for _, r := range recs {
		rec := make([]byte, 64)
		copy(rec[:52], r.source)
		binary.NativeEndian.PutUint16(rec[54:], r.status)
		binary.NativeEndian.PutUint64(rec[56:], uint64(r.time))
		b = append(b, rec...)
	}
	return b
}

// faillockFixture writes conf (with dir= pointing at a fresh tally dir
// appended) and, unless data is nil, the tally file for "alice". It returns
// the conf's path.
func faillockFixture(t *testing.T, conf string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	tallies := filepath.Join(dir, "faillock")
	if err := os.Mkdir(tallies, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "faillock.conf")
	if err := os.WriteFile(path, []byte(conf+"\ndir = "+tallies+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data != nil {
		if err := os.WriteFile(filepath.Join(tallies, "alice"), data, 0o660); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

const valid = 0x1 // TALLY_STATUS_VALID

var faillockNow = time.Unix(1_800_000_000, 0)

func ago(d time.Duration) int64 { return faillockNow.Add(-d).Unix() }

func TestFaillockStatus(t *testing.T) {
	for _, c := range []struct {
		name string
		conf string
		recs []record
		want Status
		last bool
	}{
		{name: "no failures", want: Status{Deny: 3}},
		{name: "one failure", recs: []record{{"/dev/pts/1", valid | 0x4, ago(time.Minute)}},
			want: Status{Failures: 1, Deny: 3}},
		{name: "two: the next one locks", recs: []record{
			{"sudo", valid, ago(2 * time.Minute)}, {"polkit-1", valid, ago(time.Minute)}},
			want: Status{Failures: 2, Deny: 3}, last: true},
		{name: "invalid records don't count", recs: []record{
			{"sudo", 0, ago(time.Minute)}, {"sudo", 0x4, ago(time.Minute)}, {"sudo", valid, ago(time.Minute)}},
			want: Status{Failures: 1, Deny: 3}},
		{name: "older than fail_interval no longer counts", recs: []record{
			{"sudo", valid, ago(16 * time.Minute)}, {"sudo", valid, ago(time.Minute)}},
			want: Status{Failures: 1, Deny: 3}},
		{name: "three: locked for unlock_time after the last", recs: []record{
			{"sudo", valid, ago(5 * time.Minute)}, {"sudo", valid, ago(4 * time.Minute)}, {"10.0.0.1", valid | 0x2, ago(3 * time.Minute)}},
			want: Status{Failures: 3, Locked: true, Unlock: faillockNow.Add(7 * time.Minute), Deny: 3}},
		{name: "still locked at the unlock second", recs: []record{
			{"sudo", valid, ago(10 * time.Minute)}, {"sudo", valid, ago(10 * time.Minute)}, {"sudo", valid, ago(10 * time.Minute)}},
			want: Status{Failures: 3, Locked: true, Unlock: faillockNow, Deny: 3}},
		{name: "a lapsed lock resets the count", recs: []record{
			{"sudo", valid, ago(12 * time.Minute)}, {"sudo", valid, ago(11 * time.Minute)}, {"sudo", valid, ago(11 * time.Minute)}},
			want: Status{Deny: 3}},
		{name: "locked is judged against the newest failure, not now",
			conf: "unlock_time = 1200", recs: []record{
				{"sudo", valid, ago(20 * time.Minute)}, {"sudo", valid, ago(19 * time.Minute)}, {"sudo", valid, ago(6 * time.Minute)}},
			want: Status{Failures: 3, Locked: true, Unlock: faillockNow.Add(14 * time.Minute), Deny: 3}},
		{name: "unlock_time never", conf: "unlock_time = never", recs: []record{
			{"sudo", valid, ago(time.Hour)}, {"sudo", valid, ago(time.Hour)}, {"sudo", valid, ago(time.Hour)}},
			want: Status{Failures: 3, Locked: true, Deny: 3}},
		{name: "deny and fail_interval from the conf",
			conf: "# local policy\ndeny=5   # five\nfail_interval=60\nsilent\n", recs: []record{
				{"sudo", valid, ago(2 * time.Minute)}, {"sudo", valid, ago(30 * time.Second)}, {"sudo", valid, ago(20 * time.Second)}},
			want: Status{Failures: 2, Deny: 5}},
		{name: "deny 0 never locks", conf: "deny = 0", recs: []record{
			{"sudo", valid, ago(time.Minute)}, {"sudo", valid, ago(time.Minute)}, {"sudo", valid, ago(time.Minute)}, {"sudo", valid, ago(time.Minute)}},
			want: Status{Failures: 4}},
		{name: "invalid values keep the defaults",
			conf: "deny = lots\nunlock_time = 9999999\nfail_interval = -1\ndir = relative", recs: []record{
				{"sudo", valid, ago(14 * time.Minute)}, {"sudo", valid, ago(time.Minute)}},
			want: Status{Failures: 2, Deny: 3}, last: true},
		{name: "a record from the future is outside the interval", recs: []record{
			{"sudo", valid, faillockNow.Add(time.Hour).Unix()}, {"sudo", valid, ago(time.Minute)}},
			want: Status{Failures: 1, Deny: 3}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var data []byte
			if c.recs != nil {
				data = tally(c.recs...)
			}
			got, err := faillockStatus(faillockFixture(t, c.conf, data), "alice", faillockNow)
			if err != nil {
				t.Fatal(err)
			}
			if got.Failures != c.want.Failures || got.Locked != c.want.Locked ||
				!got.Unlock.Equal(c.want.Unlock) || got.Deny != c.want.Deny {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			if got.LastAttempt() != c.last {
				t.Fatalf("LastAttempt = %v, want %v", got.LastAttempt(), c.last)
			}
		})
	}
}

// TestFaillockTrailingPartialRecordIgnored: PAM reads whole records only.
func TestFaillockTrailingPartialRecordIgnored(t *testing.T) {
	data := append(tally(record{"sudo", valid, ago(time.Minute)}), make([]byte, 60)...)
	data[64+54] = valid // a VALID flag in the partial tail must not count
	got, err := faillockStatus(faillockFixture(t, "", data), "alice", faillockNow)
	if err != nil || got.Failures != 1 {
		t.Fatalf("got %+v, %v; want 1 failure", got, err)
	}
}

// TestFaillockDefaults: no conf file means faillock's defaults.
func TestFaillockDefaults(t *testing.T) {
	cfg, err := readFaillockConf(filepath.Join(t.TempDir(), "missing.conf"))
	if err != nil {
		t.Fatal(err)
	}
	want := faillockConfig{dir: "/var/run/faillock", deny: 3, unlockTime: 600 * time.Second, failInterval: 900 * time.Second}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestFaillockRefusesPathlikeUsers(t *testing.T) {
	conf := faillockFixture(t, "", nil)
	for _, u := range []string{"", ".", "..", "../alice", "a/b", "a\x00b"} {
		if _, err := faillockStatus(conf, u, faillockNow); err == nil {
			t.Errorf("user %q accepted", u)
		}
	}
}

func TestFaillockUnreadableTally(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	conf := faillockFixture(t, "", tally(record{"sudo", valid, ago(time.Minute)}))
	tallyPath := filepath.Join(filepath.Dir(conf), "faillock", "alice")
	if err := os.Chmod(tallyPath, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := faillockStatus(conf, "alice", faillockNow); err == nil {
		t.Fatal("an unreadable tally must be an error, not zero failures")
	}
}

// TestFaillockStatusLive reads the real state of the user running the tests,
// read-only, as the GUI will. A missing tally (CI) is no failures; the point
// is that it works without root.
func TestFaillockStatusLive(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	st, err := FaillockStatus(u.Username, time.Now())
	if err != nil {
		t.Fatalf("FaillockStatus(%s): %v", u.Username, err)
	}
	if st.Deny < 0 || st.Failures < 0 {
		t.Fatalf("nonsense status %+v", st)
	}
}

// TestFaillockUnreadableConf: a faillock.conf that can't be read (a path
// through a file, a directory, a line too long to scan) is an error, never
// faillock's defaults: those could say "no lockout" or name the wrong
// tally dir, and a frontend would then not warn before the last attempt.
func TestFaillockUnreadableConf(t *testing.T) {
	good := faillockFixture(t, "deny = 1", tally(record{"sudo", valid, ago(time.Minute)}))
	if st, err := faillockStatus(good, "alice", faillockNow); err != nil || !st.Locked || st.Deny != 1 {
		t.Fatalf("the readable fixture: %+v, %v", st, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Readable up to the long line: deny = 1 is read, then the scan fails.
	long := filepath.Join(dir, "long.conf")
	if err := os.WriteFile(long, []byte("deny = 1\n"+strings.Repeat("#", 70<<10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		conf string
		want error
	}{
		"not a directory": {filepath.Join(file, "faillock.conf"), syscall.ENOTDIR},
		"a directory":     {dir, syscall.EISDIR},
		"line too long":   {long, bufio.ErrTooLong},
	} {
		if st, err := faillockStatus(c.conf, "alice", faillockNow); !errors.Is(err, c.want) {
			t.Errorf("%s: read as %+v, %v; want %v", name, st, err, c.want)
		}
	}
}

// TestFaillockTallyIsADirectory: a tally path that opens but can't be read
// is an error, not zero failures (root included, unlike a 0000 file).
func TestFaillockTallyIsADirectory(t *testing.T) {
	conf := faillockFixture(t, "", nil)
	if err := os.Mkdir(filepath.Join(filepath.Dir(conf), "faillock", "alice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if st, err := faillockStatus(conf, "alice", faillockNow); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("read as %+v, %v; want EISDIR", st, err)
	}
}
