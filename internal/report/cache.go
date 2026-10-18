package report

import (
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// DefaultCacheDir is pacman's cache folder when pacman-conf names none.
const DefaultCacheDir = "/var/cache/pacman/pkg"

// CacheDirs asks `pacman-conf CacheDir` (the same source the root helper's
// tidy uses) for the cache folders, one absolute path per line. It falls
// back to DefaultCacheDir when pacman-conf is missing, fails or names no
// absolute path.
func CacheDirs(ctx context.Context, q Querier) []string {
	out, err := q(ctx, "pacman-conf", "CacheDir")
	if err == nil {
		var dirs []string
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") {
				dirs = append(dirs, path.Clean(l))
			}
		}
		if len(dirs) > 0 {
			return dirs
		}
	}
	return []string{DefaultCacheDir}
}

// paccacheDry matches the summary line of `paccache -dk0`:
//
//	==> finished dry run: 312 candidates (disk space saved: 4.20 GiB)
//
// (pacman-contrib's size_to_human prints at most two decimals, B to EiB;
// under a comma locale awk may print "4,20".) Colour codes around the
// "==>" don't matter, the match is on the rest.
var paccacheDry = regexp.MustCompile(`finished dry run: (\d+) candidates? \(disk space saved: ([0-9]+(?:[.,][0-9]+)?) (B|KiB|MiB|GiB|TiB|PiB|EiB)\)`)

// paccacheNone is what paccache prints, and exits 0, when nothing is a
// candidate: an empty cache.
const paccacheNone = "no candidate packages found for pruning"

var unitBytes = map[string]float64{
	"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "PiB": 1 << 50, "EiB": 1 << 60,
}

// ParsePaccacheDry reads the output of `paccache -dk0`: how many package
// files it would remove (all of them, with keep 0) and how much they take.
// ok is false for output that is neither a summary nor the "no candidates"
// message.
func ParsePaccacheDry(out string) (pkgs int, bytes uint64, ok bool) {
	if m := paccacheDry.FindStringSubmatch(out); m != nil {
		n, err := strconv.Atoi(m[1])
		f, ferr := strconv.ParseFloat(strings.Replace(m[2], ",", ".", 1), 64)
		if err != nil || ferr != nil {
			return 0, 0, false
		}
		return n, uint64(f*unitBytes[m[3]] + 0.5), true
	}
	if strings.Contains(out, paccacheNone) {
		return 0, 0, true
	}
	return 0, 0, false
}

// CacheStillHolds is what the package cache holds after the run: the totals
// of a read-only `paccache -dk0 --nocolor` (as the user, nothing is
// removed), or, when paccache is missing, fails or prints something else,
// the cache folders summed. ok is false when neither works.
func (c *Collector) cacheStillHolds(ctx context.Context) (CacheHold, bool) {
	out, err := c.cfg.Query(ctx, "paccache", "-dk0", "--nocolor")
	if err == nil {
		if n, b, ok := ParsePaccacheDry(string(out)); ok {
			return CacheHold{Bytes: b, Packages: n, Source: "paccache"}, true
		}
	}
	s := c.meas.size(ctx, c.cacheDirs(ctx)...)
	if !s.OK {
		return CacheHold{}, false
	}
	return CacheHold{Bytes: s.Bytes, Packages: s.Pkgs, Source: "folder"}, true
}
