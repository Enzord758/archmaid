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

// pacman's download leftovers (task #116).
//
// pacman 7 downloads as its DownloadUser (alpm) into a temporary folder
// it makes in the first writable cache dir: mkdtemp("download-XXXXXX"),
// chowned to that user, removed when the download ends. An interrupted
// download leaves it behind, and `pacman -Sc` then prints a red
// "error: could not open file <cache>/download-XXXXXX" for each one, on
// an otherwise clean run. So the clean-uninstalled part removes those it
// can prove are abandoned and empty, right before pacman -Sc, and nothing
// else:
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
//   - fstatat with AT_SYMLINK_NOFOLLOW must show a directory, not a
//     symlink or anything else;
//   - it must not have changed for leftoverMinAge: a download in progress
//     changes its folder (every file it starts or finishes), and a folder
//     pacman made after the lock check below is new;
//   - and nothing happens while pacman's db.lck exists.
//
// The removal is unlinkat(AT_REMOVEDIR): rmdir semantics, so a folder
// that isn't empty stays (ENOTEMPTY), and it is never recursive. Each
// removal is one line of the part's output; so is each candidate left
// alone, so the red line pacman then prints for it has its reason above
// it. None of this changes the part's result: it is pacman -Sc's.

// PacmanConf prints pacman's configuration as pacman itself resolves it.
const PacmanConf = "/usr/bin/pacman-conf"

// leftoverMinAge is how long a download folder must have been unchanged
// (removeLeftover's note says "the last hour").
const leftoverMinAge = time.Hour

// maxCacheDirs caps how many cache dirs pacman-conf may name.
const maxCacheDirs = 16

// downloadDirPattern is libalpm's template "download-XXXXXX" after
// mkdtemp, whose six characters are ASCII letters and digits.
var downloadDirPattern = regexp.MustCompile(`^download-[A-Za-z0-9]{6}$`)

func cacheDirArgv() []string { return []string{PacmanConf, "CacheDir"} }

// tidyDownloadLeftovers removes the empty download folders interrupted
// pacman downloads left in the cache, writing one line per folder it
// removes or leaves alone (see above). It never fails the part.
func (s *session) tidyDownloadLeftovers() {
	note := func(format string, a ...any) { s.em.Line("out", fmt.Sprintf(format, a...)) }
	if err := pacmanIdle(s.o.PacmanDBLock); err != nil {
		note("Left pacman's download folders alone: %v", err)
		return
	}
	dirs, err := s.cacheDirs()
	if err != nil {
		note("Left pacman's download folders alone: %v", err)
		return
	}
	now := time.Now()
	for _, dir := range dirs {
		removed, kept, err := removeDownloadLeftovers(dir, now)
		for _, name := range removed {
			note("Removed %s: an empty folder an interrupted pacman download left behind", filepath.Join(dir, name))
		}
		for _, k := range kept {
			note("Left %s alone: %s", filepath.Join(dir, k.name), k.reason)
		}
		if err != nil {
			note("Left %s alone: %v", dir, err)
		}
	}
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

// keptLeftover is a candidate left alone, and why.
type keptLeftover struct{ name, reason string }

// removeDownloadLeftovers removes dir's abandoned, empty download folders
// (see the top of this file) and returns the names it removed and the
// candidates it left alone. err is why dir itself was not looked at, or
// why reading it stopped.
func removeDownloadLeftovers(dir string, now time.Time) (removed []string, kept []keptLeftover, err error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return nil, nil, errors.New("not a directory (or a symlink)")
		}
		return nil, nil, err
	}
	d := os.NewFile(uintptr(fd), dir) // owns fd from here
	defer d.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, nil, err
	}
	switch {
	case st.Uid != uint32(unix.Geteuid()):
		return nil, nil, fmt.Errorf("owned by uid %d, not %d", st.Uid, unix.Geteuid())
	case st.Mode&0o022 != 0:
		return nil, nil, errors.New("group- or other-writable")
	}
	names, err := d.Readdirnames(-1)
	for _, name := range names {
		if !downloadDirPattern.MatchString(name) {
			continue
		}
		if reason := removeLeftover(fd, name, now); reason == "" {
			removed = append(removed, name)
		} else {
			kept = append(kept, keptLeftover{name, reason})
		}
	}
	return removed, kept, err
}

// removeLeftover removes dirfd's child name if it is an empty directory
// unchanged for leftoverMinAge, and returns "" when it did, else why not.
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
	if changed := time.Unix(st.Mtim.Unix()); now.Sub(changed) < leftoverMinAge {
		return "changed within the last hour, so a download may be using it"
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
			return "not empty"
		}
		return err.Error()
	}
	return ""
}
