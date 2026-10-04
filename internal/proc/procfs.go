package proc

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Info is what /proc says about one process.
type Info struct {
	PPid    int    // parent pid: field 4 of /proc/<pid>/stat
	Pgrp    int    // process group: field 5
	Session int    // session id: field 6
	Start   uint64 // start time: field 22, in clock ticks since boot
	Comm    string // field 2: the executed file's name
	EUID    int    // effective uid, from /proc/<pid>/status (-1 if absent or unparsable, never a guessed 0)
	Exe     string // /proc/<pid>/exe, or "" when we may not read it
}

// Read reads pid's Info from /proc. The exe link of a process we may not
// ptrace — another user's, or a setuid one like sudo — is unreadable; Exe is
// then left empty rather than failing the lookup.
func Read(pid int) (Info, error) {
	return readAt("/proc/"+strconv.Itoa(pid), pid)
}

// readAt is Read of the /proc/<pid> directory dir: a seam, so tests can
// hand it a fake one.
func readAt(dir string, pid int) (Info, error) {
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return Info{}, err
	}
	// "pid (comm) state ppid …": comm may hold spaces and parentheses itself,
	// so it runs from the first "(" to the last ")".
	open, end := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return Info{}, fmt.Errorf("pid %d: malformed stat", pid)
	}
	f := strings.Fields(string(stat[end+1:])) // f[0] is field 3
	if len(f) < 20 {
		return Info{}, fmt.Errorf("pid %d: short stat", pid)
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgrp, err2 := strconv.Atoi(f[2])
	sid, err3 := strconv.Atoi(f[3])
	start, err4 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Info{}, fmt.Errorf("pid %d: malformed stat", pid)
	}
	info := Info{PPid: ppid, Pgrp: pgrp, Session: sid, Start: start, Comm: string(stat[open+1 : end]), EUID: -1}

	status, err := os.ReadFile(dir + "/status")
	if err != nil {
		return Info{}, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			if ids := strings.Fields(rest); len(ids) >= 2 { // real, effective, …
				// An unparsable euid is unknown (-1), never 0: Atoi's
				// zero on failure would read as root, which is exactly
				// what the askpass check trusts (isSudo).
				if euid, err := strconv.Atoi(ids[1]); err == nil && euid >= 0 {
					info.EUID = euid
				}
			}
		}
	}
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		info.Exe = strings.TrimSuffix(exe, " (deleted)")
	}
	return info, nil
}
