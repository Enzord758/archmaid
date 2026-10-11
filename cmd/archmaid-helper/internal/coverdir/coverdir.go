// Package coverdir points a coverage-instrumented archmaid-helper at a
// directory baked in at build time, for its runs under pkexec. It is linked
// only into a cover build (scripts/cover-build.sh with COVER_HELPER_DIR, tag
// archmaidcover, cover_on.go); a normal build doesn't contain it, and
// scripts/check.sh fails if one does.
//
// A cover binary writes its counters to $GOCOVERDIR, read once at startup
// (the runtime's coverage init runs after every imported package's init, so
// this package's init comes first). pkexec starts the helper with a fixed,
// minimal environment, so GOCOVERDIR never gets through; Dir stands in for
// it. The helper's own environment scrub (hardenProcess) runs later, in
// main, and doesn't matter: the runtime has the directory by then.
package coverdir

import (
	"io/fs"
	"os"
	"syscall"
)

// Dir is set with -ldflags "-X …/coverdir.Dir=/var/lib/archmaid-cover".
var Dir string

func init() {
	if d := Pick(os.Getenv("GOCOVERDIR"), os.Geteuid(), Dir, os.Lstat); d != "" {
		os.Setenv("GOCOVERDIR", d)
	}
}

// Pick returns the directory to set GOCOVERDIR to, or "" to leave it alone:
// a GOCOVERDIR already set wins (a run as the user, which inherits it), and
// dir is used only by root, only if it is a real directory (no symlink)
// owned by root and not writable by group or others. Root never writes into
// a directory a user controls; without one the cover binary just warns
// "GOCOVERDIR not set" on stderr and runs as usual.
func Pick(env string, euid int, dir string, lstat func(string) (fs.FileInfo, error)) string {
	if env != "" || dir == "" || euid != 0 {
		return ""
	}
	fi, err := lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o022 != 0 {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return ""
	}
	return dir
}
