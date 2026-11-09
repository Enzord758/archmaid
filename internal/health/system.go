package health

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// DefaultLockFile is pacman's database lock.
const DefaultLockFile = "/var/lib/pacman/db.lck"

// pacmanProcs are the programs that hold (or make) pacman's lock; a process
// whose name is one of these, or starts with "pamac" (pamac-daemon), counts.
var pacmanProcs = map[string]bool{"pacman": true, "yay": true, "paru": true, "makepkg": true}

func isPacmanProc(name string) bool {
	return pacmanProcs[name] || strings.HasPrefix(name, "pamac")
}

func checkPacmanLock(_ context.Context, d Deps) Check {
	if d.Exists == nil || d.LockFile == "" {
		return unknown(IDPacmanLock, TitlePacmanLock, "can't check the lock")
	}
	if !d.Exists(d.LockFile) {
		return Check{State: OK, Detail: "No lock file"}
	}
	if d.Procs == nil {
		return unknown(IDPacmanLock, TitlePacmanLock, "can't list processes")
	}
	names, err := d.Procs()
	if err != nil {
		return unknown(IDPacmanLock, TitlePacmanLock, "can't list processes")
	}
	for _, n := range names {
		if isPacmanProc(n) {
			return Check{State: OK, Detail: "pacman is running"}
		}
	}
	return Check{State: Look, Detail: "pacman's lock is left over from a run that stopped",
		Copy: "sudo rm " + DefaultLockFile}
}

// maxProcs bounds how many /proc entries are looked at.
const maxProcs = 20000

// listProcs is the real Procs: the comm of every process in /proc.
func listProcs() ([]string, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []string
	for i, e := range ents {
		if i >= maxProcs {
			break
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue // the process ended, or is not ours to read
		}
		out = append(out, strings.TrimSpace(string(b)))
	}
	return out, nil
}

// readUptime is the real Uptime: /proc/uptime.
func readUptime() (time.Duration, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseUptime(string(b))
}

func parseUptime(s string) (time.Duration, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, errors.New("empty uptime")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 || v > 1e10 {
		return 0, errors.New("bad uptime")
	}
	return time.Duration(v * float64(time.Second)), nil
}

// syncGrace: the clock is allowed this long after boot to synchronize.
const syncGrace = 10 * time.Minute

func checkTimeSync(ctx context.Context, d Deps) Check {
	if d.Query == nil {
		return unknown(IDTimeSync, TitleTimeSync, "timedatectl not available")
	}
	out, err := d.Query(ctx, "timedatectl", "show", "-p", "NTP", "-p", "NTPSynchronized")
	if err != nil && len(out) == 0 {
		return unknown(IDTimeSync, TitleTimeSync, "can't read the clock state")
	}
	var ntp, synced string
	for _, line := range strings.Split(limitBytes(out, 4096), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "NTP":
			ntp = v
		case "NTPSynchronized":
			synced = v
		}
	}
	if (ntp != "yes" && ntp != "no") || (synced != "yes" && synced != "no") {
		return unknown(IDTimeSync, TitleTimeSync, "can't read the clock state")
	}
	switch {
	case ntp == "no":
		return Check{State: Look, Detail: "Automatic time sync is off"}
	case synced == "yes":
		return Check{State: OK, Detail: "Clock is synchronized"}
	}
	if d.Uptime == nil {
		return unknown(IDTimeSync, TitleTimeSync, "can't read uptime")
	}
	up, err := d.Uptime()
	if err != nil {
		return unknown(IDTimeSync, TitleTimeSync, "can't read uptime")
	}
	if up <= syncGrace {
		return Check{State: OK, Detail: "Waiting for the first sync"}
	}
	return Check{State: Look, Detail: "Clock isn't synchronized"}
}

func limitBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

// Crash listing limits.
const (
	crashOutputMax = 256 << 10 // bytes of coredumpctl output looked at
	crashLinesMax  = 200       // lines parsed
	crashNames     = 3         // executable names in the Detail
	// CrashCopy is the command the crashes check offers.
	CrashCopy = "coredumpctl list --since=-24h"
)

func checkCrashes(ctx context.Context, d Deps) Check {
	if d.Query == nil {
		return unknown(IDCrashes, TitleCrashes, "coredumpctl not available")
	}
	out, err := d.Query(ctx, "coredumpctl", "list", "--since=-24h", "--no-pager", "--no-legend", "-q")
	exes, n := parseCoredumps(limitBytes(out, crashOutputMax))
	if n == 0 {
		var exit *exec.ExitError
		if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 1) {
			return Check{State: OK, Detail: "No crashes in the last 24 h"}
		}
		return unknown(IDCrashes, TitleCrashes, "can't read the crash list")
	}
	count := fmt.Sprint(n)
	if n >= crashLinesMax {
		count += "+"
	}
	noun := "crashes"
	if n == 1 {
		noun = "crash"
	}
	var names []string
	for _, e := range exes {
		if len(names) < crashNames {
			names = append(names, sanitize.Field(e, 40))
		}
	}
	detail := fmt.Sprintf("%s %s in 24 h: %s", count, noun, strings.Join(names, ", "))
	if len(exes) > len(names) {
		detail += fmt.Sprintf(" +%d more", len(exes)-len(names))
	}
	return Check{State: Look, Detail: sanitize.Field(detail, DetailMax), Copy: CrashCopy}
}

// parseCoredumps reads `coredumpctl list --no-legend` lines:
//
//	Mon 2026-11-09 10:02:11 CET 12345 1000 1000 SIGSEGV present /usr/bin/foo 1.2M
//
// The time has a variable number of words, so a line is found by its signal
// column ("SIGSEGV", or a number), after which come the core file state and the
// executable. It returns the distinct executable base names, first seen first,
// and the number of lines understood (at most crashLinesMax).
func parseCoredumps(out string) (exes []string, n int) {
	seen := map[string]bool{}
	for i, line := range strings.Split(out, "\n") {
		if i >= crashLinesMax {
			break
		}
		f := strings.Fields(line)
		sig := -1
		for j := 3; j+2 < len(f); j++ {
			if strings.HasPrefix(f[j], "SIG") && strings.ToUpper(f[j]) == f[j] {
				sig = j
				break
			}
		}
		if sig < 0 {
			continue
		}
		exe := path.Base(f[sig+2])
		if exe == "." || exe == "/" || exe == "" {
			continue
		}
		n++
		if !seen[exe] {
			seen[exe] = true
			exes = append(exes, exe)
		}
	}
	return exes, n
}
