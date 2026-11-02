// Package report builds the run report: what a finished queue changed on
// the machine, for the end-of-run summary (the TUI's block, the GUI's hero)
// and the summary object in last-run.json. It is UI-free and needs no root.
//
// # Where the numbers come from
//
//   - Packages: the [ALPM] upgraded / installed / removed lines of
//     /var/log/pacman.log written after the byte offset the log had when the
//     queue started (pacmanlog.go), folded to each package's net change.
//     Any other pacman run while archmaid runs (a second terminal, a
//     scheduled update) writes to the same log and counts too. AUR packages
//     are the foreign ones (`pacman -Qqm`, asked once at the end).
//   - Orphans removed: the orphan step's own confirmed lists
//     (steps.Step.Items and Cascade), not the log.
//   - Space freed: the size of each cleaning step's target as the user,
//     before the step starts and after it ends (measure.go), clamped at
//     zero; a target the user can't read, or a walk that ran out of time,
//     is unknown, never a guess.
//   - Cache still holds: a read-only `paccache -dk0` dry run at the end,
//     or the summed cache folder when that fails (cache.go).
//   - Attention: a restart (restart.go), new .pacnew/.pacsave files (the
//     log's warning lines), a high AUR audit finding.
//
// Everything is keyed by step ID, so a step that is not in the queue simply
// has no row. A Collector is owned by one goroutine (the Driver's loop).
//
// # Left out on purpose
//
// AUR updates held back by the 72 h yay cooldown are not reported: the
// widget's updates-cache.json (aur_held) is rewritten by a check the Driver
// only queues after the run (driver.RefreshUpdates, --no-block), so at
// report time it is the count from before the run, and it holds a count,
// not the package names or when they install; the cooldown hook lives
// outside this repo and keeps no log archmaid knows. A wrong "held back"
// is worse than none.
package report

import "time"

// Report is the run report. Sections that don't apply to the action, or
// that couldn't be read, are nil or empty; Notes says what was left out.
type Report struct {
	Action   string        // the menu action's id
	Started  time.Time     // when the queue started
	Finished time.Time     // when the report was made
	Elapsed  time.Duration // the run's duration
	Steps    StepTally

	// Packages is what pacman.log says changed; nil for an action that
	// changes no packages, or when the log couldn't be read (see Notes).
	Packages *Packages
	// Orphans is what the orphan removal took, nil when it didn't run.
	Orphans *Orphans
	// Freed is a row per cleaning step that ran, in display order (see
	// areaOrder). FreedBytes sums the known rows; FreedPartial says some row
	// was unknown, so the sum is a lower bound.
	Freed        []Area
	FreedBytes   uint64
	FreedPartial bool
	// Cache is what the package cache still holds after the run; nil when
	// no step touched it.
	Cache *CacheHold
	// Attention are the things that need the user, in the order shown.
	Attention []Attention
	// Audit is the AUR audit step's findings; nil when it didn't run or
	// failed. Only High ones are also an Attention item.
	Audit *AuditNote
	// Notes say what could not be determined, one short sentence each.
	Notes []string
}

// StepTally counts a queue's steps as the summary line does.
type StepTally struct{ Done, Skipped, Failed int }

// Total is the number of steps counted.
func (t StepTally) Total() int { return t.Done + t.Skipped + t.Failed }

// Pkg is one package's net change over the run.
type Pkg struct {
	Name string
	Old  string // "" for an install
	New  string // "" for a removal
	AUR  bool   // foreign (pacman -Qqm); never set on a removal
}

// Packages are the net package changes over the run, each list in the order
// pacman first touched them.
type Packages struct {
	Upgraded, Installed, Removed, Downgraded []Pkg
	// AURUpgraded and AURInstalled count the foreign ones among them.
	AURUpgraded, AURInstalled int
	// AURKnown: pacman -Qqm answered, so the AUR flags and counts are real.
	AURKnown bool
	// SizeChange is the net change in installed size, in bytes (negative
	// for a removal), of a Packages page run, from the plan the user
	// confirmed (pacman's installed sizes) for the steps that ended OK;
	// SizeKnown is false when the plan held none (an AUR build installs
	// from files). Not read from the log, which holds no sizes.
	SizeChange int64
	SizeKnown  bool
	// Partial: pacman.log was rotated or truncated during the run, so what
	// was written before that is not counted.
	Partial bool
}

// Orphans is what the orphan removal took: Names are the orphans the
// confirm listed, Total everything -Rns removed with them.
type Orphans struct {
	Names []string
	Total int
}

// Area is one cleaned target. ID is the cleaning step's ID where one step
// has one area (the AUR build cache is "yay-cache"); under sudo, yay -Sc's
// pacman half is "pkg-cache-uninstalled", as under polkit.
type Area struct {
	ID    string
	Label string
	Bytes uint64
	Known bool // false: couldn't be measured; Bytes is 0
}

// CacheHold is what `paccache -dk0` (every package file in the cache is a
// candidate) says the cache still holds.
type CacheHold struct {
	Bytes    uint64
	Packages int
	// Source is "paccache" (its dry run) or "folder" (the fallback: the
	// cache folder summed).
	Source string
}

// Attention is one item for the "Needs your attention" list.
type Attention struct {
	// Kind: "restart", "pacnew", "pacsave" or "audit".
	Kind string
	// Short is a few words for the widget ("Restart recommended").
	Short string
	// Text is the full one-line item.
	Text string
	// Detail are the names behind it (the files, the packages).
	Detail []string
}

// AuditNote is the AUR audit's outcome as the report keeps it.
type AuditNote struct {
	Total    int      // foreign packages checked
	Findings int      // flagged packages
	High     int      // of them high severity
	Names    []string // the flagged packages
}

// Attention kinds.
const (
	KindRestart = "restart"
	KindPacnew  = "pacnew"
	KindPacsave = "pacsave"
	KindAudit   = "audit"
)

// Area IDs. They are step IDs (internal/steps) where a step has one; the
// pacman half of yay -Sc has the ID the polkit queue gives that step.
const (
	AreaPaccache       = "paccache"
	AreaPaccacheWipe   = "paccache-wipe"
	AreaPkgUninstalled = "pkg-cache-uninstalled"
	AreaYay            = "yay-cache"
	AreaJournal        = "journal"
	AreaTmpfiles       = "tmpfiles"
	AreaUserCache      = "user-cache"
)

// Summary is the part of the report last-run.json keeps (logstore.RunSummary
// is its on-disk shape).
type Summary struct {
	Upgraded   int
	AUR        int
	Installed  int // a Packages page run: packages installed / removed
	Removed    int
	FreedBytes uint64
	Attention  []string
}

// ActionPackages is the Packages page run's action id (actions.IDPackages;
// this package does not import actions).
const ActionPackages = "packages"

// Summary is r reduced to the numbers the widget shows.
func (r Report) Summary() Summary {
	var s Summary
	if r.Packages != nil {
		s.Upgraded = len(r.Packages.Upgraded)
		s.AUR = r.Packages.AURUpgraded
		if r.Action == ActionPackages {
			s.Installed, s.Removed = len(r.Packages.Installed), len(r.Packages.Removed)
		}
	}
	s.FreedBytes = r.FreedBytes
	for _, a := range r.Attention {
		s.Attention = append(s.Attention, a.Short)
	}
	return s
}

// HasData reports whether the report holds anything worth keeping in
// last-run.json (a package change, a measured row or an attention item).
func (r Report) HasData() bool {
	return r.Packages != nil || len(r.Freed) > 0 || len(r.Attention) > 0
}
