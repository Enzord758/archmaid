package report

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"
)

// Size is what a walk of some folders found. Unknown (OK false) is a
// folder the user can't read, a walk that ran out of time or context, or a
// budget already spent; its Bytes are 0 and must not be used.
type Size struct {
	Bytes uint64 // disk usage (allocated blocks), as du counts it
	// Pkgs counts package files (".pkg.tar*", signatures not counted).
	Pkgs int
	OK   bool
}

// DirSize walks roots as the user, du-style: allocated blocks, symlinks not
// followed, a hard-linked file once, other filesystems not entered. A root
// that doesn't exist is a known 0 (nothing there to free). A root that
// can't be listed, or a walk that outlives ctx, makes the whole size
// unknown; a subfolder that can't be listed is skipped (the same ones are
// skipped before and after a step, so the difference stands).
func DirSize(ctx context.Context, roots ...string) Size {
	var total Size
	total.OK = true
	seen := map[[2]uint64]bool{}
	for _, root := range roots {
		fi, err := os.Lstat(root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return Size{}
		}
		st, _ := fi.Sys().(*syscall.Stat_t)
		var dev uint64
		if st != nil {
			dev = uint64(st.Dev)
		}
		if !fi.IsDir() {
			addFile(&total, fi, seen)
			continue
		}
		if !walk(ctx, root, dev, true, &total, seen) {
			return Size{}
		}
	}
	return total
}

// walk adds the folder dir to total; false when the walk can't stand (the
// context ended, or the top folder can't be listed).
func walk(ctx context.Context, dir string, dev uint64, top bool, total *Size, seen map[[2]uint64]bool) bool {
	if ctx.Err() != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if top {
			return false // the target itself can't be read: unknown
		}
		if len(entries) == 0 {
			return true // a subfolder the user can't list is skipped
		}
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		p := dir + "/" + e.Name()
		if fi.IsDir() {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) != dev {
				continue // another filesystem: du -x
			}
			addFile(total, fi, seen) // the folder's own blocks
			if !walk(ctx, p, dev, false, total, seen) {
				return false
			}
			continue
		}
		addFile(total, fi, seen)
	}
	return true
}

func addFile(total *Size, fi fs.FileInfo, seen map[[2]uint64]bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		total.Bytes += uint64(max(fi.Size(), 0))
		return
	}
	if st.Nlink > 1 && !fi.IsDir() {
		k := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
		if seen[k] {
			return
		}
		seen[k] = true
	}
	total.Bytes += uint64(max(st.Blocks, 0)) * 512
	if n := fi.Name(); !fi.IsDir() && strings.Contains(n, ".pkg.tar") && !strings.HasSuffix(n, ".sig") {
		total.Pkgs++
	}
}

// Freed is what a step freed: before minus after, never negative (a folder
// that grew while the step ran freed nothing). ok is false when either
// measurement is unknown.
func Freed(before, after Size) (bytes uint64, ok bool) {
	if !before.OK || !after.OK {
		return 0, false
	}
	if after.Bytes >= before.Bytes {
		return 0, true
	}
	return before.Bytes - after.Bytes, true
}

// measurer takes sizes within one budget: every walk gets at most Each, and
// the walks of a run together at most total of walking time, after which
// every walk is unknown at once. Only time spent walking counts: how long
// topgrade ran, or a password or a confirm waited, is not spent.
type measurer struct {
	each  time.Duration
	total time.Duration
	spent time.Duration
	now   func() time.Time
}

func (m *measurer) size(parent context.Context, roots ...string) Size {
	if parent.Err() != nil {
		return Size{}
	}
	left := m.total - m.spent
	if left <= 0 {
		return Size{}
	}
	ctx, cancel := context.WithTimeout(parent, min(m.each, left))
	defer cancel()
	start := m.now()
	defer func() { m.spent += max(m.now().Sub(start), 0) }()
	return DirSize(ctx, roots...)
}
