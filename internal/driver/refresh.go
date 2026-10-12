package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Refreshing the widget's update count. After a run whose topgrade step
// started, the engine emits engine.RefreshUpdates and the Driver calls
// Deps.RefreshUpdates, so the DMS widget's UPDATES line is current within
// seconds instead of up to 30 minutes (the timer's period). The check is
// the user unit the package ships; the widget re-reads updates-cache.json
// when the file is replaced (packaging/dms/archmaid/ArchmaidWidget.qml).

// UpdatesUnit is the user unit that runs the check, UpdatesScript what it
// runs; both are variables so tests can point them elsewhere.
var (
	UpdatesUnit   = "archmaid-updates-check.service"
	UpdatesScript = "/usr/lib/archmaid/archmaid-updates-check"
)

// refreshTimeout bounds the systemctl call: it only queues the job
// (--no-block), so it is quick unless the user manager hangs.
const refreshTimeout = 5 * time.Second

// RefreshUpdates is the Deps.RefreshUpdates both front ends set. It starts
// the unit with `systemctl --user start --no-block` (a oneshot unit that
// is already running is not started twice). If that fails (no unit, no
// user manager) and UpdatesScript exists, it starts the script in the background,
// niced (19), and does not wait for it. Otherwise
// it returns an error saying so; the Driver reports that as one line. It
// never waits for the check itself, and ctx (the Driver's) ends a hung
// systemctl.
func RefreshUpdates(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	sc := exec.CommandContext(cctx, "systemctl", "--user", "start", "--no-block", UpdatesUnit)
	sc.WaitDelay = time.Second // a killed systemctl's grandchildren may hold the pipe
	out, err := sc.CombinedOutput()
	if err == nil {
		return nil
	}
	if _, serr := os.Stat(UpdatesScript); serr != nil {
		return fmt.Errorf("driver: updates check not started: systemctl: %v (%s); no %s", err, trimOut(out), UpdatesScript)
	}
	// No Setsid or Setpgid: children share archmaid's session
	// (internal/proc, TestNoSetsid). Its stdin, stdout and stderr are
	// /dev/null, so it cannot touch the terminal.
	c := exec.Command(UpdatesScript)
	if serr := c.Start(); serr != nil {
		return fmt.Errorf("driver: updates check not started: systemctl: %v; %s: %v", err, UpdatesScript, serr)
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, c.Process.Pid, 19)
	go func() { _ = c.Wait() }() // reap it; nothing waits for the check
	return nil
}

func trimOut(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
