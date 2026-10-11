// Package sysstat reads the header status line (free disk, available RAM,
// pacman cache size) straight from the kernel, with no child processes and no
// UI dependencies.
package sysstat

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Paths are the roots Read inspects; tests point them at fixtures.
type Paths struct {
	Disk    string // statfs'd for free space
	Meminfo string // /proc/meminfo format
	Cache   string // directory whose disk usage is reported
}

// DefaultPaths are the real system locations.
func DefaultPaths() Paths {
	return Paths{Disk: "/", Meminfo: "/proc/meminfo", Cache: "/var/cache/pacman/pkg"}
}

// Status holds the formatted fields of the status line. Unreadable sources are
// "N/A".
//
// The byte counts behind them are for a front end that draws them as
// meters (the GUI's sidebar): disk free of the disk's size, RAM available
// of the total, the cache's size. A count is 0 when its source can't be
// read; Free/RAM/Cache say "N/A" then.
type Status struct {
	Free, RAM, Cache string

	FreeBytes, DiskBytes    uint64
	RAMAvailBytes, RAMBytes uint64
	CacheBytes              uint64
}

// Read gathers the status. It reads the kernel directly (statfs,
// /proc/meminfo, a directory walk) instead of forking df/free/du: the line
// refreshes every few seconds and the shell pipelines cost nine processes a
// tick. The numbers and format match what those tools printed.
func Read(p Paths) Status {
	var s Status
	s.Free, s.FreeBytes, s.DiskBytes = diskFree(p.Disk)
	s.RAM, s.RAMAvailBytes, s.RAMBytes = memAvail(p.Meminfo)
	s.Cache, s.CacheBytes = cacheSize(p.Cache)
	return s
}

// Line renders the header line.
func (s Status) Line() string {
	return fmt.Sprintf(" Free: %s   RAM avail: %s   Cache: %s", s.Free, s.RAM, s.Cache)
}

// diskFree is `df -h`'s Avail column for path, or "N/A" when unreadable,
// with the bytes available and the filesystem's size (df's Size).
func diskFree(path string) (string, uint64, uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "N/A", 0, 0
	}
	free := st.Bavail * uint64(st.Frsize)
	return humanCeil(free), free, st.Blocks * uint64(st.Frsize)
}

// memAvail is the kernel's MemAvailable (`free -h`'s "available" column, memory
// usable without swapping), or "N/A" when unreadable, with the bytes
// available and MemTotal (0 when that line is missing).
func memAvail(path string) (string, uint64, uint64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "N/A", 0, 0
	}
	var avail, total uint64
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			total = meminfoKB(rest)
			continue
		}
		rest, ok := strings.CutPrefix(line, "MemAvailable:")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
				avail, found = kb*1024, true
				break // MemTotal comes first in /proc/meminfo
			}
		}
	}
	if !found {
		return "N/A", 0, total
	}
	return humanRound(avail), avail, total
}

// meminfoKB is a /proc/meminfo value ("  32557020 kB") in bytes, 0 when it
// doesn't parse. No strings.Fields: the status line's reads keep their
// allocation count (bench-compare fails any rise).
func meminfoKB(rest string) uint64 {
	num, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if kb, err := strconv.ParseUint(num, 10, 64); err == nil {
		return kb * 1024
	}
	return 0
}

// cacheSize is `du -sh` of dir: disk usage in blocks, directories included,
// unreadable subdirectories skipped. "N/A" when dir itself can't be read.
// The bytes are the same usage.
func cacheSize(dir string) (string, uint64) {
	var total uint64
	missing := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				missing = true
			}
			return nil
		}
		total += diskBytes(d)
		return nil
	})
	if missing {
		return "N/A", 0
	}
	return humanCeil(total), total
}

// diskBytes is the disk usage of one walked entry (blocks * 512), 0 when it
// can't be statted.
func diskBytes(d fs.DirEntry) uint64 {
	if info, err := d.Info(); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			return uint64(st.Blocks) * 512
		}
	}
	return 0
}

// EntriesUsage is the disk usage `rm -rf dir/*` would free: everything under
// dir except dir itself and the entries whose names start with a dot (the
// shell glob skips them). It formats like cacheSize (du -h, rounded up) and
// returns the bytes too. Unreadable subdirectories are skipped; an error
// when dir itself can't be read, or when ctx ends first (a walk of a huge
// tree can be abandoned). Read doesn't call it.
func EntriesUsage(ctx context.Context, dir string) (string, uint64, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, err
	}
	var total uint64
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		err := filepath.WalkDir(filepath.Join(dir, e.Name()), func(_ string, d fs.DirEntry, err error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if err == nil {
				total += diskBytes(d)
			}
			return nil
		})
		if err != nil {
			return "", 0, err
		}
	}
	return humanCeil(total), total, ctx.Err()
}

var sizeUnits = []string{"", "K", "M", "G", "T", "P"}

// scale reduces n to a value below 1024 and the index of its unit.
func scale(n uint64) (float64, int) {
	v, i := float64(n), 0
	for v >= 1024 && i < len(sizeUnits)-1 {
		v /= 1024
		i++
	}
	return v, i
}

// humanCeil formats n like GNU `df -h` / `du -h`: powers of 1024, no "i",
// one decimal below 10, always rounded up.
func humanCeil(n uint64) string {
	v, i := scale(n)
	if v < 10 {
		v = math.Ceil(v*10) / 10
	} else {
		v = math.Ceil(v)
	}
	if v >= 1024 && i < len(sizeUnits)-1 {
		v, i = v/1024, i+1
		v = math.Ceil(v*10) / 10
	}
	if v < 10 && i > 0 {
		return fmt.Sprintf("%.1f%s", v, sizeUnits[i])
	}
	return fmt.Sprintf("%.0f%s", v, sizeUnits[i])
}

// humanRound formats n like procps `free -h`: binary units with an "i"
// suffix, one decimal below 10, rounded to nearest.
func humanRound(n uint64) string {
	v, i := scale(n)
	if i == 0 {
		return fmt.Sprintf("%.0fB", v)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f%si", v, sizeUnits[i])
	}
	return fmt.Sprintf("%.0f%si", v, sizeUnits[i])
}
