package health

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The news feed and its limits. The feed is untrusted input.
const (
	NewsURL = "https://archlinux.org/feeds/news/"
	// NewsHost is the only host a link, or a redirect, may point to.
	NewsHost = "archlinux.org"
	// NewsTimeout bounds the one GET.
	NewsTimeout = 10 * time.Second
	// MaxNewsBody caps the feed (and the cache file read back).
	MaxNewsBody = 1 << 20
	// NewsTTL: a cached feed younger than this is used without a request.
	NewsTTL = 3 * time.Hour
	// MaxNewsItems is how many items are kept, newest first.
	MaxNewsItems = 30
	// TitleMax and LinkMax cap an item's title (runes) and link (bytes).
	TitleMax = 120
	LinkMax  = 300

	newsCacheName = "arch-news.xml"
)

// NewsItem is one sanitized feed item.
type NewsItem struct {
	Title string
	Link  string // https://archlinux.org/... or ""
	Date  time.Time
}

// UnreadResult is what UnreadNews returns.
type UnreadResult struct {
	// Items are the news posted after Since, newest first (at most
	// MaxNewsItems). When Since is zero (no full update found in the log) there
	// are none: no items, no error.
	Items []NewsItem
	// Since is the last full system update ("" time when unknown).
	Since time.Time
	// FromCache: the feed could not be fetched; Items come from the cache.
	FromCache bool
	// CacheAge is the age of the feed used when FromCache (else 0).
	CacheAge time.Duration
}

// UnreadNews is the news check without the others: the Arch news posted since
// the last full system update, for the confirm sheets. An error means there
// is no feed at all (offline and no cache, or nothing parsable); the sheets
// then show no news block. It makes at most one request, like Run's check.
func UnreadNews(ctx context.Context, d Deps) (UnreadResult, error) {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	return unread(ctx, d)
}

func unread(ctx context.Context, d Deps) (UnreadResult, error) {
	items, fromCache, age, err := loadNews(ctx, d)
	if err != nil {
		return UnreadResult{}, err
	}
	res := UnreadResult{FromCache: fromCache, CacheAge: age}
	if since, ok := lastUpgrade(d); ok {
		res.Since = since
		for _, it := range items {
			if it.Date.After(since) {
				res.Items = append(res.Items, it)
			}
		}
	}
	return res, nil
}

func lastUpgrade(d Deps) (time.Time, bool) {
	sr, f, err := openLogTail(d.PacmanLog)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	return LastFullUpgrade(sr)
}

func checkNews(ctx context.Context, d Deps) Check {
	res, err := unread(ctx, d)
	if err != nil {
		return unknown(IDNews, TitleNews, sanitize.Field(newsReason(err), 80))
	}
	suffix := ""
	if res.FromCache {
		suffix = " (offline, cached " + Ago(res.CacheAge) + " ago)"
	}
	if res.Since.IsZero() {
		// No reference date: say so, but do not raise an alarm.
		return Check{State: Unknown, Detail: "No full update found in the pacman log" + suffix}
	}
	n := len(res.Items)
	if n == 0 {
		if res.FromCache {
			return Check{State: Unknown, Detail: "Can't reach archlinux.org; nothing new " + Ago(res.CacheAge) + " ago"}
		}
		return Check{State: OK, Detail: "Nothing new since your last update"}
	}
	first := res.Items[0]
	detail := fmt.Sprintf("%d new since your last update: %s", n, first.Title)
	if n > 1 {
		detail = fmt.Sprintf("%d new since your last update, newest: %s", n, first.Title)
	}
	return Check{State: Look, Detail: sanitize.Field(detail+suffix, DetailMax), Link: first.Link}
}

// newsReason is the short reason shown for an Unknown news check.
func newsReason(err error) string {
	switch {
	case errors.Is(err, errNoFeed):
		return "Can't read the Arch news feed"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "Arch news timed out"
	}
	return "Can't reach archlinux.org"
}

var errNoFeed = errors.New("unreadable news feed")

// Ago writes a duration the way the sheet does: "5 min", "3 h", "2 d".
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d d", int(d/(24*time.Hour)))
}

// loadNews returns the feed's items, newest first: from the cache when it is
// younger than NewsTTL, else fetched (and cached). When the fetch or its parse
// fails, an older cache is used instead (fromCache, with its age); with no
// usable cache the error is returned.
func loadNews(ctx context.Context, d Deps) (items []NewsItem, fromCache bool, age time.Duration, err error) {
	now := d.now()
	path := ""
	if d.CacheDir != "" {
		path = filepath.Join(d.CacheDir, "archmaid", newsCacheName)
	}
	cached, cachedAge, haveCache := readNewsCache(path, now)
	if haveCache && cachedAge < NewsTTL {
		return cached, false, 0, nil
	}
	var fetchErr error
	if d.HTTPGet == nil || d.NewsURL == "" {
		fetchErr = errors.New("no network")
	} else {
		var body []byte
		body, fetchErr = d.HTTPGet(ctx, d.NewsURL)
		if fetchErr == nil {
			var fresh []NewsItem
			if fresh, fetchErr = ParseFeed(body, now); fetchErr == nil {
				writeNewsCache(path, body) // best effort: a failed write loses only the cache
				return fresh, false, 0, nil
			}
			fetchErr = fmt.Errorf("%w: %v", errNoFeed, fetchErr)
		}
	}
	if haveCache {
		return cached, true, cachedAge, nil
	}
	return nil, false, 0, fetchErr
}

// readNewsCache reads the cache file at path: a regular file of at most
// MaxNewsBody bytes that parses. age is how old it is by now; a file dated in
// the future is no cache.
func readNewsCache(path string, now time.Time) (items []NewsItem, age time.Duration, ok bool) {
	if path == "" {
		return nil, 0, false
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxNewsBody {
		return nil, 0, false
	}
	age = now.Sub(fi.ModTime())
	if age < 0 {
		return nil, 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()
	body, err := readCapped(f, MaxNewsBody)
	if err != nil {
		return nil, 0, false
	}
	items, err = ParseFeed(body, now)
	if err != nil {
		return nil, 0, false
	}
	return items, age, true
}

// writeNewsCache stores body at path, atomically (a temp file in the same
// folder, then rename), the folder 0700 and the file 0600.
func writeNewsCache(path string, body []byte) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".arch-news-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if os.Rename(name, path) == nil {
		ok = true
	}
}

// rss is the part of the feed we read: items of the first channel. Anything
// else (descriptions, HTML) is never decoded.
type rss struct {
	Channel *struct {
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			PubDate string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

// ParseFeed parses an RSS 2.0 feed of at most MaxNewsBody bytes into at most
// MaxNewsItems items, newest first. Per item: the title is plain sanitized
// text (sanitize.Field, TitleMax runes) and an item with none is dropped; the
// link is kept only if it is an https://archlinux.org/ address; the date must
// parse as RFC 1123 (zone numeric or named) and not lie more than a day after
// now, or the item is dropped. A feed without a channel is an error; one
// whose items are all dropped is an empty list, not an error. now is the
// clock for the future-date test.
func ParseFeed(body []byte, now time.Time) ([]NewsItem, error) {
	if len(body) > MaxNewsBody {
		return nil, errors.New("feed too large")
	}
	var doc rss
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("bad feed: %w", err)
	}
	if doc.Channel == nil {
		return nil, errors.New("bad feed: no channel")
	}
	var out []NewsItem
	for _, it := range doc.Channel.Items {
		title := strings.TrimSpace(sanitize.Field(strings.TrimSpace(it.Title), TitleMax))
		date, ok := parseDate(it.PubDate)
		if title == "" || !ok || date.After(now.Add(24*time.Hour)) {
			continue
		}
		out = append(out, NewsItem{Title: title, Link: safeLink(it.Link), Date: date})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	if len(out) > MaxNewsItems {
		out = out[:MaxNewsItems]
	}
	return out, nil
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// safeLink returns raw only if it is a plain https://archlinux.org/ address:
// no user info, no port, no control characters, at most LinkMax bytes. Anything
// else gives "".
func safeLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > LinkMax {
		return ""
	}
	for _, r := range raw {
		if r < 0x21 || r > 0x7e { // printable ASCII only: a link is not text to show
			return ""
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != NewsHost || u.User != nil || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	return u.String()
}

// httpGet is the real HTTPGet: one GET, NewsTimeout, a body capped at
// MaxNewsBody, a redirect only to the same scheme and host as the request
// (and at most 3). The pattern is internal/aursearch's: its own transport,
// "archmaid" as User-Agent, no cookies.
func httpGet(ctx context.Context, rawURL string) ([]byte, error) {
	return httpGetLimited(ctx, rawURL, NewsTimeout, MaxNewsBody)
}

// httpGetLimited is httpGet with its own timeout and body cap (the mirror's
// lastsync: 5 s, 64 bytes).
func httpGetLimited(ctx context.Context, rawURL string, timeout time.Duration, maxBody int64) ([]byte, error) {
	first, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
				return errors.New("redirect off the news host refused")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "archmaid")
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news feed returned HTTP %d", resp.StatusCode)
	}
	return readCapped(resp.Body, maxBody)
}
