package aurbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The review's caps. Everything shown is cut to these, so a hostile repo
// cannot make the sheet huge.
const (
	MaxFileText  = 128 << 10 // one file's text in the sheet
	MaxTotalText = 768 << 10 // all files' text together
	MaxDiff      = 256 << 10
	MaxFiles     = 200
	MaxFlags     = 40
)

// Review reads the pinned checkout of base b into the sheet's payload: the
// files (text of the small text ones, sanitized), the sources and hints from
// .SRCINFO, and the diff against prev (the last reviewed commit, "" for a first
// review). It never sources the PKGBUILD. A .SRCINFO that lists a dependency
// the AUR index did not is a *Refusal (res.Verify).
func (f Fetcher) Review(ctx context.Context, co Checkout, b Base, res Resolution, prev string) (steps.AURPlan, error) {
	if err := f.checkPath(co.Dir); err != nil {
		return steps.AURPlan{}, err
	}
	raw, err := readSmallFile(filepath.Join(co.Dir, ".SRCINFO"), maxSrcinfo)
	if err != nil {
		return steps.AURPlan{}, fmt.Errorf("%s has no usable .SRCINFO: %w", b.Name, err)
	}
	si, err := ParseSrcinfo(string(raw))
	if err != nil {
		return steps.AURPlan{}, err
	}
	if err := res.Verify(b, si); err != nil {
		return steps.AURPlan{}, err
	}
	files, truncated, err := readFiles(co.Dir)
	if err != nil {
		return steps.AURPlan{}, err
	}
	p := steps.AURPlan{
		Base: b.Name, Names: append([]string(nil), b.Names...), Version: sanitize.Field(si.Version, 64),
		Commit: co.Commit, Prev: prev, Files: files, Truncated: truncated, AURDeps: append([]string(nil), b.After...),
	}
	for _, n := range b.Names {
		for _, d := range res.RepoDeps {
			if res.seen[n][d] && !contains(p.RepoDeps, d) {
				p.RepoDeps = append(p.RepoDeps, d)
			}
		}
	}
	for _, s := range si.Sources {
		p.Sources = append(p.Sources, steps.AURSource{URL: sanitize.Field(s.URL, 300), Checksum: s.Checksum})
	}
	p.Flags = Flags(si, files)
	var cut bool
	p.Diff, p.DiffNote, cut = f.Diff(ctx, co, prev, MaxDiff)
	p.Diff = visible(p.Diff)
	p.Truncated = p.Truncated || cut
	return p, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// visible makes file or diff text safe to show as plain text. A carriage
// return is shown as a mark instead of being applied (sanitize would let
// text after it overwrite the line, which could hide a command).
func visible(s string) string {
	return sanitize.String(strings.ReplaceAll(s, "\r", "␍"), sanitize.Plain)
}

// readSmallFile reads a regular file (no symlink) of at most max bytes.
func readSmallFile(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("over %d bytes", max)
	}
	return io.ReadAll(io.LimitReader(f, max))
}

func kindOf(name string) string {
	base := filepath.Base(name)
	switch {
	case name == "PKGBUILD":
		return steps.AURFilePKGBUILD
	case strings.HasSuffix(base, ".install"):
		return steps.AURFileInstall
	case strings.HasSuffix(base, ".patch"), strings.HasSuffix(base, ".diff"):
		return steps.AURFilePatch
	}
	return steps.AURFileOther
}

// readFiles lists the checkout's files (not .git, and not archmaid's own
// build folders at the top), PKGBUILD and .install first.
func readFiles(dir string) (files []steps.AURFile, truncated bool, err error) {
	var rels []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if rel == ".git" || contains(reserved, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(rels) >= MaxFiles {
			truncated = true
			return filepath.SkipAll
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Slice(rels, func(i, j int) bool {
		ki, kj := kindRank(kindOf(rels[i])), kindRank(kindOf(rels[j]))
		if ki != kj {
			return ki < kj
		}
		return rels[i] < rels[j]
	})
	budget := MaxTotalText
	for _, rel := range rels {
		af := steps.AURFile{Path: sanitize.Field(filepath.ToSlash(rel), 200), Kind: kindOf(rel)}
		full := filepath.Join(dir, rel)
		fi, err := os.Lstat(full)
		switch {
		case err != nil:
			af.Note = "unreadable"
		case fi.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(full)
			af.Note = "symlink to " + sanitize.Field(target, 120)
		case !fi.Mode().IsRegular():
			af.Note = "not a regular file"
		default:
			af.Size = fi.Size()
			fillFile(full, &af, &budget, &truncated)
		}
		files = append(files, af)
	}
	return files, truncated, nil
}

func kindRank(k string) int {
	switch k {
	case steps.AURFilePKGBUILD:
		return 0
	case steps.AURFileInstall:
		return 1
	case steps.AURFilePatch:
		return 2
	}
	return 3
}

// fillFile hashes the file and, if it is small text within the budget, sets
// its sanitized text.
func fillFile(path string, af *steps.AURFile, budget *int, truncated *bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		af.Note = "unreadable"
		return
	}
	defer f.Close()
	h := sha256.New()
	var head bytes.Buffer
	lim := &capWriter{w: &head, left: MaxFileText + 1}
	if _, err := io.Copy(io.MultiWriter(h, lim), f); err != nil {
		af.Note = "unreadable"
		return
	}
	af.Sha256 = hex.EncodeToString(h.Sum(nil))
	switch {
	case af.Size > MaxFileText:
		af.Note = "too large to show"
		*truncated = true
	case !utf8.Valid(head.Bytes()) || bytes.IndexByte(head.Bytes(), 0) >= 0:
		af.Note = "binary"
	case head.Len() > *budget:
		af.Note = "not shown: the review's text limit is used up"
		*truncated = true
	default:
		*budget -= head.Len()
		af.Text = visible(head.String())
	}
}

var (
	rePipeShell = regexp.MustCompile(`(?i)\b(curl|wget|fetch)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b`)
	reSudo      = regexp.MustCompile(`(?m)(^|[;&|(\s])sudo\s`)
	reBase64    = regexp.MustCompile(`base64\s+(-d|--decode)|openssl\s+enc\s+-d`)
	reIPHost    = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$|^\[.*\]$`)
)

// commonHosts are source hosts that need no remark.
var commonHosts = []string{"github.com", "gitlab.com", "codeberg.org", "sourceforge.net", "bitbucket.org", "pypi.org", "pythonhosted.org",
	"crates.io", "npmjs.org", "rubygems.org", "kernel.org", "gnu.org", "archlinux.org", "githubusercontent.com", "freedesktop.org", "gitlab.freedesktop.org",
	"gnome.org", "kde.org", "apache.org", "mozilla.org", "qt.io"}

// Flags are the review's hints, from .SRCINFO and the file list; each begins
// with what it is, "hint:" ones being guesses from the text. They are shown
// to help the reader look in the right place; an empty list is not a clean
// bill of health and a long one is not proof of anything.
func Flags(si Srcinfo, files []steps.AURFile) []string {
	var out []string
	add := func(format string, a ...any) {
		if len(out) < MaxFlags {
			out = append(out, sanitize.Clip(sanitize.OneLine(fmt.Sprintf(format, a...)), 240))
		}
	}
	projectHost := hostOf(si.URL)
	for _, s := range si.Sources {
		u := s.URL
		if i := strings.Index(u, "::"); i >= 0 {
			u = u[i+2:]
		}
		vcs := false
		for _, p := range []string{"git+", "svn+", "hg+", "bzr+"} {
			if strings.HasPrefix(u, p) {
				vcs = true
			}
		}
		if vcs && !strings.Contains(u, "#commit=") {
			add("source %s is a version-control source: it builds the upstream's moving head, not what you reviewed", u)
		}
		if !vcs && !s.Checksum && strings.Contains(u, "://") {
			add("source %s has no checksum (SKIP)", u)
		}
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "ftp://") || strings.HasPrefix(u, "git+http://") {
			add("source %s is not https", u)
		}
		if !strings.Contains(u, "://") {
			continue
		}
		h := hostOf(strings.TrimPrefix(u, "git+"))
		switch {
		case h == "":
		case reIPHost.MatchString(h):
			add("hint: source %s is a bare IP address", u)
		case !hostMatches(h, projectHost) && !hostIn(h, commonHosts):
			add("hint: source host %s is not the project's (%s) and not a well-known host", h, projectHost)
		}
	}
	for _, f := range files {
		switch {
		case f.Kind == steps.AURFileInstall:
			add("%s is an install script: it runs as root when the package is installed", f.Path)
		case strings.HasPrefix(f.Note, "symlink"):
			add("%s is a %s", f.Path, f.Note)
		case f.Note == "binary":
			add("%s is a binary file", f.Path)
		}
		if f.Text == "" || (f.Kind != steps.AURFilePKGBUILD && f.Kind != steps.AURFileInstall) {
			continue
		}
		if rePipeShell.MatchString(f.Text) {
			add("hint: %s downloads something and pipes it to a shell", f.Path)
		}
		if reSudo.MatchString(f.Text) {
			add("hint: %s calls sudo", f.Path)
		}
		if reBase64.MatchString(f.Text) {
			add("hint: %s decodes data (base64 or openssl) before using it", f.Path)
		}
	}
	return out
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostMatches: h is the project host or a subdomain of it, or the reverse.
func hostMatches(h, project string) bool {
	if project == "" {
		return false
	}
	return h == project || strings.HasSuffix(h, "."+project) || strings.HasSuffix(project, "."+h)
}

func hostIn(h string, list []string) bool {
	for _, c := range list {
		if h == c || strings.HasSuffix(h, "."+c) {
			return true
		}
	}
	return false
}

// capWriter writes at most left bytes to w and swallows the rest.
type capWriter struct {
	w    *bytes.Buffer
	left int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if n := min(len(p), c.left); n > 0 {
		c.w.Write(p[:n])
		c.left -= n
	}
	return len(p), nil
}
