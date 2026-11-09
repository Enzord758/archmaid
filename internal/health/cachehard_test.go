package health

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewsCacheHardening (#271): the cache is used only when it is a private
// file of ours in a real folder, and a symlinked cache folder is neither
// chmod'ed nor written through.
func TestNewsCacheHardening(t *testing.T) {
	body := feed(item("Fresh", "https://archlinux.org/news/f/", dNew))
	var gets atomic.Int32

	t.Run("symlinked folder is left alone", func(t *testing.T) {
		d := newsDeps(t, logHead, body, nil, &gets)
		target := t.TempDir()
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(d.CacheDir, "archmaid")); err != nil {
			t.Fatal(err)
		}
		if got := checkNews(context.Background(), d); got.State != Look {
			t.Fatalf("%+v", got)
		}
		if di, _ := os.Stat(target); di.Mode().Perm() != 0o755 {
			t.Errorf("symlink target chmod'ed to %v", di.Mode().Perm())
		}
		if ents, _ := os.ReadDir(target); len(ents) != 0 {
			t.Errorf("wrote through the symlink: %v", ents)
		}
	})

	t.Run("cache in a symlinked folder is not read", func(t *testing.T) {
		d := newsDeps(t, logHead, body, nil, &gets)
		target := t.TempDir()
		os.WriteFile(filepath.Join(target, "arch-news.xml"), body, 0o600)
		os.Chtimes(filepath.Join(target, "arch-news.xml"), t0.Add(-time.Hour), t0.Add(-time.Hour))
		os.Symlink(target, filepath.Join(d.CacheDir, "archmaid"))
		if _, _, ok := readNewsCache(filepath.Join(d.CacheDir, "archmaid", "arch-news.xml"), t0); ok {
			t.Error("read a cache through a symlinked folder")
		}
	})

	t.Run("group or world writable file is not read", func(t *testing.T) {
		d := newsDeps(t, logHead, body, nil, &gets)
		dir := filepath.Join(d.CacheDir, "archmaid")
		os.Mkdir(dir, 0o700)
		path := filepath.Join(dir, "arch-news.xml")
		os.WriteFile(path, body, 0o600)
		os.Chtimes(path, t0.Add(-time.Hour), t0.Add(-time.Hour))
		if _, _, ok := readNewsCache(path, t0); !ok {
			t.Fatal("a private cache was refused")
		}
		os.Chmod(path, 0o666)
		if _, _, ok := readNewsCache(path, t0); ok {
			t.Error("read a world-writable cache")
		}
		os.Chmod(path, 0o600)
		os.Chmod(dir, 0o777)
		if _, _, ok := readNewsCache(path, t0); ok {
			t.Error("read a cache in a world-writable folder")
		}
	})
}
