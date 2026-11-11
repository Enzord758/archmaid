package frontend

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Enzord758/archmaid/internal/aurhelper"
)

// DefaultTopgradeConfig is what "Edit topgrade config" creates when the user
// has no topgrade config yet and no AUR helper is installed (or topgrade has
// to find one itself): the TUI before it opens $EDITOR, the GUI before it
// hands the file to the desktop's default app. TopgradeConfigFor adds the line
// that names an installed helper.
//
// There is no "aur_command" here: topgrade has no such key, and its [linux]
// table rejects unknown keys, so a config with it made topgrade refuse to
// start. The AUR helper is `arch_package_manager` (topgrade's config.rs;
// allowed: autodetect, aura, garuda_update, pacman, pamac, paru, pikaur,
// shelly, trizen, yay).
const DefaultTopgradeConfig = `# topgrade configuration
# Documentation: https://github.com/topgrade-rs/topgrade/wiki/Configuration

[misc]
assume_yes = true
cleanup = true

[linux]
# The AUR helper (paru or yay) is found by topgrade itself. To pin one:
# arch_package_manager = "paru"

# Disable steps you don't want by setting them to false:
# [skip_steps]
# flatpak = false
`

// TopgradeConfigFor is DefaultTopgradeConfig naming the AUR helper h in its
// [linux] table (a helper that is installed, internal/aurhelper); with none it
// is DefaultTopgradeConfig as it is.
func TopgradeConfigFor(h aurhelper.Helper) string {
	if h.None() {
		return DefaultTopgradeConfig
	}
	return strings.Replace(DefaultTopgradeConfig,
		"# The AUR helper (paru or yay) is found by topgrade itself. To pin one:\n# arch_package_manager = \"paru\"\n",
		"# The AUR helper, found installed (topgrade finds one itself when this is left out):\narch_package_manager = \""+h.Name+"\"\n", 1)
}

// ErrNoHome: the home directory is unknown ($HOME unset or relative), so
// there is no config path to create or open.
var ErrNoHome = errors.New("no home directory: $HOME is unset or not absolute")

// TopgradeConfigPath is the user's topgrade config, ~/.config/topgrade.toml.
// The file is the user's own: nothing about it needs root.
func TopgradeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", ErrNoHome
	}
	return filepath.Join(home, ".config", "topgrade.toml"), nil
}

// EnsureTopgradeConfig creates path with TopgradeConfigFor the AUR helper that
// is installed (DefaultTopgradeConfig when none) when nothing
// is there (its directory too), and never touches an existing file, not
// even one that appears between the check and the write (O_EXCL). created
// says whether it wrote the file.
func EnsureTopgradeConfig(path string) (created bool, err error) {
	return EnsureTopgradeConfigWith(path, TopgradeConfigFor(aurhelper.Detect(aurhelper.Deps{})))
}

// EnsureTopgradeConfigWith is EnsureTopgradeConfig writing content.
func EnsureTopgradeConfigWith(path, content string) (created bool, err error) {
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return true, err
	}
	return true, f.Close()
}
