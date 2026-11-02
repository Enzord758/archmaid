package aursearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

// serve starts a fake RPC root; the handler sees the request.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL + "/rpc/v5"})
}

func reply(w http.ResponseWriter, typ string, results ...map[string]any) {
	json.NewEncoder(w).Encode(map[string]any{"version": 5, "type": typ, "resultcount": len(results), "results": results})
}

func nameList(ps []pkgdb.Package) string {
	var n []string
	for _, p := range ps {
		n = append(n, p.Name)
	}
	return strings.Join(n, ",")
}

func TestSearchRequestAndRanking(t *testing.T) {
	var gotPath, gotQuery, gotUA, gotMethod string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA, gotMethod = r.URL.EscapedPath(), r.URL.RawQuery, r.UserAgent(), r.Method
		reply(w, "search",
			map[string]any{"Name": "other-yay-thing", "Version": "1", "NumVotes": 900, "Popularity": 50.0},
			map[string]any{"Name": "yay-git", "Version": "1", "NumVotes": 200, "Popularity": 1.5},
			map[string]any{"Name": "yay-bin", "PackageBase": "yay-bin", "Version": "12.5-1", "Description": "Yet another yogurt", "Maintainer": "jguer",
				"NumVotes": 100, "Popularity": 12.5, "OutOfDate": 1700000000, "LastModified": 1760000000, "FirstSubmitted": 1600000000,
				"URL": "https://github.com/Jguer/yay", "URLPath": "/cgit/aur.git/snapshot/yay-bin.tar.gz",
				"License": []string{"GPL-3.0"}, "Groups": []string{"tools"}, "Depends": []string{"pacman>=6", "git"},
				"OptDepends": []string{"sudo: privilege"}, "Conflicts": []string{"yay"}, "Provides": []string{"yay=12.5"}, "Replaces": []string{"yay-old"}},
			map[string]any{"Name": "yay", "Version": "12.5-2", "NumVotes": 50, "Description": nil, "Maintainer": nil, "OutOfDate": nil},
			map[string]any{"Name": "yay", "Version": "dup", "NumVotes": 51},
		)
	})
	pkgs, err := c.Search(context.Background(), "  yay  ")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "GET" || gotPath != "/rpc/v5/search/yay" || gotQuery != "by=name-desc" || gotUA != "archmaid" {
		t.Fatalf("request = %s %s?%s UA %q", gotMethod, gotPath, gotQuery, gotUA)
	}
	if nameList(pkgs) != "yay,yay-bin,yay-git,other-yay-thing" {
		t.Fatalf("order = %s, want exact, then prefix by popularity, then the rest (and no duplicate)", nameList(pkgs))
	}
	if pkgs[0].Version != "dup" {
		t.Fatalf("yay = %+v", pkgs[0])
	}
	y := pkgs[1]
	a := y.AUR
	if y.Repo != pkgdb.RepoAUR || a == nil || y.Description != "Yet another yogurt" || a.Maintainer != "jguer" || a.Votes != 100 || a.Popularity != 12.5 ||
		a.PackageBase != "yay-bin" || !a.OutOfDate.Equal(time.Unix(1700000000, 0)) || !a.LastModified.Equal(time.Unix(1760000000, 0)) ||
		!a.FirstSubmitted.Equal(time.Unix(1600000000, 0)) || a.URLPath != "/cgit/aur.git/snapshot/yay-bin.tar.gz" ||
		y.URL != "https://github.com/Jguer/yay" || y.Licenses[0] != "GPL-3.0" || y.Groups[0] != "tools" {
		t.Fatalf("yay-bin = %+v / %+v", y, a)
	}
	if y.Depends[0].Name != "pacman" || y.Depends[0].Raw != "pacman>=6" || y.OptDepends[0].Name != "sudo" || y.OptDepends[0].Desc != "privilege" ||
		y.Conflicts[0].Name != "yay" || y.Provides[0].Name != "yay" || y.Replaces[0].Name != "yay-old" {
		t.Fatalf("yay-bin deps = %+v", y)
	}
	if a.MakeDepends != nil && len(a.MakeDepends) != 0 {
		t.Fatal("a search hit has no makedepends")
	}
	if o := pkgs[0]; o.AUR.Maintainer != "" || o.Description != "" || !o.AUR.OutOfDate.IsZero() {
		t.Fatalf("null fields = %+v, want zero values", o)
	}
}

func TestSearchEscapesTheTerm(t *testing.T) {
	var gotPath string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		reply(w, "search")
	})
	if _, err := c.Search(context.Background(), "a/b?c d#e"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rpc/v5/search/a%2Fb%3Fc%20d%23e" {
		t.Fatalf("path = %q, want the term kept inside one path segment", gotPath)
	}
}

// TestSearchTermLimits: a bad term is refused before any request is made (a
// term goes to the AUR, so nothing is sent for one letter).
func TestSearchTermLimits(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, "search") })
	for _, bad := range []string{"", " ", "a", "  é  ", "ab\x00", "a\nb", "a\x1bb", strings.Repeat("x", MaxTerm+1), "ab\xff", strings.Repeat("é", 33)} {
		if _, err := c.Search(context.Background(), bad); !errors.Is(err, ErrBadTerm) {
			t.Fatalf("%q: err = %v, want ErrBadTerm", bad, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("%d requests sent for refused terms", calls.Load())
	}
	for _, ok := range []string{"ab", "éé", strings.Repeat("x", MaxTerm)} {
		if _, err := c.Search(context.Background(), ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestSearchCapsHits(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var rs []map[string]any
		for i := 0; i < MaxHits+50; i++ {
			rs = append(rs, map[string]any{"Name": fmt.Sprintf("pkg%03d", i), "Version": "1"})
		}
		reply(w, "search", rs...)
	})
	pkgs, err := c.Search(context.Background(), "pkg")
	if err != nil || len(pkgs) != MaxHits {
		t.Fatalf("got %d, %v; want %d", len(pkgs), err, MaxHits)
	}
}

func TestTooManyResults(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"type": "error", "error": "Too many package results."})
	})
	if _, err := c.Search(context.Background(), "ab"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("err = %v, want ErrTooMany", err)
	}
}

func TestInfo(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotArgs []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotMethod, gotPath, gotCT, gotArgs = r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.PostForm["arg[]"]
		reply(w, "multiinfo",
			map[string]any{"Name": "yay", "Version": "12", "Depends": []string{"pacman>6.1", "git"}, "MakeDepends": []string{"go"}, "CheckDepends": []string{"check"}, "OptDepends": []string{"sudo: privilege"}},
			map[string]any{"Name": "unasked", "Version": "1"},
		)
	})
	pkgs, err := c.Info(context.Background(), []string{"yay", "paru", "yay", "bad name", "-rf", ""})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/rpc/v5/info" || !strings.HasPrefix(gotCT, "application/x-www-form-urlencoded") || strings.Join(gotArgs, ",") != "yay,paru" {
		t.Fatalf("request = %s %s %q args %v; want the legal, distinct names only", gotMethod, gotPath, gotCT, gotArgs)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "yay" || pkgs[0].Depends[1].Name != "git" || pkgs[0].AUR.MakeDepends[0].Name != "go" ||
		pkgs[0].AUR.CheckDepends[0].Name != "check" || pkgs[0].OptDepends[0].Desc != "privilege" {
		t.Fatalf("pkgs = %+v, want only the asked name, with its dependencies", pkgs)
	}
}

// TestInfoChunks: more than MaxInfoNames names go out in several requests.
func TestInfoChunks(t *testing.T) {
	var sizes []int
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sizes = append(sizes, len(r.PostForm["arg[]"]))
		var rs []map[string]any
		for _, n := range r.PostForm["arg[]"] {
			rs = append(rs, map[string]any{"Name": n, "Version": "1"})
		}
		reply(w, "multiinfo", rs...)
	})
	var names []string
	for i := 0; i < MaxInfoNames*2+5; i++ {
		names = append(names, fmt.Sprint("p", i))
	}
	pkgs, err := c.Info(context.Background(), names)
	if err != nil || len(pkgs) != len(names) {
		t.Fatalf("got %d, %v", len(pkgs), err)
	}
	if len(sizes) != 3 || sizes[0] != MaxInfoNames || sizes[2] != 5 {
		t.Fatalf("chunks = %v", sizes)
	}
}

func TestInfoNothingToAsk(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, "multiinfo") })
	for _, in := range [][]string{nil, {}, {"a b", "x&arg[]=y", "-rf", "", "a\x1b[2J"}} {
		if p, err := c.Info(context.Background(), in); p != nil || err != nil {
			t.Fatalf("%q: %v %v", in, p, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("%d requests sent", calls.Load())
	}
}

func TestInfoError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 503) })
	if _, err := c.Info(context.Background(), []string{"yay"}); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err = %v", err)
	}
}

// TestUntrustedFieldsAreCleaned: a hostile AUR record can't smuggle escapes,
// forged lines or unbounded text, or a name the install path would reject.
func TestUntrustedFieldsAreCleaned(t *testing.T) {
	evil := "x\x1b]52;c;ZXZpbA==\x07y\nforged‮"
	deps := make([]string, 600)
	for i := range deps {
		deps[i] = "d"
	}
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, "search",
			map[string]any{"Name": "evil", "Version": evil, "Description": evil + strings.Repeat("d", 5000), "Maintainer": evil + strings.Repeat("m", 500),
				"PackageBase": evil, "URL": evil, "URLPath": evil, "NumVotes": -5, "Popularity": -1.0, "Depends": append([]string{evil}, deps...),
				"License": []string{evil}, "Groups": deps, "LastModified": -1, "FirstSubmitted": 99999999999999},
			map[string]any{"Name": "bad name\n", "Version": "1"},
			map[string]any{"Name": "../x", "Version": "1"},
		)
	})
	pkgs, err := c.Search(context.Background(), "evil")
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("pkgs = %+v, err %v; want only the legal name", pkgs, err)
	}
	p := pkgs[0]
	for _, s := range []string{p.Version, p.Description, p.AUR.Maintainer, p.AUR.PackageBase, p.URL, p.AUR.URLPath, p.Depends[0].Raw, p.Licenses[0]} {
		if strings.ContainsAny(s, "\x1b\x07\n‮") {
			t.Fatalf("%q survived", s)
		}
	}
	if utf8.RuneCountInString(p.Description) > descMax+1 || utf8.RuneCountInString(p.AUR.Maintainer) > 65 {
		t.Fatalf("fields not capped: %d, %d", utf8.RuneCountInString(p.Description), utf8.RuneCountInString(p.AUR.Maintainer))
	}
	if len(p.Depends) != 512 || len(p.Groups) != 64 {
		t.Fatalf("lists not capped: %d, %d", len(p.Depends), len(p.Groups))
	}
	if p.AUR.Votes != 0 || p.AUR.Popularity != 0 || !p.AUR.LastModified.IsZero() || !p.AUR.FirstSubmitted.IsZero() {
		t.Fatalf("p = %+v, want negatives and absurd dates cleared", p.AUR)
	}
}

func TestErrors(t *testing.T) {
	evil := "slow down\x1b]52;c;ZXZpbA==\x07\n[HIGH] forged" + strings.Repeat("x", 1000)
	cases := []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"rpc error", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"type": "error", "error": evil})
		}, "AUR error: slow down [HIGH] forged"},
		{"http 500", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }, "HTTP 500"},
		{"http 429 with json", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); reply(w, "search") }, "HTTP 429"},
		{"not json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }, "unreadable AUR response"},
		{"wrong type", func(w http.ResponseWriter, r *http.Request) { reply(w, "multiinfo\x1b[2J") }, "unexpected AUR response type"},
		{"oversized", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"type":"search","results":[{"Name":"a","Description":"`))
			w.Write([]byte(strings.Repeat("a", MaxResponse+10)))
			w.Write([]byte(`"}]}`)) // complete JSON: only the cap makes it unreadable
		}, "unreadable AUR response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := serve(t, tc.h)
			pkgs, err := c.Search(context.Background(), "abc")
			if err == nil || pkgs != nil || !strings.Contains(err.Error(), tc.want) || strings.ContainsAny(err.Error(), "\x1b\x07\n") || len(err.Error()) > 300 {
				t.Fatalf("pkgs %v err = %q, want %q on one clean short line and no partial list", pkgs, err, tc.want)
			}
		})
	}
}

func TestConfigErrors(t *testing.T) {
	if _, err := New(Config{}).Search(context.Background(), "abc"); !errors.Is(err, ErrNoURL) {
		t.Fatalf("err = %v, an empty BaseURL must fail, not default to the real AUR", err)
	}
	if _, err := New(Config{}).Info(context.Background(), []string{"abc"}); !errors.Is(err, ErrNoURL) {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(Config{BaseURL: "http://bad host/"}).Search(context.Background(), "abc"); err == nil {
		t.Fatal("want a request error")
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:1"}).Search(context.Background(), "abc"); err == nil {
		t.Fatal("want a connection error")
	}
}

func TestCancelAndTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c := New(Config{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := c.Search(ctx, "abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel was not prompt")
	}

	c = New(Config{BaseURL: srv.URL, Timeout: 100 * time.Millisecond})
	if _, err := c.Search(context.Background(), "abc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the timeout", err)
	}
}
