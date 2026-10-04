// Package sysstat reads the header status line (free disk, available RAM,
// pacman cache size) straight from the kernel, with no child processes and no
// UI dependencies.
package sysstat

import (
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
type Status struct {
	Free, RAM, Cache string
}

// Read gathers the status. It reads the kernel directly (statfs,
// /proc/meminfo, a directory walk) instead of forking df/free/du: the line
// refreshes every few seconds and the shell pipelines cost nine processes a
// tick. The numbers and format match what those tools printed.
func Read(p Paths) Status {
	return Status{Free: diskFree(p.Disk), RAM: memAvail(p.Meminfo), Cache: cacheSize(p.Cache)}
}

// Line renders the header line.
func (s Status) Line() string {
	return fmt.Sprintf(" Free: %s   RAM avail: %s   Cache: %s", s.Free, s.RAM, s.Cache)
}

// diskFree is `df -h`'s Avail column for path, or "N/A" when unreadable.
func diskFree(path string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "N/A"
	}
	return humanCeil(st.Bavail * uint64(st.Frsize))
}

// memAvail is the kernel's MemAvailable (`free -h`'s "available" column, memory
// usable without swapping), or "N/A" when unreadable.
func memAvail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "N/A"
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "MemAvailable:")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
				return humanRound(kb * 1024)
			}
		}
	}
	return "N/A"
}

// cacheSize is `du -sh` of dir: disk usage in blocks, directories included,
// unreadable subdirectories skipped. "N/A" when dir itself can't be read.
func cacheSize(dir string) string {
	var total uint64
	missing := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				missing = true
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				total += uint64(st.Blocks) * 512
			}
		}
		return nil
	})
	if missing {
		return "N/A"
	}
	return humanCeil(total)
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
