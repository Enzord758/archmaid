package privilege

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// The kept password's PATH guard (docs/HANDOVER.md, "archmaid doesn't trust
// PATH for privilege"). archmaid runs su, sudo and pkexec by absolute path
// (internal/sysbin), but topgrade, yay and makepkg start sudo by name, on
// the PATH archmaid hands the step. A program called sudo in a directory
// that comes first (~/.local/bin, a relative entry, a dir an installer
// added) runs instead of /usr/bin/sudo. A script there that execs the real
// sudo with a command of its own (`exec /usr/bin/sudo -A sh -c '…'`) is
// then sudo, run by yay: the askpass request passes Request.Verify and
// Requester.KeptMayAnswer, and the kept password would answer it without a
// dialog. So the Driver scans the step's PATH when a step that gets the
// askpass starts, and turns the kept password off for the run when
// anything could stand in for sudo: every prompt then asks the user, who
// sees who asks.

// ShadowNames are the programs that, found on a step's PATH before the
// real sudo, could stand in for it or for another way to root: sudo itself,
// its rewrite, the other sudo-likes, and su and pkexec.
var ShadowNames = []string{"sudo", "sudo-rs", "doas", "run0", "please", "su", "pkexec"}

// A Shadow is what could stand in for a privileged program on a step's
// PATH: a file named one of ShadowNames in a directory before the real
// sudo's, or a relative PATH entry (searched from whatever directory the
// program that looks runs in: yay builds in the package's own folder).
type Shadow struct {
	// Path is the file, or the relative PATH entry as written ("" is the
	// current directory).
	Path string
	// Relative marks a relative PATH entry.
	Relative bool
}

// String names the shadow for a log line or the step's output.
func (s Shadow) String() string {
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
// for a privileged program: every directory before the first one that
// holds sysbin.Sudo itself (a plain file, the same file) is searched for
// ShadowNames, and a relative entry before it is one by itself. It is
// conservative: a symlink with one of those names counts whatever it
// points at (even /usr/bin/sudo: its target can change mid-run), and so
// does any regular file with an execute bit; a directory or a file without
// one doesn't (execvp skips it). Without that directory on path (or
// without sysbin.Sudo), every entry is searched. A sysbin.Sudo that is a
// bare name (only under testenv, where PATH decides every program) leaves
// nothing to compare against: nothing is a shadow.
func PathShadows(path string) []Shadow {
	real := sysbin.Sudo
	if !filepath.IsAbs(real) {
		return nil
	}
	realInfo, err := os.Stat(real)
	if err != nil {
		realInfo = nil
	}
	var out []Shadow
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			out = append(out, Shadow{Path: dir, Relative: true})
			continue
		}
		if realInfo != nil {
			if fi, err := os.Lstat(filepath.Join(dir, "sudo")); err == nil && fi.Mode().IsRegular() && os.SameFile(fi, realInfo) {
				break // the real sudo's directory: what follows can't shadow it
			}
		}
		for _, name := range ShadowNames {
			p := filepath.Join(dir, name)
			fi, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 || (fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0) {
				out = append(out, Shadow{Path: p})
			}
		}
	}
	return out
}
