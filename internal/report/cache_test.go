package report

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// paccacheDryFixture is what `paccache -dk0 --nocolor` prints, as
// pacman-contrib 1.13.1's script does (summarize(), size_to_human()): a
// blank line, then the msg line.
const paccacheDryFixture = "\n==> finished dry run: 312 candidates (disk space saved: 4.2 GiB)\n"

func TestParsePaccacheDry(t *testing.T) {
	for _, c := range []struct {
		name  string
		out   string
		pkgs  int
		bytes uint64
		ok    bool
	}{
		{"real output", paccacheDryFixture, 312, 4509715661, true},
		{"one candidate, in bytes", "==> finished dry run: 1 candidate (disk space saved: 812 B)\n", 1, 812, true},
		{"coloured", "\x1b[1;34m==>\x1b[0m\x1b[1m finished dry run: 3 candidates (disk space saved: 14.18 MiB)\x1b[0m\n", 3, 14868808, true},
		{"comma locale", "==> finished dry run: 2 candidates (disk space saved: 1,5 KiB)\n", 2, 1536, true},
		{"empty cache", "==> no candidate packages found for pruning\n", 0, 0, true},
		{"something else", "paccache: command not found\n", 0, 0, false},
		{"a real run's line is not a dry run", "==> finished: 3 packages removed (disk space saved: 1 MiB)\n", 0, 0, false},
		{"empty", "", 0, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, b, ok := ParsePaccacheDry(c.out)
			if n != c.pkgs || b != c.bytes || ok != c.ok {
				t.Errorf("= %d, %d, %v; want %d, %d, %v", n, b, ok, c.pkgs, c.bytes, c.ok)
			}
		})
	}
}

func TestCacheDirs(t *testing.T) {
	q := func(out string, err error) Querier {
		return func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "pacman-conf" || !reflect.DeepEqual(args, []string{"CacheDir"}) {
				t.Errorf("asked %s %v", name, args)
			}
			return []byte(out), err
		}
	}
	ctx := context.Background()
	if got := CacheDirs(ctx, q("/var/cache/pacman/pkg/\n/mnt/cache/\n", nil)); !reflect.DeepEqual(got, []string{"/var/cache/pacman/pkg", "/mnt/cache"}) {
		t.Errorf("dirs %v", got)
	}
	for name, qq := range map[string]Querier{
		"fails":      q("", errors.New("not found")),
		"relative":   q("cache/\n", nil),
		"names none": q("\n", nil),
	} {
		if got := CacheDirs(ctx, qq); !reflect.DeepEqual(got, []string{DefaultCacheDir}) {
			t.Errorf("pacman-conf %s: dirs %v, want the default", name, got)
		}
	}
}

// holdCollector is a Collector whose cache folder is dir and whose commands
// answer from answers (name -> output, error).
func holdCollector(t *testing.T, dir string, answers map[string]func() ([]byte, error)) *Collector {
	t.Helper()
	cfg := Config{
		PacmanLog: filepath.Join(t.TempDir(), "none"),
		Query: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "pacman-conf" {
				return []byte(dir + "\n"), nil
			}
			if f := answers[name]; f != nil {
				return f()
			}
			return nil, errors.New("not found: " + name)
		},
		Now: time.Now,
	}
	return New(cfg, "full-cleanup", time.Now())
}

// TestCacheStillHoldsPaccache: the dry run's totals are used, and it is
// read-only (-d) with keep 0.
func TestCacheStillHoldsPaccache(t *testing.T) {
	var argv []string
	c := holdCollector(t, t.TempDir(), map[string]func() ([]byte, error){
		"paccache": func() ([]byte, error) { return []byte(paccacheDryFixture), nil },
	})
	q := c.cfg.Query
	c.cfg.Query = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "paccache" {
			argv = args
		}
		return q(ctx, name, args...)
	}
	h, ok := c.cacheStillHolds(context.Background())
	if !ok || h.Source != "paccache" || h.Packages != 312 || h.Bytes != 4509715661 {
		t.Errorf("hold %+v ok %v", h, ok)
	}
	if strings.Join(argv, " ") != "-dk0 --nocolor" {
		t.Errorf("paccache ran with %v, want -dk0 --nocolor", argv)
	}
}

// TestCacheStillHoldsFallback: with paccache missing, failing or printing
// something unexpected, the cache folder is summed; with that unreadable
// too, the line is left out.
func TestCacheStillHoldsFallback(t *testing.T) {
	dir := t.TempDir()
	writeN(t, filepath.Join(dir, "a-1-1-x86_64.pkg.tar.zst"), 8192)
	writeN(t, filepath.Join(dir, "b-1-1-x86_64.pkg.tar.zst"), 8192)
	for name, ans := range map[string]func() ([]byte, error){
		"missing":  nil,
		"fails":    func() ([]byte, error) { return nil, errors.New("exit 1") },
		"unparsed": func() ([]byte, error) { return []byte("hello\n"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			answers := map[string]func() ([]byte, error){}
			if ans != nil {
				answers["paccache"] = ans
			}
			h, ok := holdCollector(t, dir, answers).cacheStillHolds(context.Background())
			if !ok || h.Source != "folder" || h.Packages != 2 || h.Bytes < 16384 {
				t.Errorf("hold %+v ok %v, want the folder's two packages", h, ok)
			}
		})
	}
	// A cache folder that doesn't exist is a known empty cache; one that
	// can't be told from a file is unknown, and the line is left out.
	if h, ok := holdCollector(t, filepath.Join(dir, "gone"), nil).cacheStillHolds(context.Background()); !ok || h.Bytes != 0 {
		t.Errorf("a missing cache folder gave %+v, ok %v", h, ok)
	}
	if _, ok := holdCollector(t, filepath.Join(dir, "a-1-1-x86_64.pkg.tar.zst", "x"), nil).cacheStillHolds(context.Background()); ok {
		t.Error("an unreadable cache folder gave a line")
	}
}
