package privilege

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// The kept password's PATH guard (docs/HANDOVER.md, "archmaid doesn't trust
// PATH for privilege"). archmaid runs su, sudo and pkexec by absolute path
// (internal/sysbin), but topgrade, paru, yay and makepkg start sudo by name, on
// the PATH archmaid hands the step. A program called sudo in a directory
// that comes first (~/.local/bin, a relative entry, a dir an installer
// added) runs instead of /usr/bin/sudo. A script there that execs the real
// sudo with a command of its own (`exec /usr/bin/sudo -A sh -c '…'`) is
// then sudo, run by paru or yay: the askpass request passes Request.Verify and
// Requester.KeptMayAnswer, and the kept password would answer it without a
// dialog. So the Driver scans the step's PATH when a step that gets the
// askpass starts, and turns the kept password off for the run when
// anything could stand in for sudo: every prompt then asks the user, who
// sees who asks.

// ShadowNames are the programs that, found on a step's PATH before the
// system's own file of that name (or anywhere, for a name the system
// lacks), could stand in for sudo or for another way to root: sudo itself,
// its rewrite, the other sudo-likes, and su and pkexec.
var ShadowNames = []string{"sudo", "sudo-rs", "doas", "run0", "please", "su", "pkexec"}

// A Shadow is what could stand in for a privileged program on a step's
// PATH: a file named one of ShadowNames in a directory before the system's
// own file of that name (PathShadows), or a relative PATH entry (searched
// from whatever directory the program that looks runs in: the AUR helper builds in
// the package's own folder).
type Shadow struct {
	// Path is the file, or the relative PATH entry as written ("" is the
	// current directory).
	Path string
	// Relative marks a relative PATH entry.
	Relative bool
	// Note, for a config file (ConfigShadows), is what in it is the risk.
	Note string
}

// String names the shadow for a log line or the step's output.
func (s Shadow) String() string {
	if s.Note != "" {
		return s.Path + " " + s.Note
	}
	if s.Relative {
		if s.Path == "" {
			return "an empty PATH entry (the current folder)"
		}
		return `the relative PATH entry "` + s.Path + `"`
	}
	return s.Path
}

// ChildPATH is the PATH a child started with env (nil: archmaid's own
// environment, as proc.Spec's Env) and then extra (proc.Spec's ExtraEnv)
// gets: the last PATH= among them, as exec keeps the last value of a name
// given twice. With none, it is what execvp searches then (glibc's
// default, /bin:/usr/bin).
func ChildPATH(env, extra []string) string {
	if env == nil {
		env = os.Environ()
	}
	path, ok := "", false
	for _, list := range [][]string{env, extra} {
		for _, kv := range list {
			if v, found := strings.CutPrefix(kv, "PATH="); found {
				path, ok = v, true
			}
		}
	}
	if !ok {
		return "/bin:/usr/bin"
	}
	return path
}

// PathShadows scans path, a PATH value, for anything that could stand in
// for a privileged program. Each of ShadowNames has its own boundary, the
// first directory on path that holds the system's own file of that name,
// the same file as the one in sysbin.Sudo's directory (/usr/bin, also
// reached as /bin or /usr/sbin): for sudo that is sysbin.Sudo itself.
// Before its boundary, a file of that name is a shadow; from there on,
// execvp finds the real one first. A name the system doesn't have there
// (doas, please and sudo-rs on most Arch machines) has no boundary: it is
// searched on the whole path, since a program that looks for it (topgrade
// tries doas and please before sudo) finds whichever comes first. A
// relative entry (searched from whatever directory the program that looks
// runs in) is a shadow by itself while any name's boundary is still to
// come, and so is an empty entry, the current directory (an empty path is
// one: execvp, bash and Rust's split_paths search "." for PATH=).
//
// archmaid's own sudo wrapper (the TUI's, sudowrap.go), first on a step's
// PATH, is sudo's boundary too, but only that file at that path as this
// process wrote it (same file, mode and contents): a file put in its place
// or a copy of it elsewhere counts like any other.
//
// It is conservative: a symlink with one of those names counts whatever it
// points at (even /usr/bin/sudo: its target can change mid-run), and so
// does any regular file with an execute bit; a directory or a file without
// one doesn't (execvp skips it). Without sysbin.Sudo's directory, every
// entry is searched for every name. A sysbin.Sudo that is a bare name
// (only under testenv, where PATH decides every program) leaves nothing to
// compare against: nothing is a shadow.
func PathShadows(path string) []Shadow {
	if !filepath.IsAbs(sysbin.Sudo) {
		return nil
	}
	sysDir := filepath.Dir(sysbin.Sudo)
	// real[i] is the system's own file named ShadowNames[i], not followed
	// (/usr/bin/run0 is a symlink to systemd-run: the same link inode
	// through /bin is the system's, a user's own link is not); nil when
	// the system has none.
	real := make([]os.FileInfo, len(ShadowNames))
	for i, name := range ShadowNames {
		file := filepath.Join(sysDir, name)
		if name == "sudo" {
			file = sysbin.Sudo
		}
		if fi, err := os.Lstat(file); err == nil {
			real[i] = fi
		}
	}
	passed := make([]bool, len(ShadowNames)) // past that name's boundary
	open := len(ShadowNames)                 // names still before theirs
	dirs := filepath.SplitList(path)
	if path == "" {
		dirs = []string{""} // SplitList("") is no entry; execvp's is "."
	}
	var out []Shadow
	for _, dir := range dirs {
		if open == 0 {
			break
		}
		if !filepath.IsAbs(dir) {
			out = append(out, Shadow{Path: dir, Relative: true})
			continue
		}
		for i, name := range ShadowNames {
			if passed[i] {
				continue
			}
			p := filepath.Join(dir, name)
			fi, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if real[i] != nil && os.SameFile(fi, real[i]) {
				passed[i], open = true, open-1 // the system's own: what follows can't shadow it
				continue
			}
			if name == "sudo" && ownSudo.is(p, fi) {
				// archmaid's own sudo wrapper, as this process wrote it
				// (sudowrap.go): it execs sysbin.Sudo, so it is the way
				// to the real sudo, and what follows can't shadow it.
				passed[i], open = true, open-1
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 || (fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0) {
				out = append(out, Shadow{Path: p})
			}
		}
	}
	return out
}
