package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// downloadsTried is what the downloads discovery attempts, for a failed
// look's record.
const downloadsTried = "Tried: the downloads folder from --downloads, $ARCHMAID_DOWNLOADS, XDG_DOWNLOAD_DIR, " +
	"~/Downloads or ~/Download, then its entries older than 30 days (by ctime)"

// discoverDownloads resolves the folder and lists its old entries. Deleting
// them is an in-process step that re-checks each entry first (env's
// DeleteDownloads; the safety properties are internal/downloads').
//
// Either way it says what it checked: the folder, how many entries, the
// newest and oldest by ctime, and the 30-day cutoff; its Details list each
// entry with its age.
func discoverDownloads(_ context.Context, env Env) Discovery {
	opt := env.Downloads()
	dir, err := downloads.Resolve(opt)
	if err != nil {
		msg := sanitize.String(err.Error(), sanitize.Plain)
		return Notice{Text: msg, Err: sanitize.OneLine(msg), Details: []string{downloadsTried}}
	}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	at := now()
	plan, err := downloads.Scan(dir, at, opt)
	if err != nil {
		msg := sanitize.String(err.Error(), sanitize.Plain)
		return Notice{Text: msg, Err: sanitize.OneLine(msg), Details: []string{downloadsTried}}
	}
	ck := downloadsChecked(plan, at)
	if len(plan.Names) == 0 {
		return Notice{Text: ck.Summary, Details: ck.Details, Log: ck.Log}
	}
	return Pending{Steps: []steps.Step{{
		ID:    steps.IDDownloads,
		Title: "Deleting old downloads…", LogLabel: "Old downloads deleted",
		Run:      env.DeleteDownloads(plan),
		Optional: true, AlwaysLog: true,
		Confirm: plan.Confirm(),
		Items:   plan.Names,
	}}, Checked: ck}
}

// maxDetails bounds a discovery's Details; the rest is counted.
const maxDetails = 500

// downloadsChecked is the record of a downloads scan: every top-level
// entry with the age of its own ctime (what the scan judges first), and
// for one old by its own ctime but not in the plan, that it was kept (it
// holds something newer, or another filesystem). It reads only what Scan
// read: names and Lstat, nothing followed.
func downloadsChecked(plan downloads.Plan, now time.Time) Checked {
	ents, err := os.ReadDir(plan.Dir)
	cutoff := now.Add(-downloads.MaxAge)
	type aged struct {
		name string
		age  time.Duration
		ok   bool
	}
	var items []aged
	for _, e := range ents {
		it := aged{name: e.Name()}
		if fi, err := os.Lstat(filepath.Join(plan.Dir, e.Name())); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				it.age, it.ok = now.Sub(time.Unix(st.Ctim.Unix())), true
			}
		}
		items = append(items, it)
	}
	slices.SortStableFunc(items, func(a, b aged) int {
		switch {
		case a.age > b.age:
			return -1
		case a.age < b.age:
			return 1
		}
		return 0
	})
	var details []string
	var newest, oldest time.Duration
	seen := false
	for _, it := range items {
		if it.ok {
			if !seen || it.age < newest {
				newest = it.age
			}
			oldest, seen = max(oldest, it.age), true
		}
	}
	for i, it := range items {
		if len(details) == maxDetails {
			details = append(details, fmt.Sprintf("… and %d more", len(items)-i))
			break
		}
		line := sanitize.QuoteName(it.name, 200) + ": "
		switch {
		case !it.ok:
			line += "age unknown, kept"
		case slices.Contains(plan.Names, it.name):
			line += age(it.age) + " old, older than 30 days"
		case it.age > downloads.MaxAge:
			line += age(it.age) + " old, kept: something inside changed within 30 days, or it is on another filesystem"
		default:
			line += age(it.age) + " old"
		}
		details = append(details, line)
	}
	n := len(items)
	var summary string
	switch {
	case err != nil:
		summary = fmt.Sprintf("Checked %s: no entries older than 30 days.", plan.Shown)
	case n == 0:
		summary = fmt.Sprintf("Checked %s: it is empty, nothing older than 30 days.", plan.Shown)
	case len(plan.Names) == 0:
		summary = fmt.Sprintf("Checked %d %s in %s: none older than 30 days (newest %s, oldest %s).",
			n, plural(n, "item", "items"), plan.Shown, age(newest), age(oldest))
	default:
		summary = fmt.Sprintf("Checked %d %s in %s: %d older than 30 days (oldest %s).",
			n, plural(n, "item", "items"), plan.Shown, len(plan.Names), age(oldest))
	}
	return Checked{Summary: summary, Details: details,
		Log: fmt.Sprintf("Folder: %s\nCutoff: ctime before %s (30 days)\n", plan.Shown, cutoff.Format(time.DateTime))}
}

// age is d for a person: minutes under an hour, hours under two days, else
// days.
func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", max(0, int(d/time.Minute)))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
}
