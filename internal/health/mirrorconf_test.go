package health

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// pacman uses the first server of the repo section in pacman.conf's order, not
// the mirrorlist's first (#283). The owner's layout: a Server line directly in
// [core], before its Include of the mirrorlist. `pacman-conf --repo core
// Server` prints the servers in pacman's order, $repo and $arch expanded.

func confQuery(out string, err error, calls *[][]string) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if calls != nil {
			*calls = append(*calls, append([]string{name}, args...))
		}
		if name != "pacman-conf" {
			return nil, errors.New("unexpected program " + name)
		}
		return []byte(out), err
	}
}

func TestMirrorFollowsPacmanConf(t *testing.T) {
	now := t0
	// The mirrorlist's first server is a different, stale one.
	list := mirrorList(t, "Server = https://mirror.example.com/archlinux/$repo/os/$arch\n")
	ts := func(ago time.Duration) string { return itoa(now.Add(-ago).Unix()) }
	for _, c := range []struct {
		name    string
		query   func(*[][]string) func(context.Context, string, ...string) ([]byte, error)
		list    string
		fetched string // the URL asked for ("" = none)
		state   State
		detail  string
	}{
		{"the owner's layout: a Server in [core] before the Include picks the LAN mirror",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("http://192.168.1.189:8099/archlinux/core/os/x86_64\nhttps://mirror.example.com/archlinux/core/os/x86_64\n", nil, c)
			}, list, "http://192.168.1.189:8099/archlinux/lastsync", OK, "192.168.1.189:8099 synced 20 min ago"},
		{"Include only: pacman-conf's first is the mirrorlist's first",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("https://mirror.example.com/archlinux/core/os/x86_64\nhttps://other.example/archlinux/core/os/x86_64\n", nil, c)
			}, list, "https://mirror.example.com/archlinux/lastsync", OK, "mirror.example.com synced 20 min ago"},
		{"pacman-conf missing: the mirrorlist's order",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("", errors.New(`exec: "pacman-conf": executable file not found`), c)
			}, list, "https://mirror.example.com/archlinux/lastsync", OK, "mirror.example.com synced 20 min ago"},
		{"pacman-conf fails with output: the mirrorlist's order",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("http://192.168.1.189:8099/archlinux/core/os/x86_64\n", errors.New("exit status 1"), c)
			}, list, "https://mirror.example.com/archlinux/lastsync", OK, "mirror.example.com synced 20 min ago"},
		{"no servers in [core]: the mirrorlist's order",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("\n  \n", nil, c)
			}, list, "https://mirror.example.com/archlinux/lastsync", OK, "mirror.example.com synced 20 min ago"},
		{"garbage output: unknown, not the mirrorlist",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("<html>not a server</html>\n", nil, c)
			}, list, "", Unknown, "can't read the mirror setting"},
		{"a server without /core/os/: unknown",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("https://mirror.example.com/archlinux/x86_64\n", nil, c)
			}, list, "", Unknown, "can't read the mirror setting"},
		{"hostile schemes and user info are refused",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("ftp://m.example/archlinux/core/os/x86_64\n", nil, c)
			}, list, "", Unknown, "can't read the mirror setting"},
		{"user info is refused",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("https://user:pw@m.example/archlinux/core/os/x86_64\n", nil, c)
			}, list, "", Unknown, "can't read the mirror setting"},
		{"a huge line is refused",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("https://m.example/"+strings.Repeat("a", 100000)+"/core/os/x86_64\n", nil, c)
			}, list, "", Unknown, "can't read the mirror setting"},
		{"pacman-conf answers, there is no mirrorlist at all",
			func(c *[][]string) func(context.Context, string, ...string) ([]byte, error) {
				return confQuery("http://192.168.1.189:8099/archlinux/core/os/x86_64\n", nil, c)
			}, "", "http://192.168.1.189:8099/archlinux/lastsync", OK, "192.168.1.189:8099 synced 20 min ago"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls [][]string
			var urls []string
			d := Deps{Now: func() time.Time { return now }, Query: c.query(&calls), MirrorList: c.list,
				MirrorGet: func(_ context.Context, u string) ([]byte, error) {
					urls = append(urls, u)
					return []byte(ts(20 * time.Minute)), nil
				}}
			got := checkMirror(context.Background(), d)
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
			if want := [][]string{{"pacman-conf", "--repo", "core", "Server"}}; !reflect.DeepEqual(calls, want) {
				t.Errorf("queried %v, want only the read-only %v", calls, want)
			}
			var wantURLs []string
			if c.fetched != "" {
				wantURLs = []string{c.fetched}
			}
			if !reflect.DeepEqual(urls, wantURLs) {
				t.Errorf("fetched %v, want %v: only <base>/lastsync", urls, wantURLs)
			}
		})
	}
	// No Query seam at all (a bare Deps): the mirrorlist, never a real pacman-conf.
	d := Deps{Now: func() time.Time { return now }, MirrorList: list,
		MirrorGet: func(context.Context, string) ([]byte, error) { return []byte(ts(time.Hour)), nil }}
	if got := checkMirror(context.Background(), d); got.State != OK || got.Detail != "mirror.example.com synced 1 h ago" {
		t.Errorf("no Query: %+v", got)
	}
}

func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
