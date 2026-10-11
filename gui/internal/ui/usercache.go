package ui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Enzord758/archmaid/internal/sysstat"
)

// The Clear user cache cell's size line ("3.2G in ~/.cache"). The action
// runs `rm -rf "$HOME/.cache/"*` (internal/actions userCacheSteps), so this
// measures $HOME/.cache, not $XDG_CACHE_HOME, and only what that glob removes
// (sysstat.EntriesUsage). It is measured off the GTK thread, at the start and
// after every run, one measurement at a time; a request that arrives while one
// goes is coalesced into one more after it. A walk over cacheMeasureLimit is
// abandoned and the cell shows nothing, as it does for an unreadable folder.

const cacheMeasureLimit = 5 * time.Second

// userCacheDir is the folder Clear user cache empties, "" without a $HOME.
func userCacheDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache")
}

// userCacheLine is the sub line for a measured size: "Empty", or the size
// and the folder. "" (hidden) when the measurement failed.
func userCacheLine(size string, bytes uint64, err error) string {
	switch {
	case err != nil:
		return ""
	case bytes == 0:
		return "Empty"
	}
	return size + " in ~/.cache"
}

// measureUserCache starts a measurement unless one is going (then it asks
// for another when that one ends). Main thread only.
func (w *Window) measureUserCache() {
	if w.closed {
		return
	}
	if w.cacheBusy {
		w.cacheAgain = true
		return
	}
	dir := userCacheDir()
	if dir == "" {
		return
	}
	w.cacheBusy = true
	gen := w.cacheGen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cacheMeasureLimit)
		defer cancel()
		size, n, err := sysstat.EntriesUsage(ctx, dir)
		line := userCacheLine(size, n, err)
		post(func() {
			w.cacheBusy = false
			if w.closed {
				return
			}
			// A result that began before the last run ended is stale.
			if gen == w.cacheGen && line != w.cacheLine {
				w.cacheLine = line
				w.updateOverview()
			}
			if w.cacheAgain {
				w.cacheAgain = false
				w.measureUserCache()
			}
		})
	}()
}

// invalidateUserCache is a run's end: the shown size is stale until the
// next measurement, so it goes, and one is asked for.
func (w *Window) invalidateUserCache() {
	w.cacheGen++
	if w.cacheLine != "" {
		w.cacheLine = ""
		w.updateOverview()
	}
	w.measureUserCache()
}
