package health

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

func checkRestart(_ context.Context, d Deps) Check {
	if d.RunningKernel == nil {
		return unknown(IDRestart, TitleRestart, "kernel release unknown")
	}
	running := d.RunningKernel()
	if running == "" {
		return unknown(IDRestart, TitleRestart, "kernel release unknown")
	}
	running = sanitize.Field(running, 60)
	stale, installed := report.KernelCheck(running, d.ModulesDir)
	if !stale {
		return Check{State: OK, Detail: "Running " + running}
	}
	if installed == "" {
		installed = "newer"
	}
	return Check{State: Look, Detail: sanitize.Field(fmt.Sprintf("running %s · installed %s", running, sanitize.Field(installed, 60)), DetailMax)}
}

// Disk thresholds.
const (
	rootMinBytes = 5 << 30  // / below this is Look
	rootMinPct   = 5        // ... or below this share free
	bootMinBytes = 50 << 20 // a separate /boot below this is Look
)

func checkDisk(_ context.Context, d Deps) Check {
	if d.FS == nil {
		return unknown(IDDisk, TitleDisk, "can't read disk space")
	}
	root, err := d.FS("/")
	if err != nil || root.Total == 0 {
		return unknown(IDDisk, TitleDisk, "can't read disk space")
	}
	look := root.Avail < rootMinBytes || root.Avail*100 < root.Total*rootMinPct
	detail := "/ " + ShortBytes(root.Avail) + " free"
	// /boot counts only when it is its own mount (another device than /).
	if boot, err := d.FS("/boot"); err == nil && boot.Total > 0 && boot.Dev != root.Dev {
		detail += " · /boot " + ShortBytes(boot.Avail) + " free"
		if boot.Avail < bootMinBytes {
			look = true
		}
	}
	if look {
		return Check{State: Look, Detail: detail}
	}
	return Check{State: OK, Detail: detail}
}

// ShortBytes writes n the way the sheet shows free space: binary units with
// a one-letter suffix ("118 G", "412 M", "4.2 G"), one decimal under 10.
func ShortBytes(n uint64) string {
	units := []string{"B", "K", "M", "G", "T", "P", "E"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	if v < 9.95 {
		return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " " + units[i]
}
