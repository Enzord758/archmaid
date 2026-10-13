package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// pacman's download leftovers (tasks #116, #122).
//
// pacman 7 downloads as its DownloadUser (alpm) into a temporary folder
// it makes in the first writable cache dir: mkdtemp("download-XXXXXX"),
// chowned to that user (libalpm's _alpm_download_dir_setup). It removes
// the folder only at the end of an actual download (_alpm_download), so
// an interrupted download leaves it behind, and so does every transaction
// that had nothing to download: download_files (lib/libalpm/sync.c,
// pacman 7.1) makes the folder before it looks for files to fetch, and on
// "nothing to fetch" only frees its name. yay's pacman -U of a built AUR
// package, or a -Syu whose packages were all cached, leaves one each, so
// Full Cleanup's own topgrade leaves fresh ones on most runs (VM round
// 8b). `pacman -Sc` then prints a red "error: could not open file
// <cache>/download-XXXXXX" for each one. So the clean-uninstalled part
// removes those it can prove are abandoned and empty, right before
// pacman -Sc, and nothing else:
//
//   - the cache dirs are the ones pacman itself uses, as `pacman-conf
//     CacheDir` reads them from pacman.conf; never a path from the caller;
//   - each is opened without following a symlink (O_NOFOLLOW) and must be
//     a directory owned by this process's uid (root) that neither group
//     nor others can write to: then only root, that is pacman, can have
//     made an entry in it;
//   - only its direct children are looked at, through that open fd
//     (fstatat, unlinkat), never by a path walk;
//   - the name is "download-" plus mkdtemp's six letters and digits,
//     exactly;
//   - the removal runs while the helper holds pacman's own lock, db.lck,
//     taken exactly as libalpm takes it (holdPacmanLock), so no pacman
//     that locks can be running, and an existing lock means nothing is
//     touched;
//   - under that lock, fstatat with AT_SYMLINK_NOFOLLOW must show a
//     directory, not a symlink or anything else, unchanged (by the later
//     of its mtime and ctime) for leftoverMinAge.
//
// Why the age floor still exists although the lock is held: libalpm
// makes these folders under the lock in a transaction (download_files
// runs inside alpm_trans_commit, which asserts the lock), but not always.
// `pacman -U <url>` fetches through alpm_fetch_pkgurl before its
// trans_init (src/pacman/upgrade.c), so it makes its folder in the cache
// with no db.lck; so does a libalpm user with another DBPath and the same
// cache (checkupdates -d run as root), and so does pacman itself if
// run with another --dbpath than pacman.conf's (the lock is pacman-conf's
// DBPath, dblock.go). Such a folder
// is empty only from its mkdtemp to the first .part file, which
// curl_add_payload opens before the transfer starts: milliseconds, or
// seconds if the download user's lookup (getpwnam, NSS) is slow. That holds
// for libalpm's built-in curl only. With an XferCommand (`curl ... -o %o`)
// the file appears only when data arrives, so a lockless `pacman -U <url>`
// can keep an empty folder for over 2 minutes. The floor covers the built-in
// case with a wide margin; and if it is ever beaten, all that happens is
// that one download fails (its .part can't be created) and
// pacman stops before it changes anything.
//
// The hold is kept as short as possible: pacman-conf runs and every cache
// dir is opened, checked and read before the lock; under it there are
// only fstatat and unlinkat on those open dirs (no exec, no output: a
// write to the client's pipe can block); the lines are written after the
// lock is gone.
//
// The removal is unlinkat(AT_REMOVEDIR): rmdir semantics, so a folder
// that isn't empty stays (ENOTEMPTY), and it is never recursive. Each
// removal is one line of the part's output; so is each candidate left
// alone, so the red line pacman then prints for it has its reason above
// it. None of this changes the part's result: it is pacman -Sc's.
//
// Not done, a note for later: the cache dirs are opened by path, and only
// the last component refuses a symlink (O_NOFOLLOW). openat2 with
// RESOLVE_NO_SYMLINKS would refuse one anywhere in the path; every parent
// of a pacman-conf CacheDir is root's to change anyway, so it is not
// needed for this threat model.

// PacmanConf prints pacman's configuration as pacman itself resolves it.
const PacmanConf = "/usr/bin/pacman-conf"

// leftoverMinAge is how long a download folder must have been unchanged
// (removeLeftover's note says "the last 2 minutes"). See the top of this
// file for why there is a floor at all with the lock held.
const leftoverMinAge = 2 * time.Minute

// maxCacheDirs caps how many cache dirs pacman-conf may name.
const maxCacheDirs = 16

// downloadDirPattern is libalpm's template "download-XXXXXX" after
// mkdtemp, whose six characters are ASCII letters and digits.
var downloadDirPattern = regexp.MustCompile(`^download-[A-Za-z0-9]{6}$`)

func cacheDirArgv() []string { return []string{PacmanConf, "CacheDir"} }

// keptLeftover is a candidate left alone, and why.
type keptLeftover struct{ name, reason string }

// cacheScan is one cache dir: opened, checked and read before pacman's
// lock is taken (fd, candidates), then the outcome of the removals under
// it (removed, kept). err is why the dir was not looked at (no fd) or
// why reading it stopped.
type cacheScan struct {
	dir        string
	d          *os.File // the open dir, nil when err says why not
	fd         int      // d's fd
	candidates []string
	removed    []string
	kept       []keptLeftover
	err        error
}

// tidyDownloadLeftovers removes the empty download folders pacman left in
// the cache, writing one line per folder it removes or leaves alone (see
// above). It never fails the part.
func (s *session) tidyDownloadLeftovers() {
	note := func(format string, a ...any) { s.em.Line("out", fmt.Sprintf(format, a...)) }
	dirs, err := s.cacheDirs()
	if err != nil {
		note("Left pacman's download folders alone: %v", err)
		return
	}
	var scans []*cacheScan
	defer func() {
		for _, sc := range scans {
			if sc.d != nil {
				sc.d.Close()
			}
		}
	}()
	n := 0
	for _, dir := range dirs {
		sc := scanCacheDir(dir)
		scans = append(scans, sc)
		n += len(sc.candidates)
	}
	if n > 0 {
		now := s.now()
		lock := s.lockPath()
		if err := s.lockDirSafe(lock); err != nil {
			// Not root's alone: don't take the lock, but a lock that is
			// there still means pacman is running.
			if idle := pacmanIdle(lock); idle != nil {
				note("Left pacman's download folders alone: %v", idle)
			} else {
				note("Left pacman's download folders alone: not taking pacman's lock %s: %v", lock, err)
			}
			return
		}
		err := holdPacmanLock(lock, func() {
			for _, sc := range scans {
				for _, name := range sc.candidates {
					if s.o.underPacmanLock != nil {
						s.o.underPacmanLock(name)
					}
					if reason := removeLeftover(sc.fd, name, now); reason == "" {
						sc.removed = append(sc.removed, name)
					} else {
						sc.kept = append(sc.kept, keptLeftover{name, reason})
					}
				}
			}
		})
		if errors.Is(err, errBusy) {
			note("Left pacman's download folders alone: %v", err)
			return
		}
		if err != nil {
			// The removals ran, but the release went wrong (the lock
			// gone, replaced or still there): that line comes first.
			note("%v", err)
		}
	}
	for _, sc := range scans {
		for _, name := range sc.removed {
			note("Removed %s: an empty folder a pacman download left behind", filepath.Join(sc.dir, name))
		}
		for _, k := range sc.kept {
			p := filepath.Join(sc.dir, k.name)
			if k.reason == reasonNotEmpty {
				note("Left %s alone: %s%s", p, k.reason, notEmptyHint(p, s.lockPath()))
				continue
			}
			note("Left %s alone: %s", p, k.reason)
		}
		if sc.err != nil {
			note("Left %s alone: %v", sc.dir, sc.err)
		}
	}
}

// now is the session's clock (Options.now, else the real one).
func (s *session) now() time.Time {
	if s.o.now != nil {
		return s.o.now()
	}
	return time.Now()
}

// cacheDirs asks pacman-conf for pacman's cache dirs: each must be an
// absolute path of printable characters; duplicates are dropped.
func (s *session) cacheDirs() ([]string, error) {
	out, errOut, exit, err := s.o.Runner.Query(cacheDirArgv())
	if err != nil {
		return nil, fmt.Errorf("pacman-conf CacheDir: %v", err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("pacman-conf CacheDir exited %d: %s", exit, firstLine(errOut))
	}
	var dirs []string
	for _, l := range nonEmptyLines(out) {
		if !filepath.IsAbs(l) || strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, fmt.Errorf("pacman-conf CacheDir printed %q, not an absolute path", l)
		}
		d := filepath.Clean(l)
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	switch {
	case len(dirs) == 0:
		return nil, errors.New("pacman-conf CacheDir named no cache folder")
	case len(dirs) > maxCacheDirs:
		return nil, fmt.Errorf("pacman-conf CacheDir named %d folders, more than %d", len(dirs), maxCacheDirs)
	}
	return dirs, nil
}

// scanCacheDir opens dir (see the top of this file), checks it and lists
// its candidates: the children named like a download folder. On success
// the scan owns the open dir (d), which the caller closes.
func scanCacheDir(dir string) *cacheScan {
	sc := &cacheScan{dir: dir}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			sc.err = errors.New("not a directory (or a symlink)")
		} else {
			sc.err = err
		}
		return sc
	}
	d := os.NewFile(uintptr(fd), dir) // owns fd from here
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		d.Close()
		sc.err = err
		return sc
	}
	switch {
	case st.Uid != uint32(unix.Geteuid()):
		d.Close()
		sc.err = fmt.Errorf("owned by uid %d, not %d", st.Uid, unix.Geteuid())
		return sc
	case st.Mode&0o022 != 0:
		d.Close()
		sc.err = errors.New("group- or other-writable")
		return sc
	}
	sc.d, sc.fd = d, fd
	names, err := d.Readdirnames(-1)
	for _, name := range names {
		if downloadDirPattern.MatchString(name) {
			sc.candidates = append(sc.candidates, name)
		}
	}
	sc.err = err
	return sc
}

// removeLeftover removes dirfd's child name if it is an empty directory
// unchanged for leftoverMinAge, and returns "" when it did, else why not.
// It runs under pacman's lock: fstatat and unlinkat only.
func removeLeftover(dirfd int, name string, now time.Time) string {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err.Error() // it vanished since the directory was read
	}
	switch {
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return "a symlink, not a folder"
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return "not a folder"
	}
	// ctime too: mkdtemp's chown, and every entry made or removed in it,
	// set it, and unlike mtime nobody can set it back (utimensat).
	changed := time.Unix(st.Mtim.Unix())
	if c := time.Unix(st.Ctim.Unix()); c.After(changed) {
		changed = c
	}
	if now.Sub(changed) < leftoverMinAge {
		return "changed within the last 2 minutes, so a download may be starting in it"
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
			return reasonNotEmpty
		}
		return err.Error()
	}
	return ""
}

// reasonNotEmpty is removeLeftover's reason for a folder with something in
// it; its line then says how to delete it by hand (notEmptyHint).
const reasonNotEmpty = "not empty"

// notEmptyHint is the rest of a not-empty folder's line: what is in it,
// and how to delete it by hand, safely. The command is meant to be pasted,
// but every output line goes through SanitizeLine, which drops C1 controls,
// bidi and format characters and replaces invalid UTF-8: a path with any of
// those would come out naming another folder. So the command is printed
// only for a plain path (plainPath); otherwise the line says to delete the
// folder by hand and gives no command.
func notEmptyHint(path, pacmanDBLock string) string {
	head := " (an interrupted download's partial files); to delete it by hand, first make sure no pacman is running (" +
		pacmanDBLock + " doesn't exist)"
	if !plainPath(path) {
		return head + ", then delete that folder with its contents (its name has characters that can't be shown safely, so no command is given)"
	}
	return head + ", then: sudo rm -r -- " + shellQuote(path)
}

// plainPath reports whether every byte of p is in [A-Za-z0-9/._+-], the
// only paths a hint's command is printed for: they survive SanitizeLine
// and a paste unchanged.
func plainPath(p string) bool {
	if p == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '/', c == '.', c == '_', c == '+', c == '-':
		default:
			return false
		}
	}
	return true
}

// shellQuote quotes s for a POSIX shell, so a hint's command can be pasted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// holdPacmanLock runs fn while holding pacman's database lock, taken the
// way libalpm takes it (_alpm_handle_lock, lib/libalpm/handle.c, pacman
// 7.1): open(path, O_WRONLY|O_CREAT|O_EXCL|O_CLOEXEC, 0000), with nothing
// written in it (libalpm writes nothing; pacman only checks that the file
// exists). If the lock exists, fn doesn't run and the error is errBusy.
//
// The lock is removed when fn returns, whichever way: a return or a panic
// (the deferred release runs before the panic goes on). A stale db.lck
// would block every later pacman ("unable to lock database"), so:
//
//   - nothing in fn may block for long: only fstatat/unlinkat on open dirs
//     (on an NFS or FUSE cache dir even those can block under the lock);
//   - the helper catches and drops SIGINT, SIGTERM, SIGHUP, SIGQUIT,
//     SIGTSTP and the rest for its whole run (main.go, catchSignals), so no
//     signal a user (or the GUI going away) can send ends it inside fn;
//   - left are SIGKILL and SIGSTOP, which can't be caught: root's, but also
//     the user's, since pkexec leaves the helper in archmaid-gui's user
//     cgroup (cgroup.kill, cgroup.freeze, `systemctl --user` stopping the
//     scope, a logout, systemd-oomd); the OOM killer, the shutdown's final
//     kill, a Go runtime fatal error and a power loss. All of that inside a
//     hold of microseconds per candidate. pacman itself, holding the same
//     lock for minutes, has the same exposure, and so does `pacman -Sc`
//     right after the tidy, for seconds.
//
// The release removes the path only while it still names the file this
// hold made (same device and inode): if root removed the lock and a new
// pacman made its own meanwhile, that one is not ours to remove. libalpm
// itself unlinks by path.
func holdPacmanLock(path string, fn func()) (err error) {
	var fd int
	for {
		fd, err = unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	switch {
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%w: pacman is running (%s exists)", errBusy, path)
	case err != nil:
		return fmt.Errorf("%w: cannot take pacman's lock %s: %v", errBusy, path, err)
	}
	defer func() {
		if rerr := releasePacmanLock(path, fd); rerr != nil {
			err = rerr
		}
	}()
	fn()
	return nil
}

// releasePacmanLock removes the lock holdPacmanLock made and closes fd.
func releasePacmanLock(path string, fd int) error {
	defer unix.Close(fd)
	var mine, now unix.Stat_t
	if err := unix.Fstat(fd, &mine); err == nil {
		if err := unix.Lstat(path, &now); err != nil {
			return fmt.Errorf("pacman's lock %s, which the tidy took, is gone: %v", path, err)
		}
		if mine.Dev != now.Dev || mine.Ino != now.Ino {
			return fmt.Errorf("pacman's lock %s was replaced while the tidy held it; left the new one alone", path)
		}
	}
	if err := unix.Unlink(path); err != nil {
		return fmt.Errorf("could not remove pacman's lock %s, which the tidy took: %v; "+
			"pacman refuses to run while it exists: once no pacman is running, remove it with sudo rm -- %s",
			path, err, shellQuote(path))
	}
	return nil
}
