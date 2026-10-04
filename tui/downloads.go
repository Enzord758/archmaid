package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
// point outside $HOME (a data disk), but never at a home folder, an ancestor
// of one, or /.
var downloadsFlag string

// downloadsEnv names the environment override, the second candidate.
const downloadsEnv = "ARCHMAID_DOWNLOADS"

// errNoDownloads is the notice when no candidate qualifies.
var errNoDownloads = errors.New("No downloads folder found (set --downloads or ARCHMAID_DOWNLOADS).")

// passwdHome is the user's home folder from the passwd database ("" if it
// can't be read). A seam: tests can't change the real one.
var passwdHome = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// homes is what the resolver knows about the home folder.
type homes struct {
	home  string   // where to look: $HOME, or the passwd home when $HOME is unusable
	real  string   // home, symlinks resolved: candidates must lie strictly inside it
	guard []string // $HOME and the passwd home, resolved: never one of these, nor above one
}

// homeDirs reads $HOME and the passwd home. $HOME is the user's to set, but
// one that's unset, relative or / is no basis for "inside your home folder",
// so the passwd home stands in; and both are guarded, so a $HOME pointed
// elsewhere can't make the real home folder a candidate.
func homeDirs() homes {
	resolve := func(p string) (string, bool) {
		if p == "" || !filepath.IsAbs(p) {
			return "", false
		}
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			r = filepath.Clean(p)
		}
		return r, r != string(filepath.Separator)
	}
	var h homes
	for _, p := range []string{os.Getenv("HOME"), passwdHome()} {
		if r, ok := resolve(p); ok {
			if h.home == "" {
				h.home, h.real = filepath.Clean(p), r
			}
			h.guard = append(h.guard, r)
		}
	}
	return h
}

// resolveDownloadsDir picks the downloads folder, symlinks resolved. In order:
// --downloads, $ARCHMAID_DOWNLOADS, XDG_DOWNLOAD_DIR from user-dirs.dirs,
// ~/Downloads, ~/Download. Each must be an existing directory; every one
// except the flag must also sit strictly inside the home folder. A candidate
// that fails is skipped — except the flag: the user named that folder, so a
// bad one is reported instead of silently cleaning a different folder.
func resolveDownloadsDir() (string, error) {
	h := homeDirs()

	if downloadsFlag != "" {
		p, err := filepath.Abs(downloadsFlag)
		if err == nil {
			p, err = checkDownloadsDir(p, h, false)
		}
		if err != nil {
			return "", fmt.Errorf("--downloads %s: %w", downloadsFlag, err)
		}
		return p, nil
	}

	candidates := []string{os.Getenv(downloadsEnv), xdgDownloadDir(h.home)}
	if h.home != "" {
		candidates = append(candidates,
			filepath.Join(h.home, "Downloads"), filepath.Join(h.home, "Download"))
	}
	for _, c := range candidates {
		if c == "" || !filepath.IsAbs(c) {
			continue
		}
		if p, err := checkDownloadsDir(c, h, true); err == nil {
			return p, nil
		}
	}
	return "", errNoDownloads
}

// checkDownloadsDir resolves p and vets it: it must be an existing directory,
// and never /, a home folder ($HOME or the passwd one), or anything
// containing one. insideHome additionally requires it to be strictly inside
// h.real. The homes are resolved too, so a symlink to one (or an XDG dir of
// "$HOME/") is caught.
func checkDownloadsDir(p string, h homes, insideHome bool) (string, error) {
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
	if real == string(filepath.Separator) {
		return "", errors.New("refusing the root folder")
	}
	for _, g := range h.guard {
		if real == g || strictlyInside(g, real) {
			return "", errors.New("refusing a folder that contains your home folder")
		}
	}
	if insideHome && (h.real == "" || !strictlyInside(real, h.real)) {
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
//
// An aborted run's result is dropped (stepSeq), and finishStep never logs
// it, so a real run puts what it deleted on record itself: when it sees the
// abort, and — should the abort land after it returned but before the model
// took its result — from a context.AfterFunc that the result's commit
// (called only when the result is taken) disarms. Either way cleanup.log gets
// one entry naming exactly what was deleted.
func deleteOldDownloads(dir string, id dirID, names []string) func(context.Context) stepResult {
	return func(ctx context.Context) stepResult {
		downloadsRunning.Add(1)
		defer downloadsRunning.Done()
		res := deleteOldDownloadsNow(ctx, dir, id, names)
		if dryRun {
			return res // nothing deleted; finishStep logs the DRYRUN entry
		}
		if ctx.Err() != nil {
			logAbortedDownloads(res.output)
			return res
		}
		if ctx.Done() == nil {
			return res // can't be aborted (a test's context.Background())
		}
		downloadsRunning.Add(1) // until the AfterFunc has run or been disarmed
		stop := context.AfterFunc(ctx, func() {
			defer downloadsRunning.Done()
			logAbortedDownloads(res.output)
		})
		res.commit = func() error {
			if stop() {
				downloadsRunning.Done()
			}
			return nil
		}
		return res
	}
}

// downloadsRunning counts deletions (and their pending abort records) still
// in flight, so main can let them finish before the process exits.
var downloadsRunning sync.WaitGroup

// waitForDownloads blocks until no deletion is in flight, saying why on w if
// that takes a moment. After an abort a run stops at the end of the entry it
// is deleting, so this is short.
func waitForDownloads(w io.Writer) {
	done := make(chan struct{})
	go func() { downloadsRunning.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		fmt.Fprintln(w, "archmaid: waiting for the downloads deletion to stop…")
		<-done
	}
}

// logAbortedDownloads records an aborted real run's output — what it deleted
// and where it stopped — in place of the entry finishStep would have written.
func logAbortedDownloads(output string) {
	appendLog(logCategoryCleanup, "ABORT", "Old downloads deleted (aborted)", output)
}

// deleteOldDownloadsNow is deleteOldDownloads' work.
func deleteOldDownloadsNow(ctx context.Context, dir string, id dirID, names []string) stepResult {
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
	deleted, kept, failed := 0, 0, 0
	for _, name := range names {
		if downloadsEntryHook != nil {
			downloadsEntryHook(name)
		}
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
			r := remover{b: b, cutoff: cutoff}
			if err := r.removeTree(root, name, name); err != nil {
				errs = append(errs, err)
				failed++
				partly := ""
				if r.removed > 0 {
					partly = fmt.Sprintf(" (stopped partway: %d item(s) inside it deleted)", r.removed)
				}
				fmt.Fprintf(&out, "couldn't delete %s: %s%s\n", shownName, pathErrCause(err), partly)
				continue
			}
			deleted++
			fmt.Fprintf(&out, "deleted: %s\n", shownName)
		}
	}
	if !dryRun {
		fmt.Fprintf(&out, "\n%d deleted, %d kept, %d failed in %s\n", deleted, kept, failed, shown)
	}
	return stepResult{output: out.String(), err: errors.Join(errs...)}
}

// downloadsEntryHook, if set, runs before each entry is handled (before the
// abort check) — a seam for tests to abort mid-deletion.
var downloadsEntryHook func(name string)

// errOtherFS refuses a removal that would leave the folder's filesystem.
var errOtherFS = errors.New("another filesystem is mounted in it")

// errChanged refuses a node that is new, or was swapped, by the time the
// removal reaches it.
var errChanged = errors.New("it changed while being deleted")

// remover deletes one entry bottom-up; see removeTree.
type remover struct {
	b       boundary
	cutoff  time.Time
	removed int // nodes deleted so far (so a partial delete can say so)
	// relinked holds hard-linked files this remover has unlinked a name of:
	// that bumped their ctime, so their other names don't count as new.
	relinked map[[2]uint64]bool
}

// removeTree deletes name — an entry of root, at rel from the downloads
// folder — bottom-up by hand, because os.Root.RemoveAll would empty a
// filesystem mounted below it. Each node is Lstat'd and re-checked right
// before it is touched — not just the entry, seconds earlier, by entryIsOld:
// anything outside the boundary is refused (errOtherFS) and anything new
// since stops the delete (errChanged), so a file written into a directory
// while it is being deleted survives. A directory is checked before it is
// entered (emptying it changes its ctime), then opened as a nested os.Root
// that must be the very directory that was Lstat'd, so a swap for a symlink
// is never followed. It stops at the first error and leaves the rest.
func (r *remover) removeTree(root *os.Root, name, rel string) error {
	fi, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if r.b.crosses(rel, fi) {
		return errOtherFS
	}
	ino := inode(fi)
	if !changedBefore(fi, r.cutoff) && !r.relinked[ino] {
		return errChanged
	}
	if fi.IsDir() {
		if err := r.removeContents(root, name, rel, fi); err != nil {
			return err
		}
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	r.removed++
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && !fi.IsDir() && st.Nlink > 1 {
		if r.relinked == nil {
			r.relinked = map[[2]uint64]bool{}
		}
		r.relinked[ino] = true
	}
	return nil
}

// removeContents empties directory name of root (fi its Lstat) through
// removeTree.
func (r *remover) removeContents(root *os.Root, name, rel string, fi fs.FileInfo) error {
	sub, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer sub.Close()
	if here, err := sub.Lstat("."); err != nil {
		return err
	} else if !os.SameFile(fi, here) {
		return errChanged
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
		if err := r.removeTree(sub, k, rel+"/"+k); err != nil {
			return err
		}
	}
	return nil
}

// inode is fi's device and inode number (zero without them).
func inode(fi fs.FileInfo) [2]uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return [2]uint64{st.Dev, st.Ino}
	}
	return [2]uint64{}
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
	if home := homeDirs().real; home != "" && strictlyInside(p, home) {
		rel, _ := filepath.Rel(home, p)
		p = "~/" + rel
	}
	return Sanitize(p, SanitizePlain)
}
