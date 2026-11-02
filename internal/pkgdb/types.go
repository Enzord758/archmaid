// Package pkgdb reads pacman's databases for the Packages page: the sync
// DBs (/var/lib/pacman/sync/*.db, gzip or zstd tar) and the local DB
// (/var/lib/pacman/local/*/desc), in plain Go, read only, as the user. It
// starts no process, makes no network call and never takes pacman's lock.
//
// This file is the contract (docs/design/packages-contract.md, section 1):
// the types the GUI, aursearch and the engine's preview share. The logic
// (Open, Search, Installed) is Dev 2's and goes in other files. Allowed
// imports: the standard library, internal/sanitize and the zstd decoder.
// Not allowed: gotk4, charmbracelet, os/exec, net, any other archmaid
// package. Every string read from a database or the AUR is untrusted text
// and is cleaned (sanitize.String(s, sanitize.Plain); one line for name,
// version and repo) before it is stored here.
package pkgdb

import "time"

// RepoAUR is Package.Repo of an AUR package.
const RepoAUR = "aur"

// Reason is why an installed package is installed (the local DB's %REASON%).
type Reason int

const (
	ReasonNone     Reason = iota // not installed
	ReasonExplicit               // installed on purpose
	ReasonDep                    // installed as a dependency
)

// Dep is one dependency, conflict, provide or replace entry as pacman
// writes it: Raw "foo>=1.2", Name "foo". For an optional dependency Desc is
// the text after ": " ("for GUI support").
type Dep struct {
	Name string
	Raw  string
	Desc string
}

// AUR is what only an AUR package has (RPC v5 search and info).
type AUR struct {
	PackageBase    string
	Maintainer     string // "" = orphaned
	Votes          int
	Popularity     float64
	OutOfDate      time.Time // zero = not flagged
	LastModified   time.Time
	FirstSubmitted time.Time
	// URLPath is the RPC's URLPath ("/cgit/aur.git/snapshot/foo.tar.gz");
	// the page's link is "https://aur.archlinux.org/packages/" + Name, not
	// derived from this.
	URLPath string
	// MakeDepends and CheckDepends come from the info call only (a search
	// hit leaves them nil); Depends and OptDepends are on Package.
	MakeDepends, CheckDepends []Dep
}

// Package is one package as the page shows it: a sync-DB entry, an AUR
// entry or an installed one, with the installed state merged in.
type Package struct {
	Name, Version string
	// Repo is the sync repository's name ("core", "extra", "cachyos-v3", a
	// local mirror's name) or RepoAUR; for an installed package in no sync
	// DB it is "" (Foreign). The GUI shows it as a neutral tag.
	Repo        string
	Description string
	URL         string // upstream URL
	Licenses    []string
	Groups      []string

	// Sizes in bytes. DownloadSize is 0 for an AUR or installed-only entry.
	DownloadSize, InstalledSize int64

	Depends, OptDepends, Conflicts, Replaces, Provides []Dep

	Installed        bool
	InstalledVersion string // "" if not installed; may differ from Version (an update)
	Reason           Reason // ReasonNone if not installed
	// RequiredBy and OptionalFor are the installed packages that depend on
	// this one (the local DB's reverse links, computed by pkgdb); only set
	// for an installed package.
	RequiredBy, OptionalFor []string
	// Orphan: installed as a dependency and nothing requires or optionally
	// requires it. The same rule as `pacman -Qdt`; a test ties the two.
	Orphan bool
	// Foreign: installed and in no sync DB (`pacman -Qm`: AUR packages and
	// hand-built ones).
	Foreign bool
	// Update: installed, in a sync DB at another version (as -Qu, by
	// vercmp). The page shows it; the helper's -Qu is the authority.
	Update bool

	AUR *AUR // non-nil only for Repo == RepoAUR
}

// View is a filter of the installed list.
type View int

const (
	ViewExplicit     View = iota // Reason == ReasonExplicit
	ViewDependencies             // Reason == ReasonDep and not an orphan
	ViewOrphans                  // Orphan (listed; removed by Full Cleanup, not here)
	ViewForeign                  // Foreign
)
