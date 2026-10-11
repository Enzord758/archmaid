package coverdir

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

type fakeInfo struct {
	mode fs.FileMode
	uid  uint32
	sys  any
}

func (f fakeInfo) Name() string       { return "d" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any {
	if f.sys != nil {
		return f.sys
	}
	return &syscall.Stat_t{Uid: f.uid}
}

func lstatOf(fi fs.FileInfo, err error) func(string) (fs.FileInfo, error) {
	return func(string) (fs.FileInfo, error) { return fi, err }
}

// TestPick: root gets the baked dir only when it is a root-owned real
// directory nobody else can write; GOCOVERDIR from the environment, a user
// run and an unset Dir leave everything alone.
func TestPick(t *testing.T) {
	good := fakeInfo{mode: fs.ModeDir | 0o755}
	for _, c := range []struct {
		name  string
		env   string
		euid  int
		dir   string
		lstat func(string) (fs.FileInfo, error)
		want  string
	}{
		{"root, good dir", "", 0, "/var/lib/c", lstatOf(good, nil), "/var/lib/c"},
		{"env wins", "/tmp/x", 0, "/var/lib/c", lstatOf(good, nil), ""},
		{"not root", "", 1000, "/var/lib/c", lstatOf(good, nil), ""},
		{"no Dir", "", 0, "", lstatOf(good, nil), ""},
		{"missing", "", 0, "/var/lib/c", lstatOf(nil, errors.New("no such file")), ""},
		{"symlink", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: fs.ModeSymlink | 0o777}, nil), ""},
		{"a file", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: 0o644}, nil), ""},
		{"group-writable", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: fs.ModeDir | 0o775}, nil), ""},
		{"world-writable", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: fs.ModeDir | fs.ModeSticky | 0o777}, nil), ""},
		{"user-owned", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: fs.ModeDir | 0o755, uid: 1000}, nil), ""},
		{"no stat", "", 0, "/var/lib/c", lstatOf(fakeInfo{mode: fs.ModeDir | 0o755, sys: struct{}{}}, nil), ""},
	} {
		if got := Pick(c.env, c.euid, c.dir, c.lstat); got != c.want {
			t.Errorf("%s: Pick = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPickRealDir: against the real filesystem, a user's own temp dir is
// never good enough for root, and / (root-owned, 0755) is.
func TestPickRealDir(t *testing.T) {
	if got := Pick("", 0, t.TempDir(), os.Lstat); os.Getuid() != 0 && got != "" {
		t.Errorf("Pick(user temp dir) = %q, want \"\"", got)
	}
	if got := Pick("", 0, "/", os.Lstat); got != "/" {
		t.Errorf("Pick(/) = %q, want /", got)
	}
}
