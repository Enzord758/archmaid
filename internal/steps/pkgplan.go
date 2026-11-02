package steps

import "fmt"

// The Packages page's plan types (docs/design/packages-contract.md,
// section 3). Types and the refusal error only; the helper builds the plan,
// helperclient decodes and checks it, the engine shows it.

// Step IDs of a Packages run. AUR steps carry the package base:
// "aur-fetch:foo", "aur-build:foo".
const (
	IDPkgInstall      = "pkg-install"
	IDPkgRemove       = "pkg-remove"
	IDAURFetchPrefix  = "aur-fetch:"
	IDAURBuildPrefix  = "aur-build:"
	IDPkgInstallLocal = "pkg-install-local"
)

// PkgMode is install or remove.
type PkgMode int

const (
	PkgInstall PkgMode = iota
	PkgRemove
)

// PkgRequest is what the page asks for. Names are validated as the helper
// does (helperclient.namePattern, 1–32, none twice). WithConfig is remove's
// "also delete config files" (-Rns instead of -Rs). AsDeps is install's
// --asdeps, set only by the AUR flow for build dependencies.
type PkgRequest struct {
	Mode       PkgMode
	Names      []string
	WithConfig bool
	AsDeps     bool
}

// PkgRow is one package of a plan: a target or what comes with it.
type PkgRow struct {
	Name, Version string
	// OldVersion is the installed version when the row replaces one ("" for
	// a new package; an install plan with any OldVersion is "updates
	// pending", refused).
	OldVersion string
	// Repo is the sync repo for an install row, "" for a remove row.
	Repo string
	// Target: one of the request's Names (else pulled in as a dependency
	// for install, or taken along by the cascade for remove).
	Target bool
	// Download and Installed are bytes; 0 = unknown (not part of the hash).
	Download, Installed int64
}

// PkgPlan is the helper's plan for an install or a remove, as plan-install
// or plan-remove reported it. Hash ("sha256:<64 hex>") commits to Mode,
// WithConfig, AsDeps, the sorted Targets and the sorted "repo name version"
// lines of Rows ("-" as the repo of a remove row); sizes and OldVersion are
// not hashed. helperclient.PkgPlanHash is the client copy, tied to the
// helper's by a test.
type PkgPlan struct {
	Mode       PkgMode
	WithConfig bool
	AsDeps     bool
	Targets    []string
	Rows       []PkgRow
	Hash       string
	// Warnings are short sanitized notes ("pulls in 31 packages").
	Warnings []string
}

// RefuseReason is why a plan or a run was refused. The strings are the
// helper's result event "reason" field: a stable API.
type RefuseReason string

const (
	RefuseUpdatesPending RefuseReason = "updates-pending"
	RefuseConflict       RefuseReason = "conflict" // replaces or conflicts with an installed package
	RefuseProtected      RefuseReason = "protected"
	RefuseNotFound       RefuseReason = "not-found" // install: not in a sync DB by exact name
	RefuseNotInstalled   RefuseReason = "not-installed"
	RefuseRequired       RefuseReason = "required-by" // remove: others still need it
	RefuseBusy           RefuseReason = "busy"        // pacman or another helper holds the lock
	RefuseStale          RefuseReason = "stale"       // the plan changed since the confirm
	RefuseBadRequest     RefuseReason = "bad-request"
)

// PkgRefusal is the error PlanPackages and a package step return when the
// helper refused: nothing was changed. Names are the packages the reason is
// about; Count is the pending updates for RefuseUpdatesPending. Detail is
// sanitized one-line text for the page. Plan is the new plan when the
// reason is RefuseStale (the helper emits it), else nil.
type PkgRefusal struct {
	Reason RefuseReason
	Names  []string
	Count  int
	Detail string
	Plan   *PkgPlan
}

func (r *PkgRefusal) Error() string {
	return fmt.Sprintf("packages refused (%s): %s", r.Reason, r.Detail)
}
