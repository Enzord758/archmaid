package downloads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A mount table that can't be read is a notice from Scan and a refusal from
// Delete (nothing is deleted without knowing where the filesystems end).
func TestMountTableUnreadable(t *testing.T) {
	home, o := isolate(t)
	dl := filepath.Join(home, "Downloads")
	writeFile(t, filepath.Join(dl, "a.iso"), "x")
	o.Now = at(ctimeMark(t))
	s := discoverDownloadStep(t, o)

	orig := mountinfoPath
	mountinfoPath = filepath.Join(t.TempDir(), "no-such-mountinfo")
	t.Cleanup(func() { mountinfoPath = orig })

	if _, err := Scan(s.plan.Dir, o.now(), o); err == nil || !strings.Contains(err.Error(), "Couldn't read the mount table") {
		t.Errorf("Scan with no mount table: %v", err)
	}
	res, err := Delete(context.Background(), s.plan, o)
	if err == nil || !strings.Contains(res.Output, "nothing deleted") {
		t.Errorf("Delete with no mount table: %v, %q", err, res.Output)
	}
	if !exists(filepath.Join(dl, "a.iso")) {
		t.Error("a.iso was deleted without a mount table")
	}
}

// user-dirs.dirs is read for XDG_DOWNLOAD_DIR only when it names an absolute
// folder: no home to expand $HOME against, a relative value or no file is "".
func TestXDGDownloadDirEdges(t *testing.T) {
	cfg := t.TempDir()
	write := func(line string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := xdgDownloadDir("", ""); got != "" {
		t.Errorf("no config dir, no home: %q", got)
	}
	if got := xdgDownloadDir("relative/cfg", ""); got != "" {
		t.Errorf("relative config dir, no home: %q", got)
	}
	if got := xdgDownloadDir(cfg, "/home/x"); got != "" {
		t.Errorf("no user-dirs.dirs: %q", got)
	}
	write(`XDG_DOWNLOAD_DIR="$HOME/Downloads"`)
	if got := xdgDownloadDir(cfg, ""); got != "" {
		t.Errorf("$HOME with no home: %q", got)
	}
	if got := xdgDownloadDir(cfg, "/home/x"); got != "/home/x/Downloads" {
		t.Errorf("$HOME expanded: %q", got)
	}
	write(`XDG_DOWNLOAD_DIR="Downloads"`)
	if got := xdgDownloadDir(cfg, "/home/x"); got != "" {
		t.Errorf("a relative folder: %q", got)
	}
	// Without a config dir it looks in <home>/.config.
	if err := os.MkdirAll(filepath.Join(cfg, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, ".config", "user-dirs.dirs"), []byte(`XDG_DOWNLOAD_DIR=/srv/dl`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := xdgDownloadDir("", cfg); got != "/srv/dl" {
		t.Errorf("config dir from the home: %q", got)
	}
}

// The clock is the real one unless a test moves it; a home that can't be
// resolved (it doesn't exist) is still guarded as its cleaned path.
func TestOptionsNowAndUnresolvableHome(t *testing.T) {
	before := time.Now()
	if got := (Options{}).now(); got.Before(before) || time.Since(got) > time.Minute {
		t.Errorf("now() = %v, not the current time", got)
	}
	fixed := time.Unix(1700000000, 0)
	if got := (Options{Now: func() time.Time { return fixed }}).now(); !got.Equal(fixed) {
		t.Errorf("now() = %v with a clock set", got)
	}
	h := homeDirs(Options{Home: "/no-such-home-290/../no-such-home-290/user"})
	if h.home == "" || !strings.HasSuffix(h.real, "/no-such-home-290/user") {
		t.Errorf("an unresolvable home: %+v", h)
	}
}
