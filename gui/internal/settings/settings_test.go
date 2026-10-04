package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

func TestPath(t *testing.T) {
	for _, c := range []struct{ xdg, home, want string }{
		{"/x/cfg", "/h", "/x/cfg/archmaid/gui.json"},
		{"", "/h", "/h/.config/archmaid/gui.json"},
		{"rel/cfg", "/h", "/h/.config/archmaid/gui.json"},
		{"", "", ""},
		{"", "rel", ""},
	} {
		if got := Path(c.xdg, c.home); got != c.want {
			t.Errorf("Path(%q, %q) = %q, want %q", c.xdg, c.home, got, c.want)
		}
	}
	// testenv put XDG_CONFIG_HOME in a scratch dir; never the state dir.
	p := DefaultPath()
	if !strings.HasPrefix(p, os.Getenv("XDG_CONFIG_HOME")+"/") || strings.HasPrefix(p, os.Getenv("XDG_STATE_HOME")+"/") {
		t.Errorf("DefaultPath %q: want under XDG_CONFIG_HOME %q, never XDG_STATE_HOME", p, os.Getenv("XDG_CONFIG_HOME"))
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "archmaid", "gui.json")
	if s, err := Load(path); err != nil || s != (Settings{}) {
		t.Fatalf("Load(missing) = %+v, %v; want the defaults, nil", s, err)
	}
	want := Settings{Downloads: "/data/dl", DryRun: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("gui.json mode %v, want 0600", fi.Mode().Perm())
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v, want 0700", di.Mode().Perm())
	}
	if s, err := Load(path); err != nil || s != want {
		t.Errorf("Load = %+v, %v; want %+v", s, err, want)
	}
	// Overwrite: no temp file is left beside it.
	if err := Save(path, Settings{}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 || ents[0].Name() != "gui.json" {
		t.Errorf("folder holds %v, want only gui.json", ents)
	}
	if s, _ := Load(path); s != (Settings{}) {
		t.Errorf("after saving the defaults Load = %+v", s)
	}

	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err == nil || s != (Settings{}) {
		t.Errorf("Load(malformed) = %+v, %v; want the defaults and an error", s, err)
	}
	if err := Save("", want); err == nil {
		t.Error("Save with no path: want an error")
	}
}

// TestCheckDownloads: the GUI refuses what the TUI's --downloads refuses,
// through the same resolver.
func TestCheckDownloads(t *testing.T) {
	home := os.Getenv("HOME") // testenv's scratch home
	dl := filepath.Join(home, "Download")
	if err := os.MkdirAll(dl, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	o := ResolverOptions("")
	for _, c := range []struct {
		in, want, err string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{dl, dl, ""},
		{" " + dl + " ", dl, ""},
		{outside, outside, ""},
		{home, "", "home"},
		{home + "/", "", "home"},
		{filepath.Dir(home), "", ""}, // an ancestor of $HOME: some error
		{"/", "", ""},
		{filepath.Join(home, "missing"), "", ""},
		{"Download", "", "full path"},
	} {
		got, err := CheckDownloads(c.in, o)
		refused := c.want == "" && strings.TrimSpace(c.in) != ""
		switch {
		case refused && err == nil:
			t.Errorf("CheckDownloads(%q) = %q, want it refused", c.in, got)
		case !refused && err != nil:
			t.Errorf("CheckDownloads(%q): %v", c.in, err)
		case !refused && got != c.want:
			t.Errorf("CheckDownloads(%q) = %q, want %q", c.in, got, c.want)
		case err != nil && strings.Contains(err.Error(), "--downloads"):
			t.Errorf("CheckDownloads(%q): %q still names the flag", c.in, err)
		case err != nil && c.err != "" && !strings.Contains(err.Error(), c.err):
			t.Errorf("CheckDownloads(%q): %q, want it to mention %q", c.in, err, c.err)
		}
	}
}

func TestApply(t *testing.T) {
	base := engine.Options{Auth: engine.AuthPolkit}
	o := Settings{Downloads: "/data/dl", DryRun: true}.Apply(base)
	if !o.DryRun || o.Auth != engine.AuthPolkit {
		t.Errorf("Apply: DryRun %v Auth %v", o.DryRun, o.Auth)
	}
	d := o.Env.Downloads()
	if d.Flag != "/data/dl" || !d.DryRun || d.Home != os.Getenv("HOME") {
		t.Errorf("downloads options %+v", d)
	}

	// A base resolver is kept; only the Flag and DryRun change.
	base.Env.Downloads = func() downloads.Options { return downloads.Options{Home: "/h", Flag: "/old"} }
	d = Settings{}.Apply(base).Env.Downloads()
	if d.Flag != "" || d.Home != "/h" || d.DryRun {
		t.Errorf("over a base resolver: %+v", d)
	}
}
