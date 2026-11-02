// Package aursearch is the AUR RPC v5 search and info client for the
// Packages page (docs/design/packages-contract.md, section 2). Types only
// here; the client is Dev 2's.
//
// Network rules, the same as internal/audit: an explicit BaseURL (empty
// fails; it never defaults to the real AUR inside a test), a per-request
// timeout, the response body capped at MaxResponse (read through a limited
// reader; a longer body is an error), unrequested or malformed entries
// dropped, every text field cleaned with internal/sanitize. Allowed
// imports: the standard library (net/http), internal/pkgdb for the types,
// internal/sanitize. Not allowed: os/exec, steps, engine, gotk4.
package aursearch

import (
	"context"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

const (
	// DefaultBaseURL is the RPC v5 root; /search/<term> and /info are under it.
	DefaultBaseURL = "https://aur.archlinux.org/rpc/v5"
	// MinTerm is the shortest search term (runes, after trimming).
	MinTerm = 2
	// MaxTerm is the longest (bytes); a longer term is refused, not cut.
	MaxTerm = 64
	// MaxHits caps what Search returns after ranking.
	MaxHits = 40
	// MaxInfoNames is how many names one Info request carries; Info splits
	// a longer list into requests.
	MaxInfoNames = 150
	// MaxResponse caps one response body.
	MaxResponse = 8 << 20
	// DefaultTimeout bounds one request (the search is interactive).
	DefaultTimeout = 8 * time.Second
)

// Config builds a Client. BaseURL is required; a zero Timeout means
// DefaultTimeout.
type Config struct {
	BaseURL string
	Timeout time.Duration
}

// Searcher is what the GUI (gui/internal/pkgview) depends on, so tests
// fake it. The real one is the Client New(Config) returns.
//
// Search asks /search/<term>?by=name-desc, ranks (exact name, prefix, name
// contains, then description; ties by Popularity, then Votes), drops
// duplicates and returns at most MaxHits packages with Repo == pkgdb.RepoAUR
// and AUR set. A term shorter than MinTerm or longer than MaxTerm, or with a
// control character, is ErrBadTerm without a request. The RPC's "Too many
// package results" is ErrTooMany. ctx cancels the request at once (the GUI
// cancels on every new keystroke).
//
// Info asks /info for exact names, in MaxInfoNames chunks, and returns the
// packages it found (including MakeDepends and CheckDepends); names that do
// not exist are absent, not an error.
type Searcher interface {
	Search(ctx context.Context, term string) ([]pkgdb.Package, error)
	Info(ctx context.Context, names []string) ([]pkgdb.Package, error)
}
