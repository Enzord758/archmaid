// Package aurbuild is the user-side half of an AUR install for the Packages
// page (docs/design/packages-contract.md, section 5): it works out what an
// install needs (Resolve), fetches each package's git repo into a cache
// folder (Fetcher), turns the repo into the text the sheet shows (Review),
// builds with makepkg once the user has accepted that text (BuildSteps) and
// describes the final root install (LocalHash, the pkg-install-local step).
//
// Everything runs as the user. Nothing here, and nothing it starts before
// the sheet is accepted, sources or executes a PKGBUILD: git runs with no
// configuration and no hooks, .SRCINFO is read as text, and makepkg starts
// only from a step the user's "Install" released. It is plain Go (no gotk4)
// and the root helper never imports it.
//
// Git, makepkg: found on PATH (they are the user's tools, not privileged
// ones); tests put shims first on PATH.
package aurbuild

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// DefaultAURURL is the AUR git host; a package base's repo is
// <DefaultAURURL>/<base>.git.
const DefaultAURURL = "https://aur.archlinux.org"

const (
	// NetTimeout bounds a clone or fetch.
	NetTimeout = 120 * time.Second
	// LocalTimeout bounds every other git command.
	LocalTimeout = 20 * time.Second
	// Depth is how much history a clone and a fetch keep, for the diff.
	Depth = "50"
)

// reserved are the folders archmaid itself keeps inside a base's folder:
// PKGDEST, SRCDEST and BUILDDIR of the build. A repo that has files there
// is refused.
var reserved = []string{"out", "src", "build"}

// ErrSymlink: a symlink in the cache path. Nothing is fetched or built.
var ErrSymlink = errors.New("a symlink in the AUR cache path")

// DefaultRoot is $XDG_CACHE_HOME/archmaid/aur (os.UserCacheDir).
func DefaultRoot() (string, error) {
	c, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "archmaid", "aur"), nil
}

// Fetcher fetches package repos into Root/<base>.
type Fetcher struct {
	// Root is the cache root (DefaultRoot), absolute.
	Root string
	// URL returns the repo URL of a base; nil means DefaultAURURL. Tests
	// point it at a path their git shim understands.
	URL func(base string) string
	// Allow is the one protocol git may use ("https"; a test's shim says
	// otherwise); "" means https.
	Allow string
}

// Checkout is a fetched, pinned package repo.
type Checkout struct {
	Base   string
	Dir    string // Root/<base>
	Commit string // the pinned HEAD (40 or 64 hex)
}

// proxyEnv are the variables git (curl) needs to reach the network through
// a proxy and a private CA; everything else of archmaid's environment is
// left behind.
var proxyEnv = []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}

// env is git's whole environment: PATH, HOME, a C locale, no user or system
// configuration, no prompt, and the proxy variables. Nothing of archmaid's
// own, in particular no GIT_DIR, GIT_SSH_COMMAND, GIT_EXEC_PATH or hook
// path an inherited environment might carry.
func (f Fetcher) env() []string {
	e := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_OPTIONAL_LOCKS=0",
	}
	for _, k := range proxyEnv {
		if v, ok := os.LookupEnv(k); ok {
			e = append(e, k+"="+v)
		}
	}
	return e
}

func (f Fetcher) url(base string) string {
	if f.URL != nil {
		return f.URL(base)
	}
	return DefaultAURURL + "/" + base + ".git"
}

// hardening are the -c options of every git call: no hooks, no filters run
// by the repo, and only the allowed transport.
func (f Fetcher) hardening() []string {
	allow := f.Allow
	if allow == "" {
		allow = "https"
	}
	return []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "-c", "protocol." + allow + ".allow=always",
		"-c", "core.fsmonitor=false", "-c", "core.sshCommand=/bin/false"}
}

// run starts one git command in dir under its own timeout. The error is
// one sanitized line.
func (f Fetcher) run(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := proc.QueryIn(ctx, dir, f.env(), "git", append(f.hardening(), args...)...)
	if err != nil {
		if ctx.Err() != nil {
			err = errors.New("timed out or cancelled")
		}
		return "", fmt.Errorf("git %s: %s", args[0], sanitize.Clip(sanitize.OneLine(err.Error()), 200))
	}
	return string(out), nil
}

// checkPath refuses a symlink at Root or anywhere below it on the way to
// path (inclusive), and a non-directory where a directory should be.
func (f Fetcher) checkPath(path string) error {
	rel, err := filepath.Rel(f.Root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%s is outside the AUR cache", path)
	}
	cur := f.Root
	parts := []string{""}
	if rel != "." {
		parts = append(parts, strings.Split(rel, string(filepath.Separator))...)
	}
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return err
		case fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%w: %s", ErrSymlink, cur)
		case !fi.IsDir():
			return fmt.Errorf("%s is not a folder", cur)
		}
	}
	return nil
}

// Dir is the folder of base inside the cache, after the base name check.
func (f Fetcher) Dir(base string) (string, error) {
	if !pkgdb.ValidName(base) || strings.ContainsAny(base, "/") {
		return "", fmt.Errorf("%q is not a package base name", sanitize.Clip(sanitize.OneLine(base), 40))
	}
	if !filepath.IsAbs(f.Root) {
		return "", errors.New("the AUR cache root is not absolute")
	}
	return filepath.Join(f.Root, base), nil
}

// Fetch clones base (or fetches into an existing checkout) and pins the
// checkout to what the AUR's HEAD is now. The commit is what Review shows
// and the build must use. Nothing is executed from the repo.
func (f Fetcher) Fetch(ctx context.Context, base string) (Checkout, error) {
	dir, err := f.Dir(base)
	if err != nil {
		return Checkout{}, err
	}
	if err := os.MkdirAll(f.Root, 0o700); err != nil {
		return Checkout{}, err
	}
	if err := f.checkPath(dir); err != nil {
		return Checkout{}, err
	}
	url := f.url(base)
	var commit string
	if _, lerr := os.Lstat(filepath.Join(dir, ".git")); lerr == nil {
		if err := f.checkPath(filepath.Join(dir, ".git")); err != nil {
			return Checkout{}, err
		}
		if _, err := f.run(ctx, NetTimeout, dir, "fetch", "--depth", Depth, "--no-recurse-submodules", "--force", url, "+HEAD:refs/archmaid/head"); err != nil {
			return Checkout{}, err
		}
		commit, err = f.run(ctx, LocalTimeout, dir, "rev-parse", "--verify", "refs/archmaid/head^{commit}")
	} else {
		if entries, _ := os.ReadDir(dir); len(entries) > 0 {
			return Checkout{}, fmt.Errorf("%s exists and is not a git checkout; remove it", dir)
		}
		if _, err := f.run(ctx, NetTimeout, f.Root, "clone", "--depth", Depth, "--no-recurse-submodules", "--", url, dir); err != nil {
			os.RemoveAll(dir) // only ever our own half-made clone: it was empty or absent
			return Checkout{}, err
		}
		commit, err = f.run(ctx, LocalTimeout, dir, "rev-parse", "--verify", "HEAD^{commit}")
	}
	if err != nil {
		return Checkout{}, fmt.Errorf("%s has no commits in the AUR: %w", base, err)
	}
	co := Checkout{Base: base, Dir: dir, Commit: strings.TrimSpace(commit)}
	if !validCommit(co.Commit) {
		return Checkout{}, errors.New("git gave an unusable commit id")
	}
	return co, f.Pin(ctx, co)
}

func validCommit(c string) bool {
	if len(c) != 40 && len(c) != 64 {
		return false
	}
	for i := 0; i < len(c); i++ {
		if !strings.ContainsRune("0123456789abcdef", rune(c[i])) {
			return false
		}
	}
	return true
}

// Pin checks the working tree out at co.Commit, detached and forced, removes
// everything untracked except the build folders, and refuses a tree that is
// not then exactly that commit (or a repo that tracks a reserved folder).
// The build step calls it again, so what makepkg sees is what was reviewed.
func (f Fetcher) Pin(ctx context.Context, co Checkout) error {
	if !validCommit(co.Commit) {
		return errors.New("not a commit id")
	}
	if err := f.checkPath(co.Dir); err != nil {
		return err
	}
	if _, err := f.run(ctx, LocalTimeout, co.Dir, "checkout", "--detach", "--force", "--quiet", co.Commit); err != nil {
		return err
	}
	clean := []string{"clean", "-ffdxq"}
	for _, r := range reserved {
		clean = append(clean, "-e", "/"+r)
	}
	if _, err := f.run(ctx, LocalTimeout, co.Dir, clean...); err != nil {
		return err
	}
	head, err := f.run(ctx, LocalTimeout, co.Dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != co.Commit {
		return fmt.Errorf("the checkout is at %s, not the reviewed %s", strings.TrimSpace(head), co.Commit)
	}
	tracked, err := f.run(ctx, LocalTimeout, co.Dir, append([]string{"ls-files", "-z", "--"}, reserved...)...)
	if err != nil {
		return err
	}
	if tracked != "" {
		return fmt.Errorf("the repo of %s has files in %s, which archmaid keeps for the build", co.Base, strings.Join(reserved, ", "))
	}
	status, err := f.run(ctx, LocalTimeout, co.Dir, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("the checkout has local changes")
	}
	return nil
}

// Diff is the unified diff from prev to commit in the checkout, capped at
// max bytes; note says why there is none when diff is "".
func (f Fetcher) Diff(ctx context.Context, co Checkout, prev string, max int) (diff, note string, truncated bool) {
	switch {
	case prev == "":
		return "", "first review: there is no earlier version to compare with", false
	case prev == co.Commit:
		return "", "no change since the version you last reviewed", false
	case !validCommit(prev):
		return "", "the remembered commit is not usable", false
	}
	if _, err := f.run(ctx, LocalTimeout, co.Dir, "cat-file", "-e", prev+"^{commit}"); err != nil {
		return "", "the version you last reviewed is no longer in the fetched history", false
	}
	out, err := f.run(ctx, LocalTimeout, co.Dir, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", prev, co.Commit, "--")
	if err != nil {
		return "", "the diff could not be made: " + err.Error(), false
	}
	if out == "" {
		return "", "no change since the version you last reviewed", false
	}
	if len(out) > max {
		out, truncated = cutUTF8(out, max), true
	}
	return out, "", truncated
}

// cutUTF8 shortens s to at most n bytes without splitting a rune.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
