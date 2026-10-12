package testenv

import (
	"os"
	"os/exec"
)

// The reaper of last resort. Run's own check runs after m.Run, so it never
// runs when the test binary dies first: a test timeout's panic, a crash, a
// SIGKILL. Whatever the tests started then lives on, reparented to the user
// manager; a step in a session of its own (driver.Deps.Detach) doesn't even
// get the terminal's hangup. So Run also starts a watchdog: a shell that
// waits for the end of a pipe only the test binary holds, which closes
// however it ends, and then SIGKILLs every process still carrying the
// package run's marker. On a normal end Run stops it first, so its own
// check still sees, reports and fails on what a test left running.

// watchdogScript is the watchdog: $1 is the marker. It ignores the
// signals a terminal sends its process group (go test's ctrl+c), reads
// stdin until EOF, then kills, twice over to catch a fork in between, and
// removes the run's scratch dirs ($2 and $3, only if named archmaid-test-*):
// a crash, a panic in a test or a timeout skips Run's deferred cleanup, and
// they used to stay in TMPDIR.
const watchdogScript = `trap '' INT HUP TERM QUIT
while read -r _; do :; done
for round in 1 2; do
	for e in /proc/[0-9]*/environ; do
		p=${e%/environ}
		p=${p#/proc/}
		[ "$p" = "$$" ] && continue
		if tr '\0' '\n' < "$e" 2>/dev/null | grep -qx "` + MarkerEnv + `=$1"; then
			kill -KILL "$p" 2>/dev/null
		fi
	done
done
for d in "$2" "$3"; do
	case "${d##*/}" in
	archmaid-test-?*)
		for try in 1 2 3; do
			rm -rf "$d" 2>/dev/null
			[ -e "$d" ] || break
			sleep 0.2
		done
		;;
	esac
done
`

// watchdog is a running watchdog; hold keeps its pipe open.
type watchdog struct {
	cmd  *exec.Cmd
	hold *os.File
}

// startWatchdog starts the watchdog for marker. Its environment carries no
// marker (it must outlive what it kills) and no trap dir.
func startWatchdog(marker string, dirs ...string) (*watchdog, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	cmd := exec.Command("/bin/sh", append([]string{"-c", watchdogScript, "sh", marker}, dirs...)...)
	cmd.Stdin = r
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	if err := cmd.Start(); err != nil {
		w.Close()
		return nil, err
	}
	return &watchdog{cmd: cmd, hold: w}, nil
}

// stop ends the watchdog without its kill round, for a test binary that
// got to Run's own check.
func (d *watchdog) stop() {
	_ = d.cmd.Process.Kill()
	_ = d.cmd.Wait()
	d.hold.Close()
}
