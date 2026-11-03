// Package settings is the GUI's Settings page without GTK: what the owner
// can set (the downloads folder, dry run), where it is kept and how it
// reaches a run.
//
// It is kept in $XDG_CONFIG_HOME/archmaid/gui.json (~/.config when the
// variable is unset or relative), written atomically with mode 0600. It is
// configuration, so never under the state dir: a real run prunes and
// rotates that one.
//
// Settings apply to the next Start: a Session is cheap, so the window
// builds a fresh engine.Options (Apply) and a new Driver for the first run
// after they changed. A run already going keeps what it started with.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
)

// Settings is what the Settings page sets; the zero value is the default
// (discover the downloads folder, a real run).
type Settings struct {
	// Downloads is the folder "Delete old downloads" cleans, as the TUI's
	// --downloads takes it; "" discovers it (downloads.Resolve's order).
	Downloads string `json:"downloads,omitempty"`
	// DryRun reports what would run and changes nothing (--dryrun).
	DryRun bool `json:"dry_run,omitempty"`
	// Glass turns on the glass look (the "Clear" skin: a translucent
	// window over the compositor's blur). Off is the default look.
	Glass bool `json:"glass,omitempty"`
	// Blur says whether the compositor blurs behind the window: BlurAuto
	// (or ""), BlurOn, BlurOff. Only read when Glass is on. Anything else
	// in the file counts as auto.
	Blur string `json:"blur,omitempty"`
}

// The values of Settings.Blur.
const (
	BlurAuto = "auto"
	BlurOn   = "on"
	BlurOff  = "off"
)

// HyprlandEnv is set by Hyprland in every session it starts; it is the one
// compositor archmaid assumes blurs behind a translucent window.
const HyprlandEnv = "HYPRLAND_INSTANCE_SIGNATURE"

// BlurMode is s.Blur as one of the three values ("" and anything unknown
// are BlurAuto).
func (s Settings) BlurMode() string {
	switch s.Blur {
	case BlurOn, BlurOff:
		return s.Blur
	}
	return BlurAuto
}

// BlurBehind says whether the glass look may be translucent: On, or Auto
// with HYPRLAND_INSTANCE_SIGNATURE set (getenv reads it). GTK can't ask a
// compositor whether it blurs, and asking Hyprland means running hyprctl,
// so everything else gets the near-opaque fallback skin; Off forces that.
func (s Settings) BlurBehind(getenv func(string) string) bool {
	switch s.BlurMode() {
	case BlurOn:
		return true
	case BlurOff:
		return false
	}
	return getenv(HyprlandEnv) != ""
}

// Path is where the settings live: $XDG_CONFIG_HOME/archmaid/gui.json, or
// $HOME/.config/archmaid/gui.json when XDG_CONFIG_HOME is unset or not
// absolute (the XDG spec's rule). "" when neither is usable.
func Path(xdgConfigHome, home string) string {
	dir := xdgConfigHome
	if !filepath.IsAbs(dir) {
		if !filepath.IsAbs(home) {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "archmaid", "gui.json")
}

// DefaultPath is Path from the process's environment.
func DefaultPath() string { return Path(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("HOME")) }

// Load reads the settings at path. A missing file is the defaults, not an
// error; an unreadable or malformed one returns the defaults and the error,
// so the page can say so and still work.
func Load(path string) (Settings, error) {
	if path == "" {
		return Settings{}, errors.New("no config folder: neither XDG_CONFIG_HOME nor HOME is set")
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Save writes s to path atomically (a temp file in the same folder, synced,
// renamed over it) with mode 0600, creating the folder 0700 if needed.
func Save(path string, s Settings) error {
	if path == "" {
		return errors.New("no config folder: neither XDG_CONFIG_HOME nor HOME is set")
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".gui.json.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ResolverOptions is the downloads resolver's input from the process's
// environment, as the TUI gathers it, with the Flag set to folder.
func ResolverOptions(folder string) downloads.Options {
	return downloads.Options{
		Flag:          folder,
		Env:           os.Getenv(downloads.EnvVar),
		Home:          os.Getenv("HOME"),
		PasswdHome:    downloads.PasswdHome(),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	}
}

// CheckDownloads vets a downloads folder the way the TUI's --downloads is
// vetted: downloads.Resolve with the Flag set, so a home folder, an
// ancestor of one, / and a missing folder are refused. "" (discover) is
// always fine. It returns the folder to keep (trimmed) or the reason, worded
// for the field it is shown under. A relative path is refused: there is no
// working folder a user could mean.
func CheckDownloads(folder string, o downloads.Options) (string, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", nil
	}
	if !filepath.IsAbs(folder) {
		return "", errors.New("use a full path, starting with /")
	}
	o.Flag = folder
	if _, err := downloads.Resolve(o); err != nil {
		// Resolve words it for the command line ("--downloads X: why");
		// the field already shows X.
		if inner := errors.Unwrap(err); inner != nil {
			return "", inner
		}
		return "", err
	}
	return folder, nil
}

// Apply returns base with s applied: DryRun, and the downloads resolver's
// Flag (base.Env.Downloads, or the process's environment when base has
// none).
func (s Settings) Apply(base engine.Options) engine.Options {
	o := base
	o.DryRun = s.DryRun
	prev := base.Env.Downloads
	folder, dry := s.Downloads, s.DryRun
	o.Env.Downloads = func() downloads.Options {
		var d downloads.Options
		if prev != nil {
			d = prev()
		} else {
			d = ResolverOptions("")
		}
		d.Flag, d.DryRun = folder, dry
		return d
	}
	return o
}
