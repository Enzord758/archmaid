package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── which folder is "Downloads" ──────────────────────────────────────────────
//
// "Delete old downloads" removes things wholesale, so the folder it works in
// must never be a guess that can land on $HOME. Real setups make that easy to
// get wrong: this machine's folder is ~/Download (singular), ~/Downloads does
// not exist, and user-dirs.dirs says XDG_DOWNLOAD_DIR="$HOME/" — a naive
// "read the XDG dir" would clean the whole home folder.

// downloadsFlag is set by --downloads <dir>. It overrides discovery and may
// point outside $HOME (a data disk), but never at $HOME itself, an ancestor
// of it, or /.
var downloadsFlag string

// downloadsEnv names the environment override, the second candidate.
const downloadsEnv = "ARCHMAID_DOWNLOADS"

// errNoDownloads is the notice when no candidate qualifies.
var errNoDownloads = errors.New("No downloads folder found (set --downloads or ARCHMAID_DOWNLOADS).")

// resolveDownloadsDir picks the downloads folder, symlinks resolved. In order:
// --downloads, $ARCHMAID_DOWNLOADS, XDG_DOWNLOAD_DIR from user-dirs.dirs,
// ~/Downloads, ~/Download. Each must be an existing directory; every one
// except the flag must also sit strictly inside $HOME. A candidate that fails
// is skipped — except the flag: the user named that folder, so a bad one is
// reported instead of silently cleaning a different folder.
func resolveDownloadsDir() (string, error) {
	home, _ := os.UserHomeDir()
	realHome := home
	if r, err := filepath.EvalSymlinks(home); err == nil {
		realHome = r
	}

	if downloadsFlag != "" {
		p, err := filepath.Abs(downloadsFlag)
		if err == nil {
			p, err = checkDownloadsDir(p, realHome, false)
		}
		if err != nil {
			return "", fmt.Errorf("--downloads %s: %w", downloadsFlag, err)
		}
		return p, nil
	}

	candidates := []string{os.Getenv(downloadsEnv), xdgDownloadDir(home)}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, "Downloads"), filepath.Join(home, "Download"))
	}
	for _, c := range candidates {
		if c == "" || !filepath.IsAbs(c) {
			continue
		}
		if p, err := checkDownloadsDir(c, realHome, true); err == nil {
			return p, nil
		}
	}
	return "", errNoDownloads
}

// checkDownloadsDir resolves p and vets it: it must be an existing directory,
// and never /, $HOME, or anything containing $HOME. insideHome additionally
// requires it to be strictly inside $HOME. realHome is $HOME with symlinks
// resolved, so a symlink to $HOME (or an XDG dir of "$HOME/") is caught.
func checkDownloadsDir(p, realHome string, insideHome bool) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", errors.New("no such folder")
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", errors.New("not a folder")
	}
	homeOK := realHome != "" && filepath.IsAbs(realHome)
	switch {
	case real == string(filepath.Separator):
		return "", errors.New("refusing the root folder")
	case homeOK && (real == realHome || strictlyInside(realHome, real)):
		return "", errors.New("refusing a folder that contains your home folder")
	case insideHome && (!homeOK || !strictlyInside(real, realHome)):
		return "", errors.New("not inside your home folder")
	}
	return real, nil
}

// strictlyInside reports whether path lies below dir (not dir itself). Both
// must be clean absolute paths.
func strictlyInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// xdgDownloadDir reads XDG_DOWNLOAD_DIR from $XDG_CONFIG_HOME/user-dirs.dirs
// (shell syntax: XDG_DOWNLOAD_DIR="$HOME/Downloads"), expanding $HOME and
// stripping the quotes. "" if it's unset or not an absolute path.
func xdgDownloadDir(home string) string {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" || !filepath.IsAbs(cfg) {
		if home == "" {
			return ""
		}
		cfg = filepath.Join(home, ".config")
	}
	data, err := os.ReadFile(filepath.Join(cfg, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	val := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "XDG_DOWNLOAD_DIR" {
			val = strings.TrimSpace(v) // the last assignment wins, as in sh
		}
	}
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	for _, h := range []string{"$HOME", "${HOME}"} {
		if val == h || strings.HasPrefix(val, h+"/") {
			if home == "" {
				return ""
			}
			val = home + val[len(h):]
			break
		}
	}
	if !filepath.IsAbs(val) {
		return ""
	}
	return filepath.Clean(val)
}

// displayPath shows a resolved path the way the user thinks of it: ~/… when
// it's inside $HOME, sanitized since it comes from the environment.
func displayPath(p string) string {
	home, _ := os.UserHomeDir()
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	if home != "" && strictlyInside(p, home) {
		rel, _ := filepath.Rel(home, p)
		p = "~/" + rel
	}
	return Sanitize(p, SanitizePlain)
}
