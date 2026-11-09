package aursearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

var (
	// ErrBadTerm: the term is shorter than MinTerm runes, longer than MaxTerm
	// bytes, not valid text or holds a control character. Nothing was sent.
	ErrBadTerm = errors.New("search term not allowed")
	// ErrTooMany: the AUR refused the search with "Too many package results".
	ErrTooMany = errors.New("too many AUR results, refine the search")
	// ErrNoURL: the Config has no BaseURL (it never defaults to the real AUR).
	ErrNoURL = errors.New("no AUR URL configured")
)

const (
	descMax  = 512
	errorMax = 200
)

// Client is the real Searcher. Build it with New. Search terms leave the
// machine here: the caller debounces typing and cancels the previous request.
type Client struct{ cfg Config }

var _ Searcher = (*Client)(nil)

// New returns a Client. An empty BaseURL is not an error here; every call
// then fails with ErrNoURL, so a test that forgets the URL never reaches the
// real AUR.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type rawPackage struct {
	Name           string
	PackageBase    string
	Version        string
	Description    *string
	URL            *string
	URLPath        string
	Maintainer     *string
	NumVotes       int
	Popularity     float64
	OutOfDate      *int64
	FirstSubmitted int64
	LastModified   int64
	License        []string
	Groups         []string
	Depends        []string
	MakeDepends    []string
	CheckDepends   []string
	OptDepends     []string
	Provides       []string
	Conflicts      []string
	Replaces       []string
}

// Search implements Searcher. Ranking: exact name, name prefix, name
// contains, then the rest (description matches); ties by popularity, then
// votes, then name.
func (c *Client) Search(ctx context.Context, term string) ([]pkgdb.Package, error) {
	term = strings.TrimSpace(term)
	if utf8.RuneCountInString(term) < MinTerm || len(term) > MaxTerm || !utf8.ValidString(term) ||
		strings.IndexFunc(term, unicode.IsControl) >= 0 {
		return nil, ErrBadTerm
	}
	raw, err := c.do(ctx, http.MethodGet, "/search/"+url.PathEscape(term)+"?by=name-desc", nil, "search")
	if err != nil {
		return nil, err
	}
	lterm := strings.ToLower(term)
	rank := func(name string) int {
		switch n := strings.ToLower(name); {
		case n == lterm:
			return 0
		case strings.HasPrefix(n, lterm):
			return 1
		case strings.Contains(n, lterm):
			return 2
		}
		return 3
	}
	sort.SliceStable(raw, func(i, j int) bool {
		a, b := raw[i], raw[j]
		switch {
		case rank(a.Name) != rank(b.Name):
			return rank(a.Name) < rank(b.Name)
		case a.Popularity != b.Popularity:
			return a.Popularity > b.Popularity
		case a.NumVotes != b.NumVotes:
			return a.NumVotes > b.NumVotes
		}
		return a.Name < b.Name
	})
	out := make([]pkgdb.Package, 0, min(len(raw), MaxHits))
	seen := map[string]bool{}
	for _, r := range raw {
		if len(out) == MaxHits {
			break
		}
		if !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, convert(r))
		}
	}
	return out, nil
}

// Info implements Searcher. Names that are not legal package names are never
// sent; names the AUR does not know are absent from the result.
func (c *Client) Info(ctx context.Context, names []string) ([]pkgdb.Package, error) {
	var asked []string
	want := map[string]bool{}
	for _, n := range names {
		if pkgdb.ValidName(n) && !want[n] {
			want[n] = true
			asked = append(asked, n)
		}
	}
	var out []pkgdb.Package
	for start := 0; start < len(asked); start += MaxInfoNames {
		form := url.Values{}
		for _, n := range asked[start:min(start+MaxInfoNames, len(asked))] {
			form.Add("arg[]", n)
		}
		raw, err := c.do(ctx, http.MethodPost, "/info", strings.NewReader(form.Encode()), "multiinfo")
		if err != nil {
			return nil, err
		}
		for _, r := range raw {
			if want[r.Name] { // ignore anything that was not asked about
				out = append(out, convert(r))
			}
		}
	}
	return out, nil
}

// do makes one request: bounded by the timeout, the body capped at
// MaxResponse, malformed entries dropped, errors on one clean line.
func (c *Client) do(ctx context.Context, method, pathQuery string, body io.Reader, wantType string) ([]rawPackage, error) {
	if c.cfg.BaseURL == "" {
		return nil, ErrNoURL
	}
	d := c.cfg.Timeout
	if d <= 0 {
		d = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	// A redirect stays on the AUR's own scheme and host: the request carries
	// the user's package names (info), and a redirect to another host, or down
	// to http, would hand them over.
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return errors.New("AUR redirect off its own host refused")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+pathQuery, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "archmaid")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var reply struct {
		Type    string
		Error   string
		Results []rawPackage
	}
	// Capped: a response past MaxResponse is cut off and fails to decode.
	err = json.NewDecoder(io.LimitReader(resp.Body, MaxResponse)).Decode(&reply)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AUR returned HTTP %d", resp.StatusCode)
	}
	if err != nil {
		return nil, fmt.Errorf("unreadable AUR response: %w", err)
	}
	if reply.Type == "error" {
		if strings.HasPrefix(strings.ToLower(reply.Error), "too many") {
			return nil, ErrTooMany
		}
		return nil, fmt.Errorf("AUR error: %s", sanitize.Clip(sanitize.OneLine(reply.Error), errorMax))
	}
	if reply.Type != wantType {
		return nil, fmt.Errorf("unexpected AUR response type %q", sanitize.Clip(sanitize.OneLine(reply.Type), 20))
	}
	out := reply.Results[:0]
	for _, r := range reply.Results {
		if pkgdb.ValidName(r.Name) {
			out = append(out, r)
		}
	}
	return out, nil
}

func convert(r rawPackage) pkgdb.Package {
	a := &pkgdb.AUR{
		PackageBase:  sanitize.Field(r.PackageBase, audit.FieldMax),
		Votes:        max(r.NumVotes, 0),
		Popularity:   max(r.Popularity, 0),
		URLPath:      sanitize.Field(r.URLPath, 200),
		MakeDepends:  pkgdb.Deps(r.MakeDepends, false),
		CheckDepends: pkgdb.Deps(r.CheckDepends, false),
	}
	if r.Maintainer != nil {
		a.Maintainer = sanitize.Field(*r.Maintainer, audit.FieldMax)
	}
	if r.OutOfDate != nil {
		a.OutOfDate = stamp(*r.OutOfDate)
	}
	a.LastModified, a.FirstSubmitted = stamp(r.LastModified), stamp(r.FirstSubmitted)
	p := pkgdb.Package{
		Name:       r.Name,
		Version:    sanitize.Field(r.Version, 64),
		Repo:       pkgdb.RepoAUR,
		Licenses:   cleanList(r.License),
		Groups:     cleanList(r.Groups),
		Depends:    pkgdb.Deps(r.Depends, false),
		OptDepends: pkgdb.Deps(r.OptDepends, true),
		Conflicts:  pkgdb.Deps(r.Conflicts, false),
		Replaces:   pkgdb.Deps(r.Replaces, false),
		Provides:   pkgdb.Deps(r.Provides, false),
		AUR:        a,
	}
	if r.Description != nil {
		p.Description = sanitize.Field(*r.Description, descMax)
	}
	if r.URL != nil {
		p.URL = sanitize.Field(*r.URL, 256)
	}
	return p
}

func stamp(n int64) time.Time {
	if n <= 0 || n > 1<<40 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func cleanList(in []string) []string {
	if len(in) > 64 {
		in = in[:64]
	}
	var out []string
	for _, s := range in {
		if s = sanitize.Field(s, 128); s != "" {
			out = append(out, s)
		}
	}
	return out
}
