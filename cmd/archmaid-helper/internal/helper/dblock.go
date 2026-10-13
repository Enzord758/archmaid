package helper

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// pacmanDBPathArgv is how the helper asks pacman where its database is.
// Like PacmanConf for CacheDir, an absolute path, never one the caller gave.
func pacmanDBPathArgv() []string { return []string{PacmanConf, "DBPath"} }

// lockPath is pacman's database lock for this run: Options.PacmanDBLock
// when set (tests), else `pacman-conf DBPath` + db.lck exactly as libalpm
// builds it (handle->lockfile = dbpath + "db.lck"), asked once, the first
// time a check needs it, and the answer kept for the whole run. Every
// db.lck check and the tidy's hold use this one path.
//
// If pacman-conf fails or prints anything that isn't one absolute path of
// printable characters, DefaultPacmanDBLock is used and one line says so.
// Falling back beats refusing: the lock is the helper's guard against a
// pacman it can't see, not a precondition of the work (pacman -Rns, -Sc and
// paccache take their own lock), and a refusal would stop every cleanup on
// a machine where pacman-conf is broken, for no gain in safety. The
// fallback guards the path nearly every system uses, so it protects no less
// than the helper did before it read the path at all.
func (s *session) lockPath() string {
	if s.lockResolved {
		return s.dbLock
	}
	s.lockResolved = true
	if s.o.PacmanDBLock != "" {
		s.dbLock = s.o.PacmanDBLock
		return s.dbLock
	}
	p, err := s.dbLockFromConf()
	if err != nil {
		s.dbLock = DefaultPacmanDBLock
		s.em.Line("out", fmt.Sprintf("Could not read pacman's database path (%v); guarding %s instead", err, DefaultPacmanDBLock))
		return s.dbLock
	}
	s.dbLock = p
	return s.dbLock
}

func (s *session) dbLockFromConf() (string, error) {
	out, errOut, exit, err := s.o.Runner.Query(pacmanDBPathArgv())
	if err != nil {
		return "", fmt.Errorf("pacman-conf DBPath: %v", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("pacman-conf DBPath exited %d: %s", exit, firstLine(errOut))
	}
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		return "", fmt.Errorf("pacman-conf DBPath printed %d lines, want 1", len(lines))
	}
	l := lines[0]
	if !filepath.IsAbs(l) || strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("pacman-conf DBPath printed %q, not an absolute path", l)
	}
	return filepath.Join(filepath.Clean(l), "db.lck"), nil
}

// lockDirSafe is nil when the lock's parent folder is root's alone to
// change: a directory owned by root, neither group- nor other-writable.
// Anyone else able to write there could pre-plant, swap or remove the lock
// under the helper. An unsafe folder means the helper doesn't take the lock;
// it still honours one that exists (pacmanIdle only looks), because a lock
// that is there means a pacman may be running, whoever put it there.
func (s *session) lockDirSafe(lock string) error {
	if s.o.Dev {
		return nil // the devhelper build never runs as root; its folders are the user's
	}
	if s.o.lockDirCheck != nil {
		return s.o.lockDirCheck(filepath.Dir(lock))
	}
	return checkLockDir(filepath.Dir(lock))
}

func checkLockDir(dir string) error {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return fmt.Errorf("cannot check %s: %v", dir, err)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return fmt.Errorf("%s is not a directory", dir)
	case st.Uid != 0:
		return fmt.Errorf("%s is not owned by root", dir)
	case st.Mode&0o022 != 0:
		return fmt.Errorf("%s is writable by group or others", dir)
	}
	return nil
}
