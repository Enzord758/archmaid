package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func item(title, link, date string) string {
	return fmt.Sprintf("<item><title>%s</title><link>%s</link><pubDate>%s</pubDate><description>&lt;b&gt;x&lt;/b&gt;</description></item>", title, link, date)
}

func feed(items ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>Arch Linux: Recent news updates</title><link>https://archlinux.org/news/</link>` +
		strings.Join(items, "") + `</channel></rss>`)
}

const (
	dNew = "Sat, 07 Nov 2026 08:19:29 +0000"
	dMid = "Mon, 26 Oct 2026 10:00:00 +0000"
	dOld = "Fri, 02 Oct 2026 08:19:29 +0000"
)

func TestParseFeed(t *testing.T) {
	items, err := ParseFeed(feed(
		item("Old", "https://archlinux.org/news/old/", dOld),
		item("New", "https://archlinux.org/news/new/", dNew),
		item("Mid", "https://archlinux.org/news/mid/", dMid),
	), t0)
	if err != nil || len(items) != 3 {
		t.Fatalf("%v %v", items, err)
	}
	if items[0].Title != "New" || items[1].Title != "Mid" || items[2].Title != "Old" {
		t.Errorf("not newest first: %+v", items)
	}
	if items[0].Link != "https://archlinux.org/news/new/" || !items[0].Date.Equal(time.Date(2026, 11, 7, 8, 19, 29, 0, time.UTC)) {
		t.Errorf("%+v", items[0])
	}
}

func TestParseFeedHostile(t *testing.T) {
	huge := strings.Repeat("A", 100000)
	for _, c := range []struct {
		name  string
		body  []byte
		check func(t *testing.T, it []NewsItem, err error)
	}{
		{"missing channel", []byte(`<rss version="2.0"></rss>`), func(t *testing.T, it []NewsItem, err error) {
			if err == nil {
				t.Error("no error")
			}
		}},
		{"not xml", []byte("<html><body>Captive portal login"), func(t *testing.T, it []NewsItem, err error) {
			if err == nil {
				t.Error("no error")
			}
		}},
		{"empty", nil, func(t *testing.T, it []NewsItem, err error) {
			if err == nil {
				t.Error("no error")
			}
		}},
		{"atom instead of rss", []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>x</title></entry></feed>`), func(t *testing.T, it []NewsItem, err error) {
			if err == nil {
				t.Error("no error")
			}
		}},
		{"entity bomb is not expanded", []byte(`<?xml version="1.0"?><!DOCTYPE r [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;">]><rss><channel>` +
			item("&b;&b;", "", dNew) + `</channel></rss>`), func(t *testing.T, it []NewsItem, err error) {
			if err == nil && len(it) > 0 && len(it[0].Title) > 50 {
				t.Errorf("entity expanded: %d bytes", len(it[0].Title))
			}
		}},
		{"huge title is cut", feed(item(huge, "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != 1 || len([]rune(it[0].Title)) > TitleMax+1 {
				t.Errorf("%d items, err %v", len(it), err)
			}
		}},
		{"control characters and escapes in a title", feed(item("Evil&#x1b;[2J&#x7;&#x0d;spoof&#x202e;txt\u0085", "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil {
				// XML 1.0 forbids &#x1b; and the decoder may refuse it; also safe.
				return
			}
			if len(it) == 1 && strings.ContainsFunc(it[0].Title, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x202e }) {
				t.Errorf("control character kept: %q", it[0].Title)
			}
		}},
		{"raw control characters in a title", feed(item("a\x1b[31mred\x07b\r\nc\td", "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil {
				return
			}
			if len(it) == 1 && strings.ContainsAny(it[0].Title, "\x1b\x07\r\n\t") {
				t.Errorf("kept: %q", it[0].Title)
			}
		}},
		{"html in a title is plain text", feed(item("&lt;script&gt;alert(1)&lt;/script&gt; <![CDATA[<b>bold</b>]]>", "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != 1 || it[0].Title != "<script>alert(1)</script> <b>bold</b>" {
				t.Errorf("%+v %v", it, err)
			}
		}},
		{"empty title dropped", feed(item("  ", "", dNew), item("Kept", "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != 1 || it[0].Title != "Kept" {
				t.Errorf("%+v %v", it, err)
			}
		}},
		{"wrong dates drop the item", feed(item("A", "", "yesterday"), item("B", "", ""), item("C", "", "2026-11-07"),
			item("D", "", "Sat, 07 Nov 2099 08:19:29 +0000"), item("E", "", dNew)), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != 1 || it[0].Title != "E" {
				t.Errorf("%+v %v", it, err)
			}
		}},
		{"links", feed(
			item("ok", "https://archlinux.org/news/x/", dNew),
			item("js", "javascript:alert(1)", dNew),
			item("http", "http://archlinux.org/news/x/", dNew),
			item("data", "data:text/html,hi", dNew),
			item("file", "file:///etc/passwd", dNew),
			item("host", "https://archlinux.org.evil.example/news/", dNew),
			item("sub", "https://evil.archlinux.org/", dNew),
			item("userinfo", "https://archlinux.org@evil.example/", dNew),
			item("userinfo2", "https://user:pw@archlinux.org/", dNew),
			item("port", "https://archlinux.org:8443/x", dNew),
			item("relative", "/news/x/", dNew),
			item("nospace", "https://archlinux.org/a b", dNew),
			item("ctl", "https://archlinux.org/a&#x7f;b", dNew),
			item("unicode", "https://archlinux.org/ä", dNew),
			item("long", "https://archlinux.org/"+strings.Repeat("a", 400), dNew),
			item("upper", "HTTPS://archlinux.org/x", dNew),
		), func(t *testing.T, it []NewsItem, err error) {
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, x := range it {
				got[x.Title] = x.Link
			}
			if len(got) != 16 {
				t.Errorf("titles must be kept when the link is dropped: %d items", len(got))
			}
			for title, link := range got {
				want := ""
				if title == "ok" {
					want = "https://archlinux.org/news/x/"
				}
				if title == "upper" {
					want = got["upper"] // url.Parse lowercases the scheme; either "" or a clean https link is safe
					if want != "" && !strings.HasPrefix(want, "https://archlinux.org/") {
						t.Errorf("upper = %q", want)
					}
					continue
				}
				if link != want {
					t.Errorf("%s: link %q, want %q", title, link, want)
				}
			}
		}},
		{"too many items are capped", func() []byte {
			var s []string
			for i := 0; i < 100; i++ {
				s = append(s, item(fmt.Sprintf("n%d", i), "", dNew))
			}
			return feed(s...)
		}(), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != MaxNewsItems {
				t.Errorf("%d items, %v", len(it), err)
			}
		}},
		{"channel without items", feed(), func(t *testing.T, it []NewsItem, err error) {
			if err != nil || len(it) != 0 {
				t.Errorf("%+v %v", it, err)
			}
		}},
		{"over the cap", append(feed(), make([]byte, MaxNewsBody)...), func(t *testing.T, it []NewsItem, err error) {
			if err == nil {
				t.Error("no error")
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			it, err := ParseFeed(c.body, t0)
			c.check(t, it, err)
		})
	}
}

// newsDeps wires a fake getter and a cache dir. gets counts requests.
func newsDeps(t *testing.T, log string, body []byte, getErr error, gets *atomic.Int32) Deps {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "pacman.log")
	if log != "" {
		os.WriteFile(logPath, []byte(log), 0o644)
	}
	return Deps{
		Now:       func() time.Time { return t0 },
		PacmanLog: logPath,
		CacheDir:  t.TempDir(),
		NewsURL:   "https://archlinux.org/feeds/news/",
		HTTPGet: func(ctx context.Context, url string) ([]byte, error) {
			if url != "https://archlinux.org/feeds/news/" {
				t.Errorf("fetched %q", url)
			}
			gets.Add(1)
			return body, getErr
		},
	}
}

// logHead's last full upgrade is 2026-10-20T08:00:40+0200 = 06:00:40Z.
func TestNewsCheck(t *testing.T) {
	two := feed(item("Manual intervention for foo", "https://archlinux.org/news/foo/", dNew), item("Bar", "", dMid), item("Old", "", dOld))
	for _, c := range []struct {
		name   string
		log    string
		body   []byte
		err    error
		state  State
		detail string
		link   string
	}{
		{"two newer than the update", logHead, two, nil, Look, "2 new since your last update, newest: Manual intervention for foo", "https://archlinux.org/news/foo/"},
		{"one newer", logHead, feed(item("Only", "https://archlinux.org/news/o/", dNew), item("Old", "", dOld)), nil, Look, "1 new since your last update: Only", "https://archlinux.org/news/o/"},
		{"all older", logHead, feed(item("Old", "", dOld)), nil, OK, "Nothing new since your last update", ""},
		{"no feed items", logHead, feed(), nil, OK, "Nothing new since your last update", ""},
		{"no reference date", "", two, nil, Unknown, "No full update found in the pacman log", ""},
		{"offline, no cache", logHead, nil, errors.New("dial tcp: no route"), Unknown, "Can't reach archlinux.org", ""},
		{"timeout", logHead, nil, context.DeadlineExceeded, Unknown, "Arch news timed out", ""},
		{"not a feed", logHead, []byte("<html>portal</html>"), nil, Unknown, "Can't read the Arch news feed", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var gets atomic.Int32
			d := newsDeps(t, c.log, c.body, c.err, &gets)
			got := checkNews(context.Background(), d)
			if got.State != c.state || got.Detail != c.detail || got.Link != c.link {
				t.Errorf("= %v %q %q\nwant %v %q %q", got.State, got.Detail, got.Link, c.state, c.detail, c.link)
			}
			if gets.Load() != 1 {
				t.Errorf("%d requests, want 1", gets.Load())
			}
		})
	}
	if got := checkNews(context.Background(), Deps{Now: func() time.Time { return t0 }}); got.State != Unknown {
		t.Errorf("no getter: %v", got.State)
	}
}

func TestNewsCache(t *testing.T) {
	body := feed(item("Fresh", "https://archlinux.org/news/f/", dNew))
	var gets atomic.Int32
	d := newsDeps(t, logHead, body, nil, &gets)
	path := filepath.Join(d.CacheDir, "archmaid", "arch-news.xml")

	// First call fetches and writes the cache: file 0600, folder 0700.
	if got := checkNews(context.Background(), d); got.State != Look {
		t.Fatalf("%+v", got)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %v %v", fi, err)
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("cache dir mode %v", di.Mode().Perm())
	}
	if ents, _ := os.ReadDir(filepath.Dir(path)); len(ents) != 1 {
		t.Errorf("temp files left behind: %v", ents)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Error("cache is not the raw feed")
	}

	// Within 3 hours: no request.
	os.Chtimes(path, t0.Add(-2*time.Hour), t0.Add(-2*time.Hour))
	if c := checkNews(context.Background(), d); c.State != Look || gets.Load() != 1 {
		t.Errorf("fresh cache: %+v, %d requests", c, gets.Load())
	}
	// Older than 3 hours: one more.
	os.Chtimes(path, t0.Add(-3*time.Hour-time.Minute), t0.Add(-3*time.Hour-time.Minute))
	if c := checkNews(context.Background(), d); c.State != Look || gets.Load() != 2 {
		t.Errorf("expired cache: %+v, %d requests", c, gets.Load())
	}
	// Refetch rewrote it (mtime is real time now, which is later than t0-3h).

	// Offline with a stale cache: the cache is used, its age shown.
	d.HTTPGet = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	os.Chtimes(path, t0.Add(-5*time.Hour), t0.Add(-5*time.Hour))
	c := checkNews(context.Background(), d)
	if c.State != Look || !strings.HasSuffix(c.Detail, "(offline, cached 5 h ago)") {
		t.Errorf("stale cache: %+v", c)
	}
	// Stale cache with nothing new: neutral, never OK, never Look.
	os.WriteFile(path, feed(item("Old", "", dOld)), 0o600)
	os.Chtimes(path, t0.Add(-5*time.Hour), t0.Add(-5*time.Hour))
	c = checkNews(context.Background(), d)
	if c.State != Unknown || !strings.Contains(c.Detail, "5 h ago") {
		t.Errorf("stale, nothing new: %+v", c)
	}
	// A cache that does not parse is no cache.
	os.WriteFile(path, []byte("garbage"), 0o600)
	os.Chtimes(path, t0.Add(-5*time.Hour), t0.Add(-5*time.Hour))
	if c := checkNews(context.Background(), d); c.State != Unknown || c.Detail != "Can't reach archlinux.org" {
		t.Errorf("bad cache: %+v", c)
	}
	// A cache dated in the future is no cache either (the clock moved).
	os.WriteFile(path, body, 0o600)
	os.Chtimes(path, t0.Add(time.Hour), t0.Add(time.Hour))
	if c := checkNews(context.Background(), d); c.State != Unknown {
		t.Errorf("future cache: %+v", c)
	}
	// A symlink is not read.
	os.Remove(path)
	target := filepath.Join(t.TempDir(), "other.xml")
	os.WriteFile(target, body, 0o600)
	os.Symlink(target, path)
	if c := checkNews(context.Background(), d); c.State != Unknown {
		t.Errorf("symlinked cache was read: %+v", c)
	}
	// An oversized cache is not read.
	os.Remove(path)
	os.WriteFile(path, append(append([]byte{}, body...), make([]byte, MaxNewsBody)...), 0o600)
	os.Chtimes(path, t0, t0)
	if c := checkNews(context.Background(), d); c.State != Unknown {
		t.Errorf("oversized cache was read: %+v", c)
	}
}

// A feed that does not parse is not cached over a good one.
func TestNewsBadFeedKeepsCache(t *testing.T) {
	var gets atomic.Int32
	d := newsDeps(t, logHead, []byte("<html>portal</html>"), nil, &gets)
	path := filepath.Join(d.CacheDir, "archmaid", "arch-news.xml")
	os.MkdirAll(filepath.Dir(path), 0o700)
	good := feed(item("Good", "", dNew))
	os.WriteFile(path, good, 0o600)
	os.Chtimes(path, t0.Add(-4*time.Hour), t0.Add(-4*time.Hour))
	c := checkNews(context.Background(), d)
	if c.State != Look || !strings.Contains(c.Detail, "cached 4 h ago") {
		t.Errorf("%+v", c)
	}
	if b, _ := os.ReadFile(path); string(b) != string(good) {
		t.Error("a bad feed overwrote the cache")
	}
}

func TestUnreadNews(t *testing.T) {
	var gets atomic.Int32
	body := feed(item("A", "https://archlinux.org/news/a/", dNew), item("B", "", dMid), item("C", "", dOld), item("D", "", "Mon, 05 Oct 2026 10:00:00 +0000"))
	d := newsDeps(t, logHead, body, nil, &gets)
	res, err := UnreadNews(context.Background(), d)
	if err != nil || len(res.Items) != 2 || res.Items[0].Title != "A" || res.Items[1].Title != "B" || res.FromCache || res.Since.IsZero() {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Items[0].Link != "https://archlinux.org/news/a/" {
		t.Errorf("link %q", res.Items[0].Link)
	}
	// No reference date: no items, no error.
	d2 := newsDeps(t, "", body, nil, &gets)
	res, err = UnreadNews(context.Background(), d2)
	if err != nil || len(res.Items) != 0 || !res.Since.IsZero() {
		t.Fatalf("%+v %v", res, err)
	}
	// Offline and nothing cached: an error, so the sheet shows no block.
	d3 := newsDeps(t, logHead, nil, errors.New("offline"), &gets)
	if _, err := UnreadNews(context.Background(), d3); err == nil {
		t.Error("no error offline")
	}
	// The pacman.log of the real machine is never read in tests: a path that
	// does not exist is the same as no reference date.
	d4 := newsDeps(t, "", body, nil, &gets)
	d4.PacmanLog = filepath.Join(t.TempDir(), "none")
	if res, err := UnreadNews(context.Background(), d4); err != nil || !res.Since.IsZero() {
		t.Errorf("%+v %v", res, err)
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{10 * time.Second: "under a minute", 5 * time.Minute: "5 min", 5 * time.Hour: "5 h", 47 * time.Hour: "47 h", 72 * time.Hour: "3 d"} {
		if got := Ago(d); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestHTTPGet(t *testing.T) {
	var ua atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a redirect left the host: %s", r.URL)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("hello"))
		case "/big":
			w.Write(make([]byte, MaxNewsBody+1))
		case "/exact":
			w.Write(make([]byte, MaxNewsBody))
		case "/404":
			http.NotFound(w, r)
		case "/off":
			http.Redirect(w, r, other.URL+"/x", http.StatusFound)
		case "/same":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/slow":
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if b, err := httpGet(ctx, srv.URL+"/ok"); err != nil || string(b) != "hello" {
		t.Errorf("ok: %q %v", b, err)
	}
	if ua.Load() != "archmaid" {
		t.Errorf("User-Agent %v", ua.Load())
	}
	if b, err := httpGet(ctx, srv.URL+"/exact"); err != nil || len(b) != MaxNewsBody {
		t.Errorf("exact: %d %v", len(b), err)
	}
	if _, err := httpGet(ctx, srv.URL+"/big"); err == nil {
		t.Error("a body over the cap was accepted")
	}
	if _, err := httpGet(ctx, srv.URL+"/404"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
	if _, err := httpGet(ctx, srv.URL+"/off"); err == nil {
		t.Error("an off-host redirect was followed")
	}
	if b, err := httpGet(ctx, srv.URL+"/same"); err != nil || string(b) != "hello" {
		t.Errorf("same-host redirect: %q %v", b, err)
	}
	if _, err := httpGet(ctx, srv.URL+"/loop"); err == nil {
		t.Error("a redirect loop ended")
	}
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := httpGet(cctx, srv.URL+"/slow"); err == nil {
		t.Error("a hung server did not time out")
	}
	if _, err := httpGet(ctx, "://bad"); err == nil {
		t.Error("bad url")
	}
}

// The cache is written atomically: a failure to write leaves no temp file and
// does not fail the check.
func TestNewsCacheWriteFailure(t *testing.T) {
	var gets atomic.Int32
	d := newsDeps(t, logHead, feed(item("A", "", dNew)), nil, &gets)
	file := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(file, nil, 0o600)
	d.CacheDir = file // MkdirAll under a regular file fails
	if c := checkNews(context.Background(), d); c.State != Look {
		t.Errorf("%+v", c)
	}
}
