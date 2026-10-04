package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── system status ────────────────────────────────────────────────────────────

type statusMsg string

// statusNowMsg is a one-off refresh. Unlike statusMsg it never reschedules, so
// there is only ever the one tick loop that Init starts.
type statusNowMsg string

// refreshStatus recomputes the disk/ram/cache line every few seconds so the
// header stays live (the bit gum couldn't do without redrawing the world).
// Only Init may start this loop; anything else wants refreshStatusNow.
func refreshStatus() tea.Cmd {
	return tickFn(4*time.Second, func(time.Time) tea.Msg {
		return statusMsg(systemStatus())
	})
}

// refreshStatusNow updates the line once, immediately — e.g. after a run so
// the freed space shows without waiting for the next tick.
func refreshStatusNow() tea.Cmd {
	return func() tea.Msg { return statusNowMsg(systemStatus()) }
}

// tickFn is tea.Tick, behind a seam so the transcript harness can drop the
// timers (status refresh, sudo keep-alive) instead of sleeping on them.
var tickFn = tea.Tick

// The roots systemStatus reads, as vars so tests can point them at fixtures.
var (
	statusDiskPath  = "/"
	statusMeminfo   = "/proc/meminfo"
	statusCacheRoot = "/var/cache/pacman/pkg"
)

// systemStatus renders the header line. It reads the kernel directly (statfs,
// /proc/meminfo, a directory walk) instead of forking df/free/du: the line
// refreshes every few seconds and the shell pipelines cost nine processes a
// tick. The numbers and format match what those tools printed.
func systemStatus() string {
	return fmt.Sprintf(" Free: %s   RAM free: %s   Cache: %s",
		diskFree(statusDiskPath), memFree(statusMeminfo), cacheSize(statusCacheRoot))
}

// diskFree is `df -h`'s Avail column for path, or "N/A" when unreadable.
func diskFree(path string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "N/A"
	}
	return humanCeil(st.Bavail * uint64(st.Frsize))
}

// memFree is `free -h`'s free column (MemFree), or "N/A" when unreadable.
func memFree(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "N/A"
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "MemFree:")
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
