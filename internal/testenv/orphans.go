package testenv

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// proc is one /proc/<pid>/stat entry.
type proc struct {
	pid, ppid, pgid int
	state           byte
	comm            string
}

// reapLeftovers finds the processes the package's tests left running (see
// leftovers), SIGKILLs each and every process group one of them leads, and
// returns them as "pid comm cmdline", sorted by pid. It rescans until none is
// left, so one that forks while it is being killed is caught too; an error
// means /proc couldn't be read or something survived SIGKILL.
func reapLeftovers(marker string) ([]string, error) {
	self, ownGroup := os.Getpid(), syscall.Getpgrp()
	seen := map[int]string{}
	var killed []int
	for range 50 {
		left, err := leftovers(marker, self)
		if err != nil {
			return report(seen), err
		}
		if len(left) == 0 {
			reap(killed)
			return report(seen), nil
		}
		for _, p := range left {
			if _, ok := seen[p.pid]; !ok {
				seen[p.pid] = fmt.Sprintf("%d %s %s", p.pid, p.comm, cmdline(p.pid))
				killed = append(killed, p.pid)
			}
			// A group one of them leads was made by a test's process; never
			// the test binary's own (go test's, the shell's).
			if p.pgid == p.pid && p.pgid > 1 && p.pgid != ownGroup {
				_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
			}
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
		reap(killed)
		time.Sleep(20 * time.Millisecond)
	}
	left, _ := leftovers(marker, self)
	var still []string
	for _, p := range left {
		still = append(still, strconv.Itoa(p.pid))
	}
	return report(seen), errors.New("still running after SIGKILL: " + strings.Join(still, " "))
}

// report lists seen's descriptions by pid.
func report(seen map[int]string) []string {
	pids := make([]int, 0, len(seen))
	for pid := range seen {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	out := make([]string, 0, len(pids))
	for _, pid := range pids {
		out = append(out, seen[pid])
	}
	return out
}

// reap collects the exit status of those of pids that are this process's
// (now dead) children, so no zombie is left; others are their parents' job.
func reap(pids []int) {
	for _, pid := range pids {
		var ws syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	}
}

// leftovers returns the live processes of this uid, other than self, that
// carry MarkerEnv=marker or descend from self (which, as a child subreaper,
// inherits every orphan below it). Zombies are dead already and don't count.
func leftovers(marker string, self int) ([]proc, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid())
	all := map[int]proc{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		info, err := os.Stat("/proc/" + e.Name())
		if err != nil {
			continue // gone
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
			continue
		}
		if p, ok := readStat(pid); ok {
			all[pid] = p
		}
	}
	if len(all) == 0 {
		return nil, errors.New("no process of this uid in /proc: can't see the tests' processes")
	}
	descends := func(pid int) bool {
		for range 4096 {
			p, ok := all[pid]
			if !ok {
				return false
			}
			if p.ppid == self {
				return true
			}
			pid = p.ppid
		}
		return false
	}
	var out []proc
	for pid, p := range all {
		if p.state == 'Z' || p.state == 'X' {
			continue
		}
		if descends(pid) || hasMarker(pid, marker) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b proc) int { return a.pid - b.pid })
	return out, nil
}

// readStat parses /proc/<pid>/stat: "pid (comm) state ppid pgrp ...", where
// comm may itself hold spaces and parentheses.
func readStat(pid int) (proc, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return proc{}, false
	}
	return parseStat(string(b))
}

func parseStat(s string) (proc, bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return proc{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return proc{}, false
	}
	f := strings.Fields(s[end+1:])
	if len(f) < 3 || len(f[0]) != 1 {
		return proc{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgid, err2 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil {
		return proc{}, false
	}
	return proc{pid: pid, ppid: ppid, pgid: pgid, state: f[0][0], comm: s[open+1 : end]}, true
}

// hasMarker reports whether pid's environment holds MarkerEnv=marker.
func hasMarker(pid int, marker string) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	want := MarkerEnv + "=" + marker
	for _, kv := range strings.Split(string(b), "\x00") {
		if kv == want {
			return true
		}
	}
	return false
}

// cmdline is pid's command line, its arguments joined by spaces.
func cmdline(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}
