// Package aurhelper decides which AUR helper archmaid works with: paru or yay,
// as topgrade does. archmaid runs the helper for the AUR cache step of Full
// Cleanup, reads its build folder for the run report, and lets it hold the
// kept password for sudo (internal/privilege, keptCallers); the updates check
// script (packaging/systemd/archmaid-updates-check) makes the same choice in
// shell. Pure Go, standard library only, no execution: every outside source is
// a Deps seam.
//
// The choice (Detect), in this order:
//
//  1. ARCHMAID_AUR_HELPER (paru, yay or none): the override the test and e2e
//     harnesses set so they never meet a helper the machine happens to have.
//  2. The user's topgrade config: [linux] arch_package_manager = "paru" or
//     "yay", when that program is installed. (topgrade's key; it has no
//     "aur_command", which its parser rejects.) Any other value, a missing key
//     or file, or a helper that is not installed, falls through.
//  3. The first installed of paru, yay: topgrade's own autodetect order
//     (src/steps/os/archlinux.rs, get_arch_package_manager: paru before yay).
//  4. None: Helper.Name is "" and the AUR cache step says so and does nothing.
//
// The cleaning argv is the same for both helpers (CleanArgv). What differs is
// where their build folder is (BuildDir) and, in the updates script, that paru
// has no age tags to split an AUR update list by the cooldown.
package aurhelper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The supported helpers.
const (
	Paru = "paru"
	Yay  = "yay"
)

// Supported are the helpers in topgrade's autodetect order: the first
// installed one wins when the topgrade config doesn't choose.
var Supported = []string{Paru, Yay}

// EnvOverride names the variable that forces the choice: "paru", "yay" or
// "none". For the e2e harness and the tests (like ARCHMAID_PACMAN_LOG); it
// changes nothing about what is run.
const EnvOverride = "ARCHMAID_AUR_HELPER"

// Helper is the chosen AUR helper. The zero value is "none installed".
type Helper struct {
	// Name is Paru, Yay or "".
	Name string
	// Path is where LookPath found it ("" for an override or none). The step's
	// argv names the program by Name, as the other steps do.
	Path string
}

// None reports whether no helper was found.
func (h Helper) None() bool { return h.Name == "" }

// Label is the name to show a user: the helper's, or "AUR helper".
func (h Helper) Label() string {
	if h.Name == "" {
		return "AUR helper"
	}
	return h.Name
}

// Deps are the outside sources of Detect and BuildDir; zero fields mean the
// real thing.
type Deps struct {
	LookPath func(file string) (string, error)
	ReadFile func(path string) ([]byte, error)
	Getenv   func(key string) string
	// Home returns the home folder; nil means os.UserHomeDir.
	Home func() (string, error)
}

func (d Deps) lookPath(f string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath(f)
	}
	return exec.LookPath(f)
}

func (d Deps) readFile(p string) ([]byte, error) {
	if d.ReadFile != nil {
		return d.ReadFile(p)
	}
	return readCapped(p)
}

func (d Deps) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

func (d Deps) home() string {
	h, err := "", error(nil)
	if d.Home != nil {
		h, err = d.Home()
	} else {
		h, err = os.UserHomeDir()
	}
	if err != nil || !filepath.IsAbs(h) {
		return ""
	}
	return h
}

// maxConfig caps what is read of any config file.
const maxConfig = 1 << 20

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxConfig))
}

// ConfigDir is the folder user configs live in: $XDG_CONFIG_HOME when it is
// absolute, else ~/.config ("" when there is no home).
func (d Deps) ConfigDir() string {
	if c := d.getenv("XDG_CONFIG_HOME"); filepath.IsAbs(c) {
		return c
	}
	if h := d.home(); h != "" {
		return filepath.Join(h, ".config")
	}
	return ""
}

// CacheDir is $XDG_CACHE_HOME when absolute, else ~/.cache.
func (d Deps) CacheDir() string {
	if c := d.getenv("XDG_CACHE_HOME"); filepath.IsAbs(c) {
		return c
	}
	if h := d.home(); h != "" {
		return filepath.Join(h, ".cache")
	}
	return ""
}

// TopgradeConfigPaths are the places topgrade looks for its config, in its
// order (src/config.rs, ConfigFile::ensure): topgrade.toml, then
// topgrade/topgrade.toml, in the config folder.
func (d Deps) TopgradeConfigPaths() []string {
	c := d.ConfigDir()
	if c == "" {
		return nil
	}
	return []string{filepath.Join(c, "topgrade.toml"), filepath.Join(c, "topgrade/topgrade.toml")}
}

// ConfiguredManager is the value of [linux] arch_package_manager in the user's
// topgrade config ("" when there is none). topgrade reads the first of
// TopgradeConfigPaths that exists, so does this.
func ConfiguredManager(d Deps) string {
	for _, p := range d.TopgradeConfigPaths() {
		b, err := d.readFile(p)
		if err != nil {
			continue
		}
		return ParseArchPackageManager(b)
	}
	return ""
}

// ParseArchPackageManager reads arch_package_manager out of the [linux] table
// of a topgrade TOML file: a quoted value on a plain `key = "value"` line, a
// trailing comment allowed. It is not a TOML parser; anything it doesn't
// recognise (dotted keys, inline tables, a value that isn't a simple string)
// reads as "no value", which only means falling back to autodetection.
func ParseArchPackageManager(b []byte) string {
	inLinux := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			name := line
			if i := strings.Index(name, "#"); i >= 0 {
				name = strings.TrimSpace(name[:i])
			}
			inLinux = name == "[linux]"
			continue
		}
		if !inLinux {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "arch_package_manager" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) < 2 || (v[0] != '"' && v[0] != '\'') {
			return ""
		}
		end := strings.IndexByte(v[1:], v[0])
		if end < 0 {
			return ""
		}
		return v[1 : 1+end]
	}
	return ""
}

// Detect decides the helper (see the package comment).
func Detect(d Deps) Helper {
	switch o := d.getenv(EnvOverride); o {
	case Paru, Yay:
		return Helper{Name: o}
	case "none":
		return Helper{}
	}
	if want := ConfiguredManager(d); want == Paru || want == Yay {
		if p, err := d.lookPath(want); err == nil {
			return Helper{Name: want, Path: p}
		}
	}
	for _, name := range Supported {
		if p, err := d.lookPath(name); err == nil {
			return Helper{Name: name, Path: p}
		}
	}
	return Helper{}
}

// CleanArgv is the AUR cache step's command line: `<helper> -Sc [--aur]
// --noconfirm` and colour. With aurOnly (the GUI, whose root half is the root
// helper's) it cleans only the helper's own cache, as the user, with no sudo;
// without it the helper also runs `sudo pacman -Sc` itself (the TUI, whose
// sudo asks through archmaid's popup).
//
// Both helpers take the same flags: yay 13 (clean.go, parser.go: --aur sets the
// AUR mode, in which -Sc runs no pacman) and paru (src/command_line.rs: --aur
// sets Mode::AUR; src/clean.rs: in AUR mode -Sc runs no pacman, and cleans the
// clone folder, with --noconfirm taking the default of each question).
func (h Helper) CleanArgv(aurOnly bool, colour []string) []string {
	if h.Name == "" {
		return nil
	}
	a := []string{h.Name, "-Sc"}
	if aurOnly {
		a = append(a, "--aur")
	}
	a = append(a, "--noconfirm")
	return append(a, colour...)
}

// BuildDir is the folder the helper builds AUR packages in, which its -Sc
// cleans: what the run report measures. "" when it can't be told.
func BuildDir(d Deps, name string) string {
	switch name {
	case Yay:
		return yayBuildDir(d)
	case Paru:
		return paruCloneDir(d)
	}
	return ""
}

// expand turns a configured path into an absolute one: environment variables
// and a leading ~/ expanded, "" if it is still not absolute.
func (d Deps) expand(p string) string {
	p = os.Expand(p, d.getenv)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h := d.home(); h != "" {
			p = filepath.Join(h, rest)
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return ""
}

// yayBuildDir is the buildDir of yay's config.json when it names one, else
// yay's default, <cache>/yay.
func yayBuildDir(d Deps) string {
	if c := d.ConfigDir(); c != "" {
		if b, err := d.readFile(filepath.Join(c, "yay", "config.json")); err == nil {
			var cfg struct {
				BuildDir string `json:"buildDir"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.BuildDir != "" {
				if p := d.expand(cfg.BuildDir); p != "" {
					return p
				}
			}
		}
	}
	if c := d.CacheDir(); c != "" {
		return filepath.Join(c, "yay")
	}
	return ""
}

// paruCloneDir is paru's clone folder (where it builds, and what -Sc cleans):
// $AURDEST when set (src/config.rs: it overrides --clonedir, so it wins), else
// CloneDir (or BuildDir) in the [options] section of paru.conf ($PARU_CONF, else
// <config>/paru/paru.conf), else the default <cache>/paru/clone.
func paruCloneDir(d Deps) string {
	if a := d.getenv("AURDEST"); a != "" {
		if p := d.expand(a); p != "" {
			return p
		}
	}
	conf := d.getenv("PARU_CONF")
	if conf == "" {
		if c := d.ConfigDir(); c != "" {
			conf = filepath.Join(c, "paru", "paru.conf")
		}
	}
	if conf != "" {
		if b, err := d.readFile(conf); err == nil {
			if v := parseParuClone(b); v != "" {
				if p := d.expand(v); p != "" {
					return p
				}
			}
		}
	}
	if c := d.CacheDir(); c != "" {
		return filepath.Join(c, "paru", "clone")
	}
	return ""
}

// parseParuClone reads CloneDir (or its older name BuildDir) from the
// [options] section of a paru.conf.
func parseParuClone(b []byte) string {
	inOptions := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			inOptions = strings.EqualFold(line, "[options]")
			continue
		}
		if !inOptions {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k == "CloneDir" || k == "BuildDir" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
