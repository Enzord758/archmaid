package steps

// The AUR install's types (docs/design/packages-contract.md, section 5),
// shared by internal/aurbuild (which builds them), the engine (which shows
// them in the sheet) and helperclient (which turns PkgLocal into the root
// verb's argv). Types only.

// AURFile kinds.
const (
	AURFilePKGBUILD = "pkgbuild"
	AURFileInstall  = "install" // an .install script: runs as root at install
	AURFilePatch    = "patch"
	AURFileOther    = "other"
)

// AURFile is one file of a package's git repo.
type AURFile struct {
	Path string // relative, "/"-separated, sanitized one line
	Kind string // AURFile* constant
	Size int64
	// Text is the file's text when it is a small text file (<= 128 KiB),
	// sanitized; "" for a large, binary or non-regular file.
	Text   string
	Sha256 string // hex of the content; "" for a symlink or other non-regular file
	// Note says why Text is empty ("binary", "symlink", "too large").
	Note string
}

// AURSource is one entry of the PKGBUILD's source array, from .SRCINFO.
type AURSource struct {
	URL      string // sanitized, one line
	Checksum bool   // false: its checksum is SKIP (or missing)
}

// AURFinding is one maintainer-audit finding the sheet shows.
type AURFinding struct {
	Name   string
	High   bool
	Reason string // sanitized one line
}

// AURPlan is what the sheet shows for one package base, and what the build
// is pinned to: the commit the user reviewed.
type AURPlan struct {
	Base    string
	Names   []string // the pkgnames that will be installed (requested + needed)
	Version string
	Commit  string // the HEAD the review is of; the build checks this out
	// Prev is the last commit the user reviewed and installed ("" = first
	// review). Diff is the unified diff Prev..Commit (capped, sanitized).
	Prev, Diff string
	// DiffNote says why Diff is empty when it should not be ("first review",
	// "the last reviewed commit is no longer in the history", "no change").
	DiffNote string
	Files    []AURFile
	Sources  []AURSource
	// Flags are the review's hints (docs: simple heuristics, not a verdict).
	Flags []string
	Audit []AURFinding
	// Deps are the repo packages installed first and the AUR packages
	// built first for this base.
	RepoDeps, AURDeps []string
	// Truncated is set when Diff or a file text was cut.
	Truncated bool
}

// PkgLocal is a pkg-install-local step's argument: the built files, as
// paths relative to the AUR cache root ("<base>/out/<file>"), in build
// order, and Hash ("sha256:<64 hex>") over their names, sizes and contents
// (aurbuild.LocalHash). helperclient builds the verb's argv from it:
// pkg-install-local Hash file...
type PkgLocal struct {
	Hash  string
	Files []string
}
