package frontend

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultTopgradeConfig is what "Edit topgrade config" creates when the user
// has no topgrade config yet: the TUI before it opens $EDITOR, the GUI
// before it hands the file to the desktop's default app.
const DefaultTopgradeConfig = `# topgrade configuration
# Documentation: https://github.com/topgrade-rs/topgrade/wiki/Configuration

[misc]
assume_yes = true
cleanup = true

[linux]
aur_command = "yay"

# Disable steps you don't want by setting them to false:
# [skip_steps]
# flatpak = false
`

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

// EnsureTopgradeConfig creates path with DefaultTopgradeConfig when nothing
// is there (its directory too), and never touches an existing file, not
// even one that appears between the check and the write (O_EXCL). created
// says whether it wrote the file.
func EnsureTopgradeConfig(path string) (created bool, err error) {
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
	if _, err := f.WriteString(DefaultTopgradeConfig); err != nil {
		f.Close()
		return true, err
	}
	return true, f.Close()
}
