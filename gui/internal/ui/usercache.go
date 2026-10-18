package ui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/Enzord758/archmaid/internal/sysstat"
)

// The Clear caches cell's size line ("3.2G in ~/.cache"). The action's
// ~/.cache step runs `rm -rf "$HOME/.cache/"*` (internal/actions), so this
// measures $HOME/.cache, not $XDG_CACHE_HOME, and only what that glob removes
// (sysstat.EntriesUsage). It is measured off the GTK thread, at the start and
// after every run, one measurement at a time; a request that arrives while one
// goes is coalesced into one more after it. A walk over cacheMeasureLimit is
// abandoned and the cell shows nothing, as it does for an unreadable folder.

const cacheMeasureLimit = 5 * time.Second

// cacheMeasures counts the finished measurements (the harness reads it to
// prove the feature ran when the cell stayed hidden).
var cacheMeasures atomic.Int64

// userCacheDir is the folder Clear caches empties, "" without a $HOME.
func userCacheDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache")
}

// userCacheLine is the sub line for a measured size: "~/.cache is empty", or the size
// and the folder. "" (hidden) when the measurement failed.
func userCacheLine(size string, bytes uint64, err error) string {
	switch {
	case err != nil:
		return ""
	case bytes == 0:
		return "~/.cache is empty"
	}
	return size + " in ~/.cache"
}

// cacheLineOf measures dir and returns the sub line; "" when the walk failed
// or ctx ended first (a cancelled or timed-out measurement shows nothing).
func cacheLineOf(ctx context.Context, dir string) string {
	size, n, err := sysstat.EntriesUsage(ctx, dir)
	return userCacheLine(size, n, err)
}

// cacheMeter is the measurement bookkeeping, with no GTK in it: the shown
// line, whether a measurement goes, whether another was asked for meanwhile,
// and the generation that makes a result stale. Main thread only.
type cacheMeter struct {
	line  string
	busy  bool
	again bool
	gen   int
}

// request asks for a measurement: true when the caller should start one
// now, false when one goes (then another follows its end).
func (m *cacheMeter) request() bool {
	if m.busy {
		m.again = true
		return false
	}
	m.busy = true
	return true
}

// finish takes the result of a measurement that started at generation gen.
// changed: the shown line changed. again: start another measurement now.
// A result that began before the last invalidate is stale and never shows.
func (m *cacheMeter) finish(gen int, line string) (changed, again bool) {
	m.busy = false
	if gen == m.gen && line != m.line {
		m.line = line
		changed = true
	}
	again = m.again
	m.again = false
	return changed, again
}

// invalidate is a run's end: the shown line goes (changed when it was shown).
func (m *cacheMeter) invalidate() (changed bool) {
	m.gen++
	if m.line != "" {
		m.line = ""
		return true
	}
	return false
}

// measureUserCache starts a measurement unless one is going (then it asks
// for another when that one ends). Main thread only.
func (w *Window) measureUserCache() {
	if w.closed {
		return
	}
	dir := userCacheDir()
	if dir == "" || !w.cache.request() {
		return
	}
	gen := w.cache.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cacheMeasureLimit)
		defer cancel()
		line := cacheLineOf(ctx, dir)
		cacheMeasures.Add(1)
		post(func() {
			if w.closed {
				w.cache.busy = false
				return
			}
			changed, again := w.cache.finish(gen, line)
			if changed {
				w.updateOverview()
			}
			if again {
				w.measureUserCache()
			}
		})
	}()
}

// invalidateUserCache is a run's end: the shown size is stale until the
// next measurement, so it goes, and one is asked for.
func (w *Window) invalidateUserCache() {
	if w.cache.invalidate() {
		w.updateOverview()
	}
	w.measureUserCache()
}
