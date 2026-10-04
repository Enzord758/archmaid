package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
)

// hardenProcess puts the process into a known state before anything else
// happens: a fixed environment, umask 022, cwd /, and no inherited fds
// beyond stdin/stdout/stderr. pkexec already does most of this; sudo or a
// direct root invocation may not, so the helper never relies on it.
func hardenProcess() {
	scrubEnv()
	syscall.Umask(0o022)
	if err := os.Chdir("/"); err != nil {
		os.Exit(helper.ExitFailed)
	}
	closeInheritedFDs()
}

// scrubEnv replaces the whole environment with helper.ChildEnv. Children
// also get ChildEnv explicitly (helper.ExecRunner), so it is cleared twice.
func scrubEnv() {
	os.Clearenv()
	for _, kv := range helper.ChildEnv() {
		k, v, _ := strings.Cut(kv, "=")
		os.Setenv(k, v)
	}
}

// closeInheritedFDs closes every fd above 2. The Go runtime's own netpoller
// fds (eventpoll/eventfd anon inodes) can't be told apart from inherited
// ones of the same kind and closing them would break the runtime, so those
// are only marked close-on-exec — no child can inherit them either way.
func closeInheritedFDs() {
	dir, err := os.Open("/proc/self/fd")
	if err != nil {
		return // no /proc: nothing we can enumerate safely
	}
	names, _ := dir.Readdirnames(-1)
	self := int(dir.Fd())
	for _, n := range names {
		fd, err := strconv.Atoi(n)
		if err != nil || fd <= 2 || fd == self {
			continue
		}
		target, _ := os.Readlink("/proc/self/fd/" + n)
		if target == "anon_inode:[eventpoll]" || target == "anon_inode:[eventfd]" {
			syscall.CloseOnExec(fd)
			continue
		}
		syscall.Close(fd)
	}
	dir.Close()
}
