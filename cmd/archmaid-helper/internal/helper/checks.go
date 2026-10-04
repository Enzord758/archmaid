package helper

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// InstallPath is where the helper must live. It is a constant on purpose:
// the polkit policy names this exact path, and the helper refuses to run as
// root from anywhere else.
const InstallPath = "/usr/lib/archmaid/archmaid-helper"

// Default lock paths.
const (
	DefaultLockPath     = "/run/archmaid-helper.lock"
	DefaultPacmanDBLock = "/var/lib/pacman/db.lck"
)

// FileStat is the part of lstat(2) the install check needs.
type FileStat struct {
	Uid  uint32
	Mode fs.FileMode
}

// LstatFile is the real FileStat source.
func LstatFile(path string) (FileStat, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileStat{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return FileStat{}, fmt.Errorf("%s: no owner information", path)
	}
	return FileStat{Uid: st.Uid, Mode: fi.Mode()}, nil
}

var uidPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

// errRefused marks a refusal (exit 4) as opposed to an internal error.
type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return refusal{fmt.Sprintf(format, a...)} }

// checkPrivilege decides whether this process may act as the root helper.
// A dev build (devhelper tag) is the inverse: it must NOT be root, and skips
// the install-path check so it can be run from a checkout.
func checkPrivilege(o Options) error {
	if o.Dev {
		if o.Euid == 0 {
			return refuse("a devhelper build must never run as root")
		}
		return nil
	}
	if o.Euid != 0 {
		return refuse("not running as root (start it through pkexec)")
	}
	if !uidPattern.MatchString(o.CallerUID) {
		return refuse("no PKEXEC_UID or SUDO_UID: not started through pkexec or sudo")
	}
	return CheckInstall(o.Exe, o.Stat)
}

// CheckInstall verifies the running binary is InstallPath, a root-owned
// regular file that is not group/other-writable and not setuid/setgid, in
// root-owned directories none of which is group/other-writable or a symlink.
// Anyone who could write to any of those could swap the code polkit is
// about to run as root.
func CheckInstall(exe func() (string, error), stat func(string) (FileStat, error)) error {
	if exe == nil || stat == nil {
		return refuse("install check is not configured")
	}
	self, err := exe()
	if err != nil {
		return refuse("cannot determine own path: %v", err)
	}
	if self != InstallPath {
		return refuse("running from %q, not %s", self, InstallPath)
	}
	for p := InstallPath; ; p = filepath.Dir(p) {
		st, err := stat(p)
		if err != nil {
			return refuse("stat %s: %v", p, err)
		}
		switch {
		case st.Mode&fs.ModeSymlink != 0:
			return refuse("%s is a symlink", p)
		case st.Uid != 0:
			return refuse("%s is not owned by root", p)
		case st.Mode.Perm()&0o022 != 0:
			return refuse("%s is group- or other-writable", p)
		}
		if p == InstallPath {
			if !st.Mode.IsRegular() {
				return refuse("%s is not a regular file", p)
			}
			if st.Mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
				return refuse("%s must never be setuid/setgid", p)
			}
		} else if !st.Mode.IsDir() {
			return refuse("%s is not a directory", p)
		}
		if p == "/" {
			return nil
		}
	}
}

// errBusy marks exit 5.
var errBusy = errors.New("busy")

// acquireLock takes the helper's own exclusive lock (non-blocking) and, when
// pacmanToo, refuses while pacman's database lock exists. The returned
// release must be called; the lock also dies with the process.
func acquireLock(path, pacmanDBLock string, pacmanToo bool) (release func(), err error) {
	if path == "" {
		return nil, fmt.Errorf("no lock path configured")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: another archmaid-helper is running (%s is locked)", errBusy, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	release = func() { f.Close() }
	if pacmanToo {
		if _, err := os.Lstat(pacmanDBLock); err == nil {
			release()
			return nil, fmt.Errorf("%w: pacman is running (%s exists)", errBusy, pacmanDBLock)
		} else if !errors.Is(err, fs.ErrNotExist) {
			release()
			return nil, fmt.Errorf("%w: cannot check %s: %v", errBusy, pacmanDBLock, err)
		}
	}
	return release, nil
}
