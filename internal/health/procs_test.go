package health

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeProc builds a /proc at a temp dir: pids maps a pid to its comm ("" =
// no comm file at all). It returns the proc dir and a mounts file with the
// given /proc options.
func fakeProc(t *testing.T, pids map[string]string, procOpts string) (procDir, mounts string) {
	t.Helper()
	root := t.TempDir()
	procDir = filepath.Join(root, "proc")
	for pid, comm := range pids {
		dir := filepath.Join(procDir, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if comm != "" {
			if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o444); err != nil {
				t.Fatal(err)
			}
		}
	}
	os.MkdirAll(filepath.Join(procDir, "self-not-a-pid"), 0o755)
	mounts = filepath.Join(root, "mounts")
	line := "proc /proc proc " + procOpts + " 0 0\n"
	if err := os.WriteFile(mounts, []byte("sysfs /sys sysfs rw 0 0\n"+line), 0o644); err != nil {
		t.Fatal(err)
	}
	return procDir, mounts
}

// TestListProcsIncomplete (#271): the pacman-lock check tells the user to
// delete the lock, so a process list that may be missing a root pacman must be
// an error (Unknown), never a short list.
func TestListProcsIncomplete(t *testing.T) {
	gid := strconv.Itoa(os.Getgid())
	otherGid := strconv.Itoa(os.Getgid() + 4242)
	for _, c := range []struct {
		name string
		opts string
		pids map[string]string
		fail bool
		want int
	}{
		{"plain", "rw,nosuid,relatime", map[string]string{"1": "systemd", "2": "pacman"}, false, 2},
		{"hidepid=2", "rw,relatime,hidepid=2", map[string]string{"1": "systemd"}, true, 0},
		{"hidepid=invisible", "rw,relatime,hidepid=invisible", map[string]string{"1": "systemd"}, true, 0},
		{"hidepid=1", "rw,hidepid=1", map[string]string{"1": "systemd"}, true, 0},
		{"hidepid=noaccess", "rw,hidepid=noaccess", map[string]string{"1": "systemd"}, true, 0},
		{"hidepid=0", "rw,hidepid=off", map[string]string{"1": "systemd"}, false, 1},
		{"hidepid but our group sees all", "rw,hidepid=2,gid=" + gid, map[string]string{"1": "systemd"}, false, 1},
		{"hidepid and another group", "rw,hidepid=2,gid=" + otherGid, map[string]string{"1": "systemd"}, true, 0},
		{"a process that ended", "rw", map[string]string{"1": "systemd", "9": ""}, false, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, mounts := fakeProc(t, c.pids, c.opts)
			got, err := listProcsAt(dir, mounts)
			if (err != nil) != c.fail || len(got) != c.want {
				t.Errorf("= %v, %v; want %d names, fail=%v", got, err, c.want, c.fail)
			}
		})
	}
}

// TestListProcsUnreadableComm: a comm that exists but is not readable (hidepid=1
// on a kernel whose mount table we could not read) is an error too.
func TestListProcsUnreadableComm(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir, mounts := fakeProc(t, map[string]string{"1": "systemd", "2": "pacman"}, "rw")
	if err := os.Chmod(filepath.Join(dir, "2", "comm"), 0); err != nil {
		t.Fatal(err)
	}
	if got, err := listProcsAt(dir, mounts); err == nil {
		t.Errorf("= %v, nil; want an error", got)
	}
}

// TestPackageKitHoldsTheLock: PackageKit's alpm backend takes db.lck too.
func TestPackageKitHoldsTheLock(t *testing.T) {
	if !isPacmanProc("packagekitd") || isPacmanProc("packagekit-glib") {
		t.Error("packagekitd must count as pacman running, look-alikes not")
	}
}
