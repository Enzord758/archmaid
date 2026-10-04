package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// ── what is "old", and deleting it ───────────────────────────────────────────
//
// This used to be `find "$dir" -maxdepth 1 -mtime +30 -exec rm -rf {} +`,
// which also matches "$dir" itself: once the folder's own mtime was a month
// old, the whole folder went, new files included. And mtime is the wrong
// clock — wget, curl -R and unzip stamp files with the server's/archive's old
// mtime, so a fresh download could look years old.
//
// Now: only the folder's entries are candidates (os.ReadDir — never the
// folder), age is ctime (which those tools can't set; a rename into the
// folder bumps it too), a directory entry counts only if nothing inside it is
// new, and symlinks are judged and removed as links, never followed.
//
// Nothing on another filesystem is touched either. A mount point inside the
// folder (a USB disk, an sshfs, a bind mount of ~/Documents) shows that
// filesystem's files with their own old ctimes, and RemoveAll would empty
// it. An entry that is, or holds, a mount point is never old (see boundary),
// and the delete re-checks every node it touches (removeTree).

// downloadsMaxAge is how long ago an entry's ctime must be for it to go.
const downloadsMaxAge = 30 * 24 * time.Hour

// downloadsNow is the clock for the age check — a seam for tests, which can't
// set ctimes and instead move "now".
var downloadsNow = time.Now

// downloadsConfirmList is how many entries the confirm names before
// "…and N more".
const downloadsConfirmList = 10

// discoverDownloads resolves the folder and lists its old entries. Deleting
// them is an in-process step that re-checks each entry first (see
// deleteOldDownloads).
func discoverDownloads() tea.Cmd {
	return func() tea.Msg {
		notice := func(s string) tea.Msg { return discoveryMsg{kind: "downloads", notice: s} }
		dir, err := resolveDownloadsDir()
		if err != nil {
			return notice(Sanitize(err.Error(), SanitizePlain))
		}
		shown := displayPath(dir)
		root, err := os.OpenRoot(dir)
		if err != nil {
			return notice("Couldn't open " + shown + ": " + pathErrCause(err))
		}
		defer root.Close()
		id, err := dirIdentity(root)
		if err != nil {
			return notice("Couldn't read " + shown + ": " + pathErrCause(err))
		}
		b, err := newBoundary(dir, id)
		if err != nil {
			return notice("Couldn't read the mount table: " + pathErrCause(err))
		}
		names, err := oldDownloads(root.FS(), downloadsNow().Add(-downloadsMaxAge), b)
		if err != nil {
			return notice("Couldn't read " + shown + ": " + pathErrCause(err))
		}
		if len(names) == 0 {
			return notice("Nothing older than 30 days in " + shown + ".")
		}
		return discoveryMsg{kind: "downloads", pending: []step{{
			title: "Deleting old downloads…", logLabel: "Old downloads deleted",
			run:      deleteOldDownloads(dir, id, names),
			optional: true, alwaysLog: true,
			confirm: downloadsConfirm(shown, names),
		}}}
	}
}

// downloadsConfirm asks about the entries by name: the first
// downloadsConfirmList of them, then a count of the rest.
func downloadsConfirm(shown string, names []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Delete %d item(s) older than 30 days in %s?\n", len(names), shown)
	for i, n := range names {
		if i == downloadsConfirmList {
			fmt.Fprintf(&b, "\n  …and %d more", len(names)-i)
			break
		}
		b.WriteString("\n  " + displayName(n))
	}
	return b.String()
}

// oldDownloads lists the entries of fsys's top level that entryIsOld judges
// older than cutoff, sorted by name. The folder itself is never one.
func oldDownloads(fsys fs.FS, cutoff time.Time, b boundary) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if entryIsOld(fsys, e.Name(), cutoff, b) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// errFresh stops entryIsOld's walk at the first new file.
var errFresh = errors.New("fresh")

// entryIsOld reports whether the entry's ctime is before cutoff — and, for a
// directory, that of everything inside it, so a folder still being used
// survives. Nothing is followed: a symlink is judged as the link itself
// (Lstat), and the walk doesn't descend through links. Nor does it leave b:
// an entry that is, or holds, a mount point or anything on another device
// counts as new, and the walk stops there. Anything that can't be inspected
// counts as new.
func entryIsOld(fsys fs.FS, name string, cutoff time.Time, b boundary) bool {
	fi, err := fs.Lstat(fsys, name)
	if err != nil || b.crosses(name, fi) || !changedBefore(fi, cutoff) {
		return false
	}
	if !fi.IsDir() {
		return true
	}
	err = fs.WalkDir(fsys, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == name {
			return nil // judged above, by Lstat
		}
		info, err := d.Info() // an Lstat: links are not followed
		if err != nil {
			return err
		}
		if b.crosses(p, info) || !changedBefore(info, cutoff) {
			return errFresh // and never descend into another filesystem
		}
		return nil
	})
	return err == nil
}

// changedBefore reports whether fi's ctime is before cutoff. Without a ctime
// to go by, it isn't.
func changedBefore(fi fs.FileInfo, cutoff time.Time) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return time.Unix(st.Ctim.Unix()).Before(cutoff)
}

// dirID is the downloads folder's device (st_dev) and inode.
type dirID struct{ dev, ino uint64 }

// dirIdentity is the folder's device and inode, so deletion can tell that the
// folder it opens is still the one that was scanned — and which filesystem
// it must never leave.
func dirIdentity(root *os.Root) (dirID, error) {
	fi, err := root.Stat(".")
	if err != nil {
		return dirID{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return dirID{}, errors.New("no inode information")
	}
	return dirID{st.Dev, st.Ino}, nil
}

// statDev is the device (st_dev) fi lives on; false without one. A seam:
// tests stand in another filesystem without mounting one.
var statDev = func(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Dev, true
}

// mountinfoPath is the mount table; a seam for tests.
var mountinfoPath = "/proc/self/mountinfo"

// boundary is the one filesystem the cleaner works in: the folder's device,
// minus every mount point inside the folder. A bind mount (of ~/Documents,
// say) keeps the device, so st_dev alone can't see it; the mount table can.
type boundary struct {
	dev    uint64
	mounts map[string]bool // mount points strictly inside the folder, as paths from it ("a/b")
}

// newBoundary is the boundary of dir (resolved; id its identity), with the
// mount table read now. An unreadable mount table is an error: without it a
// bind mount would pass for an ordinary folder.
func newBoundary(dir string, id dirID) (boundary, error) {
	data, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return boundary{}, err
	}
	b := boundary{dev: id.dev, mounts: map[string]bool{}}
	for _, line := range strings.Split(string(data), "\n") {
		// mount-id parent-id major:minor root mount-point options… (proc(5))
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		if mp := filepath.Clean(unescapeMountPath(f[4])); strictlyInside(mp, dir) {
			rel, _ := filepath.Rel(dir, mp)
			b.mounts[filepath.ToSlash(rel)] = true
		}
	}
	return b, nil
}

// unescapeMountPath undoes the mount table's octal escapes (\040 for a space;
// tab, newline and backslash likewise).
func unescapeMountPath(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// crosses reports whether the node at rel (its "/"-separated path from the
// folder), described by its Lstat fi, is outside b: on another device, a
// mount point, or of unknown device. A mount point Lstats as the root of
// what is mounted there, so another filesystem shows by its device.
func (b boundary) crosses(rel string, fi fs.FileInfo) bool {
	dev, ok := statDev(fi)
	return !ok || dev != b.dev || b.mounts[rel]
}

// deleteOldDownloads is the delete step's run: the confirm may have sat open
// for a while, so right before each removal it re-checks the entry (an entry
// touched or replaced since the scan, or with a filesystem mounted in it
// since, is kept), and it removes through an os.Root on the folder, so no
// path — however it's been swapped for a symlink in the meantime — can
// resolve outside it. It removes with removeTree, never os.Root.RemoveAll,
// which would descend into a mount point. If the folder itself is no longer
// the one scanned, nothing is deleted. In a dry run it only reports. An abort
// cancels ctx, which stops it between entries: nothing after that is touched.
func deleteOldDownloads(dir string, id dirID, names []string) func(context.Context) stepResult {
	return func(ctx context.Context) stepResult {
		shown := displayPath(dir)
		root, err := os.OpenRoot(dir)
		if err != nil {
			return stepResult{err: err}
		}
		defer root.Close()
		if got, err := dirIdentity(root); err != nil || got != id {
			return stepResult{
				output: shown + " is not the folder that was scanned anymore; nothing deleted.",
				err:    errors.New("downloads folder changed since the scan"),
			}
		}
		b, err := newBoundary(dir, id)
		if err != nil {
			return stepResult{output: "Couldn't read the mount table; nothing deleted.", err: err}
		}

		cutoff := downloadsNow().Add(-downloadsMaxAge)
		var out strings.Builder
		var errs []error
		deleted, kept := 0, 0
		for _, name := range names {
			if ctx.Err() != nil {
				fmt.Fprintf(&out, "stopped: aborted before %s\n", displayName(name))
				errs = append(errs, ctx.Err())
				break
			}
			shownName := displayName(name)
			switch {
			case !entryIsOld(root.FS(), name, cutoff, b):
				kept++
				fmt.Fprintf(&out, "kept (changed since the scan): %s\n", shownName)
			case dryRun:
				fmt.Fprintf(&out, "[dry-run] would delete: %s\n", shownName)
			default:
				if err := removeTree(root, name, name, b); err != nil {
					errs = append(errs, err)
					fmt.Fprintf(&out, "couldn't delete %s: %s\n", shownName, pathErrCause(err))
					continue
				}
				deleted++
				fmt.Fprintf(&out, "deleted: %s\n", shownName)
			}
		}
		if !dryRun {
			fmt.Fprintf(&out, "\n%d deleted, %d kept, %d failed in %s\n", deleted, kept, len(errs), shown)
		}
		return stepResult{output: out.String(), err: errors.Join(errs...)}
	}
}

// errOtherFS refuses a removal that would leave the folder's filesystem.
var errOtherFS = errors.New("another filesystem is mounted in it")

// errSwapped refuses a directory that was swapped between its Lstat and
// being opened.
var errSwapped = errors.New("it changed while being deleted")

// removeTree deletes name — an entry of root, at rel from the downloads
// folder — bottom-up by hand, because os.Root.RemoveAll would empty a
// filesystem mounted below it. Each node is Lstat'd right before it is
// touched, and nothing outside b is entered or removed (errOtherFS). A
// directory is opened as a nested os.Root and must be the very directory
// that was Lstat'd, so a swap for a symlink is never followed. It stops at
// the first error and leaves the rest.
func removeTree(root *os.Root, name, rel string, b boundary) error {
	fi, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if b.crosses(rel, fi) {
		return errOtherFS
	}
	if fi.IsDir() {
		if err := removeContents(root, name, rel, fi, b); err != nil {
			return err
		}
	}
	return root.Remove(name)
}

// removeContents empties directory name of root (fi its Lstat) through
// removeTree.
func removeContents(root *os.Root, name, rel string, fi fs.FileInfo, b boundary) error {
	sub, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer sub.Close()
	if here, err := sub.Lstat("."); err != nil {
		return err
	} else if !os.SameFile(fi, here) {
		return errSwapped
	}
	d, err := sub.Open(".")
	if err != nil {
		return err
	}
	kids, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, k := range kids {
		if err := removeTree(sub, k, rel+"/"+k, b); err != nil {
			return err
		}
	}
	return nil
}

// pathErrCause is err without the path a *fs.PathError repeats — the caller
// already names the entry, safely.
func pathErrCause(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// displayName renders an entry name on one line: a name with control
// characters (a newline would forge extra list lines; escapes could restyle
// the popup) or invalid UTF-8 is shown quoted with escapes, and a long one is
// cut to fit the popup.
func displayName(name string) string {
	if Sanitize(name, SanitizePlain) != name || strings.ContainsAny(name, "\n\t") {
		name = strconv.Quote(name)
	}
	if r := []rune(name); len(r) > 60 {
		name = string(r[:59]) + "…"
	}
	return name
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
